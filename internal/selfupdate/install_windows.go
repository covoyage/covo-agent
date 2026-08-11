//go:build windows

package selfupdate

import "os"

func installExecutable(staged, target string) error {
	return replaceWithBackup(staged, target, os.Rename)
}
