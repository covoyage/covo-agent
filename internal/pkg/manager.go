package pkg

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Package describes a single installable package.
type Package struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Version     string          `json:"version"`
	Author      string          `json:"author,omitempty"`
	License     string          `json:"license,omitempty"`
	Contents    PackageContents `json:"contents"`
	Source      *PackageSource  `json:"source,omitempty"`
}

// PackageSource describes where the package came from.
type PackageSource struct {
	Type string `json:"type"` // "local" or "git"
	URL  string `json:"url"`
}

// PackageContents lists what the package installs.
type PackageContents struct {
	Extensions []string `json:"extensions,omitempty"`
	Skills     []string `json:"skills,omitempty"`
	Templates  []string `json:"templates,omitempty"`
}

// LockFile tracks installed packages.
type LockFile struct {
	Packages map[string]LockEntry `json:"packages"`
}

// LockEntry records a single installed package in the lock file.
type LockEntry struct {
	Version     string          `json:"version"`
	Source      *PackageSource  `json:"source,omitempty"`
	Contents    PackageContents `json:"contents"`
}

func homeDir() string {
	hd, _ := os.UserHomeDir()
	return hd
}

func covoDir() string {
	return filepath.Join(homeDir(), ".covo-agent")
}

func lockFilePath() string {
	return filepath.Join(covoDir(), "installed.json")
}

// ReadManifest parses a package.json from the given directory.
func ReadManifest(dir string) (*Package, error) {
	path := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read package.json: %w", err)
	}
	var pkg Package
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("parse package.json: %w", err)
	}
	if pkg.Name == "" {
		return nil, fmt.Errorf("package.json missing 'name'")
	}
	return &pkg, nil
}

func readLockFile() (*LockFile, error) {
	data, err := os.ReadFile(lockFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return &LockFile{Packages: map[string]LockEntry{}}, nil
		}
		return nil, fmt.Errorf("read lock file: %w", err)
	}
	var lf LockFile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("parse lock file: %w", err)
	}
	if lf.Packages == nil {
		lf.Packages = map[string]LockEntry{}
	}
	return &lf, nil
}

func writeLockFile(lf *LockFile) error {
	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal lock file: %w", err)
	}
	if err := os.MkdirAll(covoDir(), 0755); err != nil {
		return fmt.Errorf("ensure covo dir: %w", err)
	}
	return os.WriteFile(lockFilePath(), data, 0644)
}

// installContents copies files from srcDir into the appropriate locations
// under ~/.covo-agent/, based on the package contents.
func installContents(pkg *Package, srcDir string) error {
	covo := covoDir()

	for _, name := range pkg.Contents.Extensions {
		src := filepath.Join(srcDir, "extensions", name)
		dst := filepath.Join(covo, "extensions", name)
		if err := copyDir(src, dst); err != nil {
			return fmt.Errorf("install extension %q: %w", name, err)
		}
	}

	for _, name := range pkg.Contents.Skills {
		src := filepath.Join(srcDir, "skills", name)
		dst := filepath.Join(covo, "skills", name)
		if err := copyDir(src, dst); err != nil {
			return fmt.Errorf("install skill %q: %w", name, err)
		}
	}

	for _, name := range pkg.Contents.Templates {
		src := filepath.Join(srcDir, "templates", name+".md")
		dst := filepath.Join(covo, "templates", name+".md")
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return fmt.Errorf("ensure templates dir: %w", err)
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read template %q: %w", name, err)
		}
		if err := os.WriteFile(dst, data, 0644); err != nil {
			return fmt.Errorf("write template %q: %w", name, err)
		}
	}

	return nil
}

// removeContents deletes files previously installed by the package.
func removeContents(entry LockEntry) error {
	covo := covoDir()

	for _, name := range entry.Contents.Extensions {
		path := filepath.Join(covo, "extensions", name)
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove extension %q: %w", name, err)
		}
	}

	for _, name := range entry.Contents.Skills {
		path := filepath.Join(covo, "skills", name)
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove skill %q: %w", name, err)
		}
	}

	for _, name := range entry.Contents.Templates {
		path := filepath.Join(covo, "templates", name+".md")
		if err := os.Remove(path); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("remove template %q: %w", name, err)
			}
		}
	}

	return nil
}

// InstallLocal installs a package from a local directory.
func InstallLocal(dir string) (*Package, error) {
	pkg, err := ReadManifest(dir)
	if err != nil {
		return nil, err
	}

	lf, err := readLockFile()
	if err != nil {
		return nil, err
	}

	if _, exists := lf.Packages[pkg.Name]; exists {
		return nil, fmt.Errorf("package %q already installed", pkg.Name)
	}

	if err := installContents(pkg, dir); err != nil {
		return nil, err
	}

	lf.Packages[pkg.Name] = LockEntry{
		Version:  pkg.Version,
		Source:   pkg.Source,
		Contents: pkg.Contents,
	}
	if err := writeLockFile(lf); err != nil {
		return nil, err
	}

	return pkg, nil
}

// IsGitURL reports whether the argument looks like a git remote URL.
func IsGitURL(s string) bool {
	return strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "git@") ||
		strings.HasPrefix(s, "ssh://")
}

// InstallGit clones a git repository and installs the package from it.
func InstallGit(url string) (*Package, error) {
	tmpDir, err := os.MkdirTemp("", "covo-pkg-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.Command("git", "clone", "--depth", "1", url, tmpDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git clone: %s", strings.TrimSpace(string(out)))
	}

	return InstallLocal(tmpDir)
}

// ListInstalled returns all installed packages from the lock file.
func ListInstalled() (map[string]LockEntry, error) {
	lf, err := readLockFile()
	if err != nil {
		return nil, err
	}
	return lf.Packages, nil
}

// Remove uninstalls a package by name.
func Remove(name string) error {
	lf, err := readLockFile()
	if err != nil {
		return err
	}

	entry, exists := lf.Packages[name]
	if !exists {
		return fmt.Errorf("package %q not found", name)
	}

	if err := removeContents(entry); err != nil {
		return err
	}

	delete(lf.Packages, name)
	return writeLockFile(lf)
}

// copyDir recursively copies a directory tree.
func copyDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}

	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			data, err := os.ReadFile(srcPath)
			if err != nil {
				return err
			}
			if err := os.WriteFile(dstPath, data, 0644); err != nil {
				return err
			}
		}
	}

	return nil
}
