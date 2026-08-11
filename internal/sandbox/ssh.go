package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type sshSandbox struct {
	cfg     Config
	host    string
	user    string
	keyPath string
	workDir string
}

func newSSHSandbox(cfg Config) (*sshSandbox, error) {
	if _, err := exec.LookPath("ssh"); err != nil {
		return nil, fmt.Errorf("ssh not found on PATH")
	}
	if cfg.SSHHost == "" {
		return nil, fmt.Errorf("SSH_HOST not set")
	}
	return &sshSandbox{
		cfg:     cfg,
		host:    cfg.SSHHost,
		user:    cfg.SSHUser,
		keyPath: cfg.SSHKey,
		workDir: cfg.WorkDir,
	}, nil
}

func (s *sshSandbox) Type() Type { return TypeSSH }

func (s *sshSandbox) Run(ctx context.Context, command string, timeout time.Duration) (*Result, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	target := s.host
	if s.user != "" {
		target = s.user + "@" + s.host
	}

	sshArgs := []string{
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10",
		"-o", "BatchMode=yes",
	}

	if s.keyPath != "" {
		sshArgs = append(sshArgs, "-i", s.keyPath)
	}

	sshArgs = append(sshArgs, target)

	if s.workDir != "" {
		command = fmt.Sprintf("cd %s && %s", shellQuote(s.workDir), command)
	}
	sshArgs = append(sshArgs, command)

	cmd := exec.CommandContext(ctx, "ssh", sshArgs...)
	cmd.Env = os.Environ()

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
			return nil, fmt.Errorf("ssh run: %w", err)
		}
	}

	return &Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Duration: duration,
	}, nil
}

func (s *sshSandbox) Close() error {
	return nil
}

// shellQuote wraps s in single quotes for safe embedding in a POSIX shell
// command line, escaping any embedded single quotes. This prevents workDir
// values containing shell metacharacters from altering or injecting
// additional commands into the remote command line.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
