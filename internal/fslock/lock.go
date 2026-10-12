package fslock

import (
	"fmt"
	"os"
)

func Acquire(path string) (func(), error) {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("lock path is not a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	entry, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || !entry.Mode().IsRegular() || !os.SameFile(info, entry) {
		file.Close()
		return nil, fmt.Errorf("lock file changed")
	}
	if err := lockFile(file); err != nil {
		file.Close()
		return nil, fmt.Errorf("resource is busy or cannot be locked: %w", err)
	}
	return func() { file.Close() }, nil
}
