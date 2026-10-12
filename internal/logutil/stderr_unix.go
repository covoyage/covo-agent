//go:build unix

package logutil

import (
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

func RedirectStderr(writer io.Writer) (func() error, error) {
	return redirectDescriptor(2, writer)
}

func redirectDescriptor(descriptor int, writer io.Writer) (func() error, error) {
	if writer == nil {
		writer = io.Discard
	}
	saved, err := unix.FcntlInt(uintptr(descriptor), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	reader, pipeWriter, err := os.Pipe()
	if err != nil {
		unix.Close(saved)
		return nil, err
	}
	if err := unix.Dup2(int(pipeWriter.Fd()), descriptor); err != nil {
		reader.Close()
		pipeWriter.Close()
		unix.Close(saved)
		return nil, err
	}
	pipeWriter.Close()
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(writer, reader)
		if err != nil {
			_, _ = io.Copy(io.Discard, reader)
		}
		reader.Close()
		done <- err
	}()
	var once sync.Once
	var restoreErr error
	restore := func() error {
		once.Do(func() {
			restoreErr = unix.Dup2(saved, descriptor)
			unix.Close(saved)
			timer := time.NewTimer(time.Second)
			defer timer.Stop()
			select {
			case err := <-done:
				restoreErr = errors.Join(restoreErr, err)
			case <-timer.C:
				reader.Close()
			}
		})
		return restoreErr
	}
	return restore, nil
}
