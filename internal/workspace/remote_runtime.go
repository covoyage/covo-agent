package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const remoteRuntimeMarker = ".covo-agent-remote"

// RuntimeSession is a deployed agent binary on another host. File and bash
// tools still go through RemoteFS/RemoteBash; the process itself runs there.
type RuntimeSession struct {
	Spec       *RemoteSpec
	RemoteBin  string
	RemoteHome string
	cmd        *exec.Cmd
}

func (s *RemoteSpec) posixJoin(parts ...string) string {
	return path.Join(parts...)
}

func (s *RemoteSpec) remoteHome() string {
	if s == nil {
		return ""
	}
	if s.WorkDir != "" && s.WorkDir != "." && s.WorkDir != "~" {
		return s.posixJoin(strings.TrimRight(s.WorkDir, "/"), remoteRuntimeMarker)
	}
	return s.posixJoin("$HOME", ".covo-agent", "remote-runtime")
}

// Probe reports whether the remote host can run a posix shell.
func (s *RemoteSpec) Probe(ctx context.Context) error {
	stdout, stderr, code, err := s.runRemote(ctx, "uname -s && pwd", nil, 20*time.Second)
	if err != nil {
		return fmt.Errorf("remote probe: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("remote probe failed: %s", strings.TrimSpace(stderr+" "+stdout))
	}
	return nil
}

func (s *RemoteSpec) ensureRemoteDir(ctx context.Context, dir string) error {
	_, stderr, code, err := s.runRemote(ctx, "mkdir -p -- "+shellQuote(dir), nil, 20*time.Second)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("remote mkdir %s: %s", dir, strings.TrimSpace(stderr))
	}
	return nil
}

func (s *RemoteSpec) uploadFile(ctx context.Context, localPath, remotePath string) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	dir := path.Dir(remotePath)
	script := fmt.Sprintf("mkdir -p %s && cat > %s && chmod 755 %s", shellQuote(dir), shellQuote(remotePath), shellQuote(remotePath))
	_, stderr, code, err := s.runRemote(ctx, script, data, 2*time.Minute)
	if err != nil {
		return fmt.Errorf("upload %s: %w (%s)", remotePath, err, strings.TrimSpace(stderr))
	}
	if code != 0 {
		return fmt.Errorf("upload %s: %s", remotePath, strings.TrimSpace(stderr))
	}
	return nil
}

func (s *RemoteSpec) handshake(ctx context.Context, remoteBin string) error {
	cmd := remoteBin + " --version"
	stdout, stderr, code, err := s.runRemote(ctx, cmd, nil, 30*time.Second)
	if err != nil {
		return fmt.Errorf("remote handshake: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("remote handshake failed: %s", strings.TrimSpace(stderr+" "+stdout))
	}
	if !strings.Contains(strings.ToLower(stdout+stderr), "covo") && strings.TrimSpace(stdout) == "" {
		return fmt.Errorf("remote handshake: empty version output")
	}
	return nil
}

func (s *RemoteSpec) startRemoteProcess(ctx context.Context, remoteBin, workDir, extraArgs string) (*exec.Cmd, error) {
	bin, args, err := s.transport()
	if err != nil {
		return nil, err
	}
	script := "cd " + shellQuote(workDir) + " && exec " + shellQuote(remoteBin)
	if extraArgs != "" {
		script += " " + extraArgs
	}
	cmd := exec.CommandContext(ctx, bin, append(args, script)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// DeployRuntime copies this process onto the remote host, verifies it starts,
// and returns a session handle. extraArgs are appended to the remote binary
// (for example `acp` to serve the editor protocol there).
func (s *RemoteSpec) DeployRuntime(ctx context.Context, extraArgs string) (*RuntimeSession, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("remote not enabled")
	}
	if err := s.Probe(ctx); err != nil {
		return nil, err
	}
	localBin, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("local executable: %w", err)
	}
	home := s.remoteHome()
	if strings.Contains(home, "$HOME") {
		stdout, _, code, err := s.runRemote(ctx, "printf '%s' \"$HOME/.covo-agent/remote-runtime\"", nil, 15*time.Second)
		if err == nil && code == 0 && strings.TrimSpace(stdout) != "" {
			home = strings.TrimSpace(stdout)
		} else {
			home = "~/.covo-agent/remote-runtime"
		}
	}
	if err := s.ensureRemoteDir(ctx, home); err != nil {
		return nil, err
	}
	remoteBin := s.posixJoin(home, filepath.Base(localBin))
	if err := s.uploadFile(ctx, localBin, remoteBin); err != nil {
		return nil, err
	}
	if err := s.handshake(ctx, remoteBin); err != nil {
		return nil, err
	}
	workDir := s.WorkDir
	if workDir == "" {
		workDir = "."
	}
	cmd, err := s.startRemoteProcess(ctx, remoteBin, workDir, extraArgs)
	if err != nil {
		return nil, err
	}
	return &RuntimeSession{
		Spec:       s,
		RemoteBin:  remoteBin,
		RemoteHome: home,
		cmd:        cmd,
	}, nil
}

func (r *RuntimeSession) Wait() error {
	if r == nil || r.cmd == nil {
		return nil
	}
	return r.cmd.Wait()
}

func (r *RuntimeSession) Signal() error {
	if r == nil || r.cmd == nil || r.cmd.Process == nil {
		return nil
	}
	return r.cmd.Process.Signal(os.Interrupt)
}

// RemoteRuntimeArgs is the posix command used to exec the deployed binary
// without starting it (tests / dry-run).
func RemoteRuntimeArgs(bin, workDir, extra string) string {
	var b bytes.Buffer
	b.WriteString("cd ")
	b.WriteString(shellQuote(workDir))
	b.WriteString(" && exec ")
	b.WriteString(shellQuote(bin))
	if extra != "" {
		b.WriteByte(' ')
		b.WriteString(extra)
	}
	return b.String()
}
