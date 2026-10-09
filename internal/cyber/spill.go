package cyber

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Output spill limits for the scan tool.
const (
	MaxStdoutBytes = 20000
	MaxStderrBytes = 4000
)

// SpillDir returns the directory where spilled (truncated-away) tool output
// is stored: <workDir>/.covo/cyber/outputs.
func SpillDir(workDir string) string {
	if strings.TrimSpace(workDir) == "" {
		workDir = "."
	}
	return filepath.Join(workDir, ".covo", "cyber", "outputs")
}

// BoundWithSpill truncates full to maxBytes. When truncation happens, the
// full text is written to a spill file under SpillDir and the returned
// string carries a notice with the path, so the agent can read the evidence
// back with the read tool.
func BoundWithSpill(workDir, label, full string, maxBytes int) string {
	if len(full) <= maxBytes {
		return full
	}
	kept := full[:maxBytes]
	path, err := writeSpill(workDir, label, full)
	if err != nil {
		return kept + fmt.Sprintf("\n...[output truncated: original %d bytes, kept %d; spill failed: %v]", len(full), maxBytes, err)
	}
	return kept + fmt.Sprintf("\n...[output truncated: original %d bytes, kept %d; full output saved to %s — read it with the read tool]", len(full), maxBytes, path)
}

func writeSpill(workDir, label, full string) (string, error) {
	root, err := NewCatalog(workDir).openStorage(true)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := root.Mkdir("outputs", 0o700); err != nil && !os.IsExist(err) {
		return "", err
	}
	info, err := root.Lstat("outputs")
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("cyber outputs must be a directory, not a symbolic link")
	}
	outputs, err := root.OpenRoot("outputs")
	if err != nil {
		return "", err
	}
	defer outputs.Close()
	dir, err := outputs.Open(".")
	if err != nil {
		return "", err
	}
	err = dir.Chmod(0o700)
	dir.Close()
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%s-%s.txt", time.Now().UTC().Format("20060102-150405.000"), sanitizeSpillLabel(label), rand.Text())
	file, err := outputs.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := file.WriteString(full); err != nil {
		file.Close()
		outputs.Remove(name)
		return "", err
	}
	if err := file.Close(); err != nil {
		outputs.Remove(name)
		return "", err
	}
	return filepath.Join(SpillDir(workDir), name), nil
}

func sanitizeSpillLabel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'),
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = out[:40]
	}
	if out == "" {
		out = "output"
	}
	return out
}
