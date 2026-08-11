package sandbox

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

type Type string

const (
	TypeLocal  Type = "local"
	TypeDocker Type = "docker"
	TypeSSH    Type = "ssh"
)

type Config struct {
	Type    Type
	Image   string
	SSHHost string
	SSHUser string
	SSHKey  string
	WorkDir string
	Env     map[string]string

	// PersistentID, when set on a Docker sandbox, switches it from
	// ephemeral per-call containers to a single long-lived container that
	// is created once and then resumed (docker start) on every later Run
	// call -- including calls from a brand-new process, since the
	// container is looked up by a name derived from this ID rather than
	// held in memory. This preserves the sandbox's filesystem and any
	// installed packages/state across calls, similar in spirit to
	// Modal/Daytona's hibernate-and-resume cloud sandboxes. Leave empty
	// for the original ephemeral behavior (the default). Ignored by the
	// local and SSH backends.
	PersistentID string
}

type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

type Sandbox interface {
	Run(ctx context.Context, command string, timeout time.Duration) (*Result, error)
	Type() Type
	Close() error
}

func New(cfg Config) (Sandbox, error) {
	switch cfg.Type {
	case TypeDocker:
		return newDockerSandbox(cfg)
	case TypeSSH:
		return newSSHSandbox(cfg)
	case TypeLocal:
		return newLocalSandbox(cfg)
	default:
		return newLocalSandbox(cfg)
	}
}

func DetectType() Type {
	if os.Getenv("SANDBOX_TYPE") != "" {
		return Type(strings.ToLower(os.Getenv("SANDBOX_TYPE")))
	}
	if os.Getenv("SSH_HOST") != "" {
		return TypeSSH
	}
	if os.Getenv("DOCKER_IMAGE") != "" || os.Getenv("SANDBOX_IMAGE") != "" {
		return TypeDocker
	}
	return TypeLocal
}

func ConfigFromEnv() Config {
	cfg := Config{
		Type: DetectType(),
		Env:  make(map[string]string),
	}

	switch cfg.Type {
	case TypeDocker:
		cfg.Image = firstEnv("DOCKER_IMAGE", "SANDBOX_IMAGE", "SANDBOX_DOCKER_IMAGE")
		if cfg.Image == "" {
			cfg.Image = "ubuntu:22.04"
		}
		cfg.PersistentID = os.Getenv("SANDBOX_PERSISTENT_ID")
	case TypeSSH:
		cfg.SSHHost = os.Getenv("SSH_HOST")
		cfg.SSHUser = firstEnv("SSH_USER", "SANDBOX_SSH_USER")
		if cfg.SSHUser == "" {
			cfg.SSHUser = "root"
		}
		cfg.SSHKey = firstEnv("SSH_KEY", "SANDBOX_SSH_KEY")
	}

	cfg.WorkDir = firstEnv("SANDBOX_WORKDIR", "SANDBOX_WORK_DIR")
	if cfg.WorkDir == "" {
		cfg.WorkDir = "/workspace"
	}

	return cfg
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func resolveShell(cmd string) string {
	shell := "/bin/sh"
	if s, ok := os.LookupEnv("SHELL"); ok {
		shell = s
	}
	return fmt.Sprintf("%s -c '%s'", shell, escapeShell(cmd))
}

func escapeShell(s string) string {
	s = strings.ReplaceAll(s, "'", "'\\''")
	return s
}
