//go:build !darwin && !linux && !windows && !freebsd && !openbsd && !netbsd && !dragonfly

package keychain

import "errors"

// No-op keychain on non-Darwin platforms – all operations fall back to file.

func keyringGet(account string) (string, error) {
	return "", errors.New("keychain not available on this platform")
}

func keyringSet(account, password string) error {
	return errors.New("keychain not available on this platform")
}

func keyringDelete(account string) error {
	return nil
}
