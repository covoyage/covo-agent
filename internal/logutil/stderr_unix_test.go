//go:build unix

package logutil

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRedirectStderrProcess(t *testing.T) {
	if path := os.Getenv("COVO_TEST_NATIVE_STDERR_LOG"); path != "" {
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		restore, err := RedirectStderr(file)
		if err != nil {
			t.Fatal(err)
		}
		defer restore()
		if _, err := unix.Write(2, []byte("MallocStackLogging: native diagnostic\n")); err != nil {
			t.Fatal(err)
		}
		child := exec.Command("/bin/sh", "-c", "printf 'child diagnostic\\n' >&2")
		child.Stderr = os.Stderr
		if err := child.Run(); err != nil {
			t.Fatal(err)
		}
		if err := restore(); err != nil {
			t.Fatal(err)
		}
		os.Stdout.WriteString("stdout preserved\n")
		os.Stderr.WriteString("stderr restored\n")
		return
	}
	path := filepath.Join(t.TempDir(), "native.log")
	command := exec.Command(os.Args[0], "-test.run=^TestRedirectStderrProcess$")
	command.Env = append(os.Environ(), "COVO_TEST_NATIVE_STDERR_LOG="+path)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("stderr helper failed: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	logged, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(logged) != "MallocStackLogging: native diagnostic\nchild diagnostic\n" {
		t.Fatalf("native/child diagnostics missing: %q", logged)
	}
	if stderr.String() != "stderr restored\n" || !bytes.Contains(stdout.Bytes(), []byte("stdout preserved\n")) {
		t.Fatalf("terminal output corrupted: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRedirectDescriptorCapturesNativeOutputAndRestores(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	var logged bytes.Buffer
	restore, err := redirectDescriptor(int(writer.Fd()), &logged)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restore() })
	diagnostic := "covo-agent(123) MallocStackLogging: can't turn off malloc stack logging because it was not enabled.\n"
	if _, err := unix.Write(int(writer.Fd()), []byte(diagnostic)); err != nil {
		t.Fatal(err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if logged.String() != diagnostic {
		t.Fatalf("native stderr not logged: %q", logged.String())
	}
	if err := restore(); err != nil {
		t.Fatal("restore is not idempotent", err)
	}
	if _, err := writer.WriteString("terminal restored"); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	output, err := io.ReadAll(reader)
	if err != nil || string(output) != "terminal restored" {
		t.Fatalf("diagnostic leaked or output not restored: %q err=%v", output, err)
	}
}

func TestRedirectDescriptorDrainsAfterLogFailure(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	logFile, err := os.CreateTemp(t.TempDir(), "closed-log")
	if err != nil {
		t.Fatal(err)
	}
	logFile.Close()
	restore, err := redirectDescriptor(int(writer.Fd()), logFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restore() })
	written := make(chan error, 1)
	go func() {
		_, err := writer.Write(bytes.Repeat([]byte("diagnostic\n"), 32768))
		written <- err
	}()
	select {
	case err := <-written:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stderr blocked after log writer failure")
	}
	if err := restore(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("log failure not reported: %v", err)
	}
}

func TestRedirectDescriptorRetainedWriterDoesNotBlockRestore(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	restore, err := redirectDescriptor(int(writer.Fd()), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restore() })
	retained, err := unix.Dup(int(writer.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(retained)
	started := time.Now()
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("restore waited for retained writer: %v", elapsed)
	}
	if _, err := writer.WriteString("restored"); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	output, err := io.ReadAll(reader)
	if err != nil || string(output) != "restored" {
		t.Fatalf("stderr not restored: %q err=%v", output, err)
	}
}
