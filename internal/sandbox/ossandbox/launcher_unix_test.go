//go:build linux || darwin

package ossandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestTrustedLauncherRejectsWritablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unshare")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o777); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(path))
	if _, err := trustedLauncher("unshare"); err == nil {
		t.Fatal("writable launcher accepted")
	}
}

func TestTrustedLauncherResolvesSystemExecutable(t *testing.T) {
	path, err := trustedLauncher("/usr/bin/true")
	if err != nil || !filepath.IsAbs(path) {
		t.Fatalf("trusted executable: %q, %v", path, err)
	}
	if err := probeLauncher(path); err != nil {
		t.Fatal(err)
	}
	if err := probeLauncher(path, "ignored"); err != nil {
		t.Fatal(err)
	}
	falsePath, err := exec.LookPath("false")
	if err != nil {
		t.Fatal(err)
	}
	if err := probeLauncher(falsePath); err == nil {
		t.Fatal("failed capability probe accepted")
	}
}

func TestLauncherCapabilityProbeFailure(t *testing.T) {
	const marker = "launcher-probe-denied"
	const details = "namespace creation not permitted"
	if os.Args[len(os.Args)-1] == marker {
		fmt.Fprintln(os.Stderr, details)
		os.Exit(1)
	}
	err := probeLauncher(os.Args[0], "-test.run=^TestLauncherCapabilityProbeFailure$", marker)
	if !errors.Is(err, errLauncherCapabilityUnavailable) || !strings.Contains(err.Error(), details) {
		t.Fatalf("missing capability classification or probe diagnostics: %v", err)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("probe exit status was lost: %v", err)
	}
}

func TestLauncherProbeStartFailureIsNotCapabilityUnavailable(t *testing.T) {
	err := probeLauncher(filepath.Join(t.TempDir(), "missing-launcher"))
	if err == nil || errors.Is(err, errLauncherCapabilityUnavailable) {
		t.Fatalf("launcher start failure treated as missing capability: %v", err)
	}
}

func TestLauncherEnvironmentRejectsDynamicLoaderInjection(t *testing.T) {
	cmd := exec.Command("never-started")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LD_PRELOAD=/tmp/inject.so", "LD_LIBRARY_PATH=/tmp", "DYLD_INSERT_LIBRARIES=/tmp/inject.dylib", "KEEP=value"}
	prepareLauncherEnvironment(cmd)
	if len(cmd.Env) != 2 || cmd.Env[1] != "KEEP=value" {
		t.Fatalf("unsafe launcher environment: %v", cmd.Env)
	}
}

func TestChildNetworkRestrictionIntegration(t *testing.T) {
	if address := os.Getenv("COVO_NETWORK_TEST_ADDRESS"); address != "" {
		connection, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
		if err != nil {
			os.Exit(3)
		}
		connection.Close()
		os.Exit(0)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	makeCommand := func() *exec.Cmd {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t.Cleanup(cancel)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestChildNetworkRestrictionIntegration$")
		cmd.Env = append(os.Environ(), "COVO_NETWORK_TEST_ADDRESS="+listener.Addr().String())
		return cmd
	}
	if output, err := makeCommand().CombinedOutput(); err != nil {
		t.Fatalf("unrestricted connection failed: %v %s", err, output)
	}
	globalState.mu.Lock()
	previousApplied, previousRestriction := globalState.applied, globalState.restrictNetwork
	globalState.applied, globalState.restrictNetwork = true, true
	globalState.mu.Unlock()
	defer func() {
		globalState.mu.Lock()
		globalState.applied, globalState.restrictNetwork = previousApplied, previousRestriction
		globalState.mu.Unlock()
	}()
	cmd := makeCommand()
	originalPath := cmd.Path
	originalArgs := append([]string(nil), cmd.Args...)
	if err := PrepareChildCommand(context.Background(), cmd); err != nil {
		if cmd.Process != nil || cmd.Path != originalPath || !slices.Equal(cmd.Args, originalArgs) {
			t.Fatalf("failed preparation started or modified the child command: %v", err)
		}
		if errors.Is(err, errLauncherCapabilityUnavailable) {
			t.Skipf("child network isolation is unavailable on this host: %v", err)
		}
		t.Fatalf("network wrapper preparation failed: %v", err)
	}
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("restricted child reached network: %s", output)
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 3 {
		t.Fatalf("child did not reach network-denial check: %v %s", err, output)
	}
}
