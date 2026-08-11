package sandbox

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestDetectType(t *testing.T) {
	// Save and restore env
	origSandboxType := os.Getenv("SANDBOX_TYPE")
	origSSHHost := os.Getenv("SSH_HOST")
	origDockerImage := os.Getenv("DOCKER_IMAGE")
	origSandboxImage := os.Getenv("SANDBOX_IMAGE")
	defer func() {
		os.Setenv("SANDBOX_TYPE", origSandboxType)
		os.Setenv("SSH_HOST", origSSHHost)
		os.Setenv("DOCKER_IMAGE", origDockerImage)
		os.Setenv("SANDBOX_IMAGE", origSandboxImage)
	}()

	tests := []struct {
		name       string
		sandbox    string
		sshHost    string
		dockerImg  string
		sandboxImg string
		want       Type
	}{
		{"default local", "", "", "", "", TypeLocal},
		{"explicit local", "local", "", "", "", TypeLocal},
		{"explicit docker", "docker", "", "", "", TypeDocker},
		{"explicit ssh", "ssh", "", "", "", TypeSSH},
		{"ssh host implies ssh", "", "1.2.3.4", "", "", TypeSSH},
		{"docker image implies docker", "", "", "ubuntu:22.04", "", TypeDocker},
		{"sandbox image implies docker", "", "", "", "alpine:3", TypeDocker},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("SANDBOX_TYPE", tt.sandbox)
			os.Setenv("SSH_HOST", tt.sshHost)
			os.Setenv("DOCKER_IMAGE", tt.dockerImg)
			os.Setenv("SANDBOX_IMAGE", tt.sandboxImg)
			if got := DetectType(); got != tt.want {
				t.Errorf("DetectType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfigFromEnv(t *testing.T) {
	orig := os.Getenv("SANDBOX_TYPE")
	defer os.Setenv("SANDBOX_TYPE", orig)

	os.Setenv("SANDBOX_TYPE", "local")
	cfg := ConfigFromEnv()
	if cfg.Type != TypeLocal {
		t.Errorf("expected TypeLocal, got %v", cfg.Type)
	}
	if cfg.WorkDir == "" {
		t.Error("expected non-empty WorkDir")
	}
	if cfg.Env == nil {
		t.Error("expected non-nil Env map")
	}
}

func TestNew_Local(t *testing.T) {
	sb, err := New(Config{Type: TypeLocal, WorkDir: "/tmp"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if sb.Type() != TypeLocal {
		t.Errorf("expected TypeLocal, got %v", sb.Type())
	}
	if err := sb.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
}

func TestNew_Default(t *testing.T) {
	sb, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if sb.Type() != TypeLocal {
		t.Errorf("expected TypeLocal for default, got %v", sb.Type())
	}
	_ = sb.Close()
}

func TestLocalSandbox_Run(t *testing.T) {
	sb, err := newLocalSandbox(Config{WorkDir: "/tmp"})
	if err != nil {
		t.Fatalf("newLocalSandbox() error: %v", err)
	}
	defer sb.Close()

	result, err := sb.Run(context.Background(), "echo hello", 5*time.Second)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if result.Stdout != "hello\n" {
		t.Errorf("expected stdout 'hello\\n', got %q", result.Stdout)
	}
}

func TestLocalSandbox_RunTimeout(t *testing.T) {
	sb, _ := newLocalSandbox(Config{WorkDir: "/tmp"})
	defer sb.Close()

	result, err := sb.Run(context.Background(), "sleep 10", 100*time.Millisecond)
	// context timeout kills the process; we may get err or a non-zero exit code
	if err == nil && result.ExitCode == 0 {
		t.Error("expected timeout to cause error or non-zero exit code")
	}
}

func TestLocalSandbox_RunNonZeroExit(t *testing.T) {
	sb, _ := newLocalSandbox(Config{WorkDir: "/tmp"})
	defer sb.Close()

	result, err := sb.Run(context.Background(), "exit 42", 5*time.Second)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", result.ExitCode)
	}
}

func TestResolveShell(t *testing.T) {
	origShell := os.Getenv("SHELL")
	defer os.Setenv("SHELL", origShell)

	os.Setenv("SHELL", "/bin/bash")
	got := resolveShell("echo test")
	if got == "" {
		t.Error("expected non-empty shell command")
	}
	// Should contain the shell path and the command
	if !contains(got, "/bin/bash") {
		t.Errorf("expected shell to contain /bin/bash, got %q", got)
	}
	if !contains(got, "echo test") {
		t.Errorf("expected shell to contain 'echo test', got %q", got)
	}
}

func TestEscapeShell(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"it's", "it'\\''s"},
		{"a'b'c", "a'\\''b'\\''c"},
	}
	for _, tt := range tests {
		if got := escapeShell(tt.input); got != tt.want {
			t.Errorf("escapeShell(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFirstEnv(t *testing.T) {
	origKey1 := os.Getenv("TEST_FIRST_ENV_1")
	origKey2 := os.Getenv("TEST_FIRST_ENV_2")
	defer func() {
		os.Setenv("TEST_FIRST_ENV_1", origKey1)
		os.Setenv("TEST_FIRST_ENV_2", origKey2)
	}()

	os.Setenv("TEST_FIRST_ENV_1", "")
	os.Setenv("TEST_FIRST_ENV_2", "value2")
	got := firstEnv("TEST_FIRST_ENV_1", "TEST_FIRST_ENV_2")
	if got != "value2" {
		t.Errorf("expected 'value2', got %q", got)
	}

	os.Setenv("TEST_FIRST_ENV_1", "value1")
	got = firstEnv("TEST_FIRST_ENV_1", "TEST_FIRST_ENV_2")
	if got != "value1" {
		t.Errorf("expected 'value1', got %q", got)
	}

	got = firstEnv("NONEXISTENT_KEY_1", "NONEXISTENT_KEY_2")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestDockerSandbox_NoDocker(t *testing.T) {
	// Save PATH and remove docker
	origPath := os.Getenv("PATH")
	defer os.Setenv("PATH", origPath)
	os.Setenv("PATH", "/nonexistent")

	_, err := newDockerSandbox(Config{Image: "ubuntu:22.04"})
	if err == nil {
		t.Error("expected error when docker not found")
	}
}

func TestSSHSandbox_NoSSH(t *testing.T) {
	origPath := os.Getenv("PATH")
	defer os.Setenv("PATH", origPath)
	os.Setenv("PATH", "/nonexistent")

	_, err := newSSHSandbox(Config{SSHHost: "example.com"})
	if err == nil {
		t.Error("expected error when ssh not found")
	}
}

func TestSSHSandbox_NoHost(t *testing.T) {
	_, err := newSSHSandbox(Config{})
	if err == nil {
		t.Error("expected error when SSH_HOST not set")
	}
}

func TestDockerContainerName_Stable(t *testing.T) {
	a := dockerContainerName("my-task-123")
	b := dockerContainerName("my-task-123")
	if a != b {
		t.Fatalf("expected dockerContainerName to be stable for the same input, got %q vs %q", a, b)
	}
	if dockerContainerName("other-task") == a {
		t.Fatalf("expected different persistent IDs to produce different names")
	}
	if !strings.HasPrefix(a, "covo-sandbox-") {
		t.Fatalf("expected covo-sandbox- prefix, got %q", a)
	}
}

func TestNewDockerSandbox_PersistentIDSetsContainerName(t *testing.T) {
	origPath := os.Getenv("PATH")
	defer os.Setenv("PATH", origPath)
	// Ensure docker lookup succeeds by using a fake docker on PATH.
	dir := t.TempDir()
	fakeDocker := dir + "/docker"
	if err := os.WriteFile(fakeDocker, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
	os.Setenv("PATH", dir)

	sb, err := newDockerSandbox(Config{Image: "ubuntu:22.04", PersistentID: "abc"})
	if err != nil {
		t.Fatalf("newDockerSandbox: %v", err)
	}
	if sb.containerName == "" {
		t.Fatal("expected containerName to be set when PersistentID is provided")
	}
	if sb.containerName != dockerContainerName("abc") {
		t.Fatalf("containerName mismatch: got %q want %q", sb.containerName, dockerContainerName("abc"))
	}

	sbEphemeral, err := newDockerSandbox(Config{Image: "ubuntu:22.04"})
	if err != nil {
		t.Fatalf("newDockerSandbox (ephemeral): %v", err)
	}
	if sbEphemeral.containerName != "" {
		t.Fatalf("expected empty containerName without PersistentID, got %q", sbEphemeral.containerName)
	}
}

func TestConfigFromEnv_PersistentID(t *testing.T) {
	origType := os.Getenv("SANDBOX_TYPE")
	origPersistentID := os.Getenv("SANDBOX_PERSISTENT_ID")
	defer func() {
		os.Setenv("SANDBOX_TYPE", origType)
		os.Setenv("SANDBOX_PERSISTENT_ID", origPersistentID)
	}()

	os.Setenv("SANDBOX_TYPE", "docker")
	os.Setenv("SANDBOX_PERSISTENT_ID", "my-persistent-sandbox")
	cfg := ConfigFromEnv()
	if cfg.PersistentID != "my-persistent-sandbox" {
		t.Errorf("expected PersistentID to be picked up from env, got %q", cfg.PersistentID)
	}
}

// requireDocker skips the test unless a working Docker daemon is reachable.
func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not reachable")
	}
}

// TestDockerSandbox_PersistentContainerSurvivesAcrossInstances verifies the
// hibernate/resume behavior end to end: a file written by one sandbox
// instance (simulating one process run) is still there when a brand-new
// sandbox instance with the same PersistentID execs into the container
// later (simulating the agent restarting).
func TestDockerSandbox_PersistentContainerSurvivesAcrossInstances(t *testing.T) {
	requireDocker(t)
	persistentID := "covo-sandbox-test-" + t.Name()
	t.Cleanup(func() { _ = RemovePersistentDockerSandbox(persistentID) })

	cfg := Config{Image: "alpine:3", WorkDir: "/", PersistentID: persistentID}

	sb1, err := newDockerSandbox(cfg)
	if err != nil {
		t.Fatalf("newDockerSandbox: %v", err)
	}
	if _, err := sb1.Run(context.Background(), "echo hi > /tmp/marker.txt", 30*time.Second); err != nil {
		t.Fatalf("Run (write marker): %v", err)
	}
	if err := sb1.Close(); err != nil {
		t.Fatalf("Close (hibernate): %v", err)
	}

	// Brand-new sandbox instance, same PersistentID -- must resume the same
	// container (not a fresh one) and see the previously written file.
	sb2, err := newDockerSandbox(cfg)
	if err != nil {
		t.Fatalf("newDockerSandbox (second instance): %v", err)
	}
	defer sb2.Close()

	result, err := sb2.Run(context.Background(), "cat /tmp/marker.txt", 30*time.Second)
	if err != nil {
		t.Fatalf("Run (read marker): %v", err)
	}
	if strings.TrimSpace(result.Stdout) != "hi" {
		t.Fatalf("expected resumed container to still have the marker file, got stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

// helper
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(s) > 0 && containsStr(s, substr)))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
