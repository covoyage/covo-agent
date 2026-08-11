package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type dockerSandbox struct {
	cfg     Config
	image   string
	workDir string

	// containerName is set (and non-empty) only when cfg.PersistentID is
	// configured, switching this sandbox from one-shot ephemeral containers
	// to a single long-lived, named container reused across calls.
	containerName string
	ensured       bool
}

func newDockerSandbox(cfg Config) (*dockerSandbox, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, fmt.Errorf("docker not found on PATH")
	}
	if cfg.Image == "" {
		cfg.Image = "ubuntu:22.04"
	}
	s := &dockerSandbox{
		cfg:     cfg,
		image:   cfg.Image,
		workDir: cfg.WorkDir,
	}
	if cfg.PersistentID != "" {
		s.containerName = dockerContainerName(cfg.PersistentID)
	}
	return s, nil
}

// dockerContainerName derives a stable, Docker-legal container name from an
// arbitrary persistent ID (hashed, so callers can pass anything -- a task
// ID, a working directory path, etc.). The same ID always maps to the same
// name, so the container can be found and resumed across process restarts,
// not just across calls within one process.
func dockerContainerName(persistentID string) string {
	sum := sha256.Sum256([]byte(persistentID))
	return "covo-sandbox-" + hex.EncodeToString(sum[:])[:16]
}

func (s *dockerSandbox) Type() Type { return TypeDocker }

func (s *dockerSandbox) Run(ctx context.Context, command string, timeout time.Duration) (*Result, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if s.containerName != "" {
		return s.runPersistent(ctx, command)
	}
	return s.runEphemeral(ctx, command)
}

// runEphemeral is the original behavior: a brand-new container per call,
// removed as soon as the command finishes. Used whenever PersistentID is
// not configured (the default -- fully backward compatible).
func (s *dockerSandbox) runEphemeral(ctx context.Context, command string) (*Result, error) {
	execArgs := []string{
		"run", "--rm",
		"-w", s.workDir,
		"--network", "none",
		"--memory", "512m",
		"--cpus", "1",
		"--security-opt", "no-new-privileges",
	}

	for k, v := range s.cfg.Env {
		execArgs = append(execArgs, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	execArgs = append(execArgs, s.image, "sh", "-c", command)
	return s.runDocker(ctx, execArgs)
}

// runPersistent ensures a long-lived, named container exists and is
// running -- creating it on first use, or resuming ("waking") it via
// `docker start` if it already exists but is stopped, including from a
// brand-new process (the container is looked up by its stable name, not
// held in memory) -- then execs the command inside it. This gives the
// sandbox a warm filesystem and installed packages that survive across
// calls and across process restarts, in the same spirit as Modal/Daytona's
// hibernate-and-resume sandboxes, without requiring any cloud SDK.
func (s *dockerSandbox) runPersistent(ctx context.Context, command string) (*Result, error) {
	if !s.ensured {
		if err := s.ensureContainer(ctx); err != nil {
			return nil, err
		}
		s.ensured = true
	}

	execArgs := []string{"exec", "-w", s.workDir}
	for k, v := range s.cfg.Env {
		execArgs = append(execArgs, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	execArgs = append(execArgs, s.containerName, "sh", "-c", command)
	return s.runDocker(ctx, execArgs)
}

// ensureContainer creates the named container if it doesn't exist yet, or
// starts it (resuming from hibernation) if it exists but isn't running.
// Safe to call when the container is already running.
func (s *dockerSandbox) ensureContainer(ctx context.Context) error {
	inspect := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", s.containerName)
	out, err := inspect.CombinedOutput()
	exists := err == nil
	running := exists && strings.TrimSpace(string(out)) == "true"

	if exists && running {
		return nil
	}
	if exists {
		// Resume: filesystem, installed packages, prior state all preserved.
		return exec.CommandContext(ctx, "docker", "start", s.containerName).Run()
	}

	createArgs := []string{
		"create", "--name", s.containerName,
		"-w", s.workDir,
		"--network", "none",
		"--memory", "512m",
		"--cpus", "1",
		"--security-opt", "no-new-privileges",
	}
	for k, v := range s.cfg.Env {
		createArgs = append(createArgs, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	// Keep the container alive between exec calls -- without a long-running
	// foreground process it would exit immediately after `docker start`.
	createArgs = append(createArgs, s.image, "tail", "-f", "/dev/null")

	if out, err := exec.CommandContext(ctx, "docker", createArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("docker create: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return exec.CommandContext(ctx, "docker", "start", s.containerName).Run()
}

func (s *dockerSandbox) runDocker(ctx context.Context, execArgs []string) (*Result, error) {
	cmd := exec.CommandContext(ctx, "docker", execArgs...)
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
			return nil, fmt.Errorf("docker: %w", err)
		}
	}

	return &Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Duration: duration,
	}, nil
}

// Close stops (hibernates) a persistent container without removing it, so
// the next sandbox created with the same PersistentID can resume it via
// `docker start` instead of recreating it from scratch. Ephemeral
// (non-persistent) sandboxes have nothing to clean up here -- each
// `docker run --rm` container already removes itself on exit.
func (s *dockerSandbox) Close() error {
	if s.containerName == "" || !s.ensured {
		return nil
	}
	return exec.Command("docker", "stop", s.containerName).Run()
}

// RemovePersistentDockerSandbox permanently deletes the named container
// backing a persistent sandbox created with the given PersistentID
// (stop + rm), for explicit teardown rather than the hibernate-by-default
// behavior of Close. No-op (returns nil) if the container doesn't exist.
func RemovePersistentDockerSandbox(persistentID string) error {
	name := dockerContainerName(persistentID)
	_ = exec.Command("docker", "stop", name).Run()
	out, err := exec.Command("docker", "rm", "-f", name).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "No such container") {
		return fmt.Errorf("docker rm: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

