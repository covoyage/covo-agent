package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/sandbox/ossandbox"
)

type localSandbox struct {
	cfg     Config
	workDir string
	envPolicy EnvPolicy
}

func newLocalSandbox(cfg Config) (*localSandbox, error) {
	wd := cfg.WorkDir
	if wd == "" {
		wd, _ = os.Getwd()
	}
	return &localSandbox{
		cfg:       cfg,
		workDir:   wd,
		envPolicy: EnvPolicyFromEnv(),
	}, nil
}

func (s *localSandbox) Type() Type { return TypeLocal }

func (s *localSandbox) Run(ctx context.Context, command string, timeout time.Duration) (*Result, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	shell := "/bin/sh"
	if sh, ok := os.LookupEnv("SHELL"); ok {
		shell = sh
	}

	cmd := exec.CommandContext(ctx, shell, "-c", command)
	cmd.Dir = s.workDir
	cmd.Env = s.envPolicy.FilterEnv(os.Environ())

	// Merge any explicit Env from Config (these take precedence over policy).
	if len(s.cfg.Env) > 0 {
		envMap := make(map[string]bool)
		for _, e := range cmd.Env {
			name := e
			if idx := strings.IndexByte(e, '='); idx >= 0 {
				name = e[:idx]
			}
			envMap[name] = true
		}
		for k, v := range s.cfg.Env {
			if !envMap[k] {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
		}
	}

	// Apply child process network restriction if the OS-level sandbox requires it.
	if ossandbox.ShouldRestrictChildNetwork() {
		if err := ossandbox.ApplyChildNetworkRestriction(cmd); err != nil {
			return nil, fmt.Errorf("apply child network restriction: %w", err)
		}
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, err
		}
	}

	return &Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Duration: duration,
	}, nil
}

func (s *localSandbox) Close() error {
	return nil
}
