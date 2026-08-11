//go:build !windows

package selfupdate

import (
	"os"
	"path/filepath"
)

func installExecutable(staged, target string) error {
	if err := os.Rename(staged, target); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
