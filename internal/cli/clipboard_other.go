//go:build !darwin

package cli

import "fmt"

func ClipboardImagePaste() (string, error) {
	return "", fmt.Errorf("image paste not supported on this platform")
}

func HasClipboardImage() bool {
	return false
}
