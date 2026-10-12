//go:build !unix

package logutil

import "io"

func RedirectStderr(writer io.Writer) (func() error, error) {
	return func() error { return nil }, nil
}
