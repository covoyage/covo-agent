package agent

import (
	"os"
	"sync/atomic"
)

// ACPFileSystem is the editor-backed filesystem an ACP client provides so the
// read/write tools can see unsaved buffers. Structurally matches covonaut's
// acp.ACPFileSystem (kept local to avoid importing the acp package here).
type ACPFileSystem interface {
	ReadTextFile(path string) ([]byte, error)
	WriteTextFile(path string, content []byte) error
}

// fileBackend is the set of filesystem operations the read/write tools need.
type fileBackend interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, content []byte) error
	Stat(path string) (os.FileInfo, error)
	MkdirAll(path string, perm os.FileMode) error
}

type localFileBackend struct{}

func (localFileBackend) ReadFile(p string) ([]byte, error)         { return os.ReadFile(p) }
func (localFileBackend) WriteFile(p string, c []byte) error        { return os.WriteFile(p, c, 0644) }
func (localFileBackend) Stat(p string) (os.FileInfo, error)        { return os.Stat(p) }
func (localFileBackend) MkdirAll(p string, perm os.FileMode) error { return os.MkdirAll(p, perm) }

// swappableFS implements covonaut tools.ReadOperations and WriteFileOperations,
// delegating to a backend that can be swapped at runtime (e.g. to an ACP/editor
// backend). Default is the local filesystem.
type swappableFS struct {
	backend atomic.Pointer[fileBackend]
}

func newSwappableFS() *swappableFS {
	s := &swappableFS{}
	var b fileBackend = localFileBackend{}
	s.backend.Store(&b)
	return s
}

func (s *swappableFS) cur() fileBackend { return *s.backend.Load() }

func (s *swappableFS) set(b fileBackend) { s.backend.Store(&b) }

// ReadOperations
func (s *swappableFS) ReadFile(p string) ([]byte, error)  { return s.cur().ReadFile(p) }
func (s *swappableFS) Stat(p string) (os.FileInfo, error) { return s.cur().Stat(p) }

// WriteFileOperations (ReadFile/Stat shared above)
func (s *swappableFS) WriteFile(p string, c []byte) error        { return s.cur().WriteFile(p, c) }
func (s *swappableFS) MkdirAll(p string, perm os.FileMode) error { return s.cur().MkdirAll(p, perm) }

// acpFileBackend routes text reads/writes through the ACP client (editor),
// falling back to the local filesystem for stat/mkdir and on error (e.g. binary
// files the editor cannot return as text).
type acpFileBackend struct {
	fs    ACPFileSystem
	local fileBackend
}

func (a *acpFileBackend) ReadFile(p string) ([]byte, error) {
	if b, err := a.fs.ReadTextFile(p); err == nil {
		return b, nil
	}
	return a.local.ReadFile(p)
}

func (a *acpFileBackend) WriteFile(p string, c []byte) error {
	if err := a.fs.WriteTextFile(p, c); err == nil {
		return nil
	}
	return a.local.WriteFile(p, c)
}

func (a *acpFileBackend) Stat(p string) (os.FileInfo, error)        { return a.local.Stat(p) }
func (a *acpFileBackend) MkdirAll(p string, perm os.FileMode) error { return a.local.MkdirAll(p, perm) }

// SetFileBackend routes the agent's read/write tools through the given editor
// filesystem (ACP). Pass nil to revert to the local filesystem.
func (ca *CovoAgent) SetFileBackend(fs ACPFileSystem) {
	if ca.fsOps == nil {
		return
	}
	if fs == nil {
		ca.fsOps.set(localFileBackend{})
		return
	}
	ca.fsOps.set(&acpFileBackend{fs: fs, local: localFileBackend{}})
}
