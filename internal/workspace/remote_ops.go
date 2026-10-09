package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (s *RemoteSpec) transport() (bin string, args []string, err error) {
	if s == nil || s.Host == "" {
		return "", nil, fmt.Errorf("empty remote")
	}
	switch s.Kind {
	case "ssh", "":
		args := []string{
			"-o", "StrictHostKeyChecking=accept-new",
			"-o", "ConnectTimeout=10",
			"-o", "BatchMode=yes",
		}
		if s.Key != "" {
			args = append(args, "-i", s.Key)
		}
		return "ssh", append(args, s.SSHTarget()), nil
	case "wsl":
		return "wsl", []string{"-d", s.Host, "--", "sh", "-c"}, nil
	case "docker":
		return "docker", []string{"exec", "-i", s.Host, "sh", "-c"}, nil
	default:
		return "", nil, fmt.Errorf("unsupported remote kind %q", s.Kind)
	}
}

func (s *RemoteSpec) runRemote(ctx context.Context, remoteCmd string, stdin []byte, timeout time.Duration) (stdout, stderr string, exitCode int, err error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	bin, args, err := s.transport()
	if err != nil {
		return "", "", -1, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append(args, remoteCmd)...)
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode(), nil
		}
		return outBuf.String(), errBuf.String(), -1, runErr
	}
	return outBuf.String(), errBuf.String(), 0, nil
}

// RemoteFS implements the file operations used by local tool backends.
type RemoteFS struct {
	Spec *RemoteSpec
}

func (f *RemoteFS) resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if f.Spec.WorkDir == "" || f.Spec.WorkDir == "." {
		return path
	}
	return strings.TrimRight(f.Spec.WorkDir, "/") + "/" + strings.TrimLeft(path, "/")
}

func (f *RemoteFS) ReadFile(path string) ([]byte, error) {
	stdout, stderr, code, err := f.Spec.runRemote(context.Background(), "cat -- "+shellQuote(f.resolve(path)), nil, 60*time.Second)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("remote read %s: %s", path, strings.TrimSpace(stderr))
	}
	return []byte(stdout), nil
}

func (f *RemoteFS) WriteFile(path string, content []byte) error {
	resolved := f.resolve(path)
	dir := filepath.Dir(resolved)
	script := fmt.Sprintf("mkdir -p %s && cat > %s", shellQuote(dir), shellQuote(resolved))
	_, stderr, code, err := f.Spec.runRemote(context.Background(), script, content, 60*time.Second)
	if err != nil {
		return fmt.Errorf("remote write %s: %w (%s)", path, err, strings.TrimSpace(stderr))
	}
	if code != 0 {
		return fmt.Errorf("remote write %s: %s", path, strings.TrimSpace(stderr))
	}
	return nil
}

func (f *RemoteFS) Stat(path string) (os.FileInfo, error) {
	stdout, stderr, code, err := f.Spec.runRemote(context.Background(),
		fmt.Sprintf("if [ -e %s ]; then stat -c '%%s %%Y %%F' -- %s; else echo MISSING; fi",
			shellQuote(f.resolve(path)), shellQuote(f.resolve(path))), nil, 15*time.Second)
	if err != nil {
		return nil, err
	}
	if code != 0 || strings.TrimSpace(stdout) == "MISSING" {
		return nil, fmt.Errorf("remote stat %s: %s", path, strings.TrimSpace(stderr+" "+stdout))
	}
	return remoteFileInfo{name: filepath.Base(path), raw: strings.TrimSpace(stdout)}, nil
}

func (f *RemoteFS) MkdirAll(path string, _ os.FileMode) error {
	_, stderr, code, err := f.Spec.runRemote(context.Background(), "mkdir -p -- "+shellQuote(f.resolve(path)), nil, 15*time.Second)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("remote mkdir %s: %s", path, strings.TrimSpace(stderr))
	}
	return nil
}

func (f *RemoteFS) Access(path string) error {
	_, err := f.Stat(path)
	return err
}

type remoteFileInfo struct {
	name string
	raw  string
}

func (i remoteFileInfo) Name() string       { return i.name }
func (i remoteFileInfo) Size() int64        { return 0 }
func (i remoteFileInfo) Mode() os.FileMode  { return 0o644 }
func (i remoteFileInfo) ModTime() time.Time { return time.Time{} }
func (i remoteFileInfo) IsDir() bool        { return strings.Contains(i.raw, "directory") }
func (i remoteFileInfo) Sys() any           { return nil }

// RemoteBash runs a shell command on the remote host.
type RemoteBash struct {
	Spec *RemoteSpec
}

func (b *RemoteBash) Exec(command string, cwd string, env map[string]string, timeoutSecs *int, onData func(data []byte)) (int, error) {
	dir := cwd
	if dir == "" {
		dir = b.Spec.WorkDir
	}
	var prefix strings.Builder
	if dir != "" {
		prefix.WriteString("cd ")
		prefix.WriteString(shellQuote(dir))
		prefix.WriteString(" && ")
	}
	for k, v := range env {
		prefix.WriteString(k)
		prefix.WriteString("=")
		prefix.WriteString(shellQuote(v))
		prefix.WriteString(" ")
	}
	timeout := 30 * time.Second
	if timeoutSecs != nil && *timeoutSecs > 0 {
		timeout = time.Duration(*timeoutSecs) * time.Second
	}
	stdout, stderr, code, err := b.Spec.runRemote(context.Background(), prefix.String()+command, nil, timeout)
	if onData != nil {
		if stdout != "" {
			onData([]byte(stdout))
		}
		if stderr != "" {
			onData([]byte(stderr))
		}
	}
	return code, err
}
