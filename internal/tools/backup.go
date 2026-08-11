package tools

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const backupVersion = 1

type BackupManifest struct {
	Version    int       `json:"version"`
	CreatedAt  time.Time `json:"created_at"`
	HomeDir    string    `json:"home_dir"`
	Entries    []string  `json:"entries"`
	TotalSize  int64     `json:"total_size"`
}

func VerifyBackup(archivePath string) (*BackupManifest, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("read gzip: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		if hdr.Name == "manifest.json" {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read manifest: %w", err)
			}
			var m BackupManifest
			if err := json.Unmarshal(data, &m); err != nil {
				return nil, fmt.Errorf("parse manifest: %w", err)
			}
			if m.Version != backupVersion {
				return nil, fmt.Errorf("unsupported backup version %d", m.Version)
			}
			return &m, nil
		}
	}
	return nil, fmt.Errorf("no manifest found in archive")
}

func isPathSafe(base, candidate string) bool {
	rel, err := filepath.Rel(base, candidate)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

func CreateBackup(homeDir, outputPath string, extraDirs ...string) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	manifest := BackupManifest{
		Version:   backupVersion,
		CreatedAt: time.Now(),
		HomeDir:   homeDir,
	}

	var lastErr error
	addFile := func(path string) {
		rel, err := filepath.Rel(homeDir, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel = filepath.Base(path)
		}

		info, err := os.Stat(path)
		if err != nil {
			lastErr = fmt.Errorf("stat %s: %w", rel, err)
			return
		}
		if info.IsDir() {
			return
		}
		if info.Size() == 0 {
			return
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			lastErr = fmt.Errorf("header %s: %w", rel, err)
			return
		}
		hdr.Name = rel
		hdr.ModTime = info.ModTime()

		if err := tw.WriteHeader(hdr); err != nil {
			lastErr = fmt.Errorf("write header %s: %w", rel, err)
			return
		}

		data, err := os.ReadFile(path)
		if err != nil {
			lastErr = fmt.Errorf("read %s: %w", rel, err)
			return
		}
		if _, err := tw.Write(data); err != nil {
			lastErr = fmt.Errorf("write data %s: %w", rel, err)
			return
		}
		manifest.Entries = append(manifest.Entries, rel)
		manifest.TotalSize += info.Size()
	}

	walkDir := func(dir string) {
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			addFile(path)
			return nil
		})
	}

	walkDir(homeDir)
	for _, extra := range extraDirs {
		if extra != "" {
			walkDir(extra)
		}
	}

	envPath := filepath.Join(homeDir, ".env")
	if _, err := os.Stat(envPath); err == nil {
		addFile(envPath)
	}

	if lastErr != nil {
		return lastErr
	}

	manifestJSON, _ := json.MarshalIndent(manifest, "", "  ")
	mhdr := &tar.Header{
		Name:     "manifest.json",
		Size:     int64(len(manifestJSON)),
		Mode:     0644,
		ModTime:  time.Now(),
	}
	if err := tw.WriteHeader(mhdr); err != nil {
		return fmt.Errorf("write manifest header: %w", err)
	}
	if _, err := tw.Write(manifestJSON); err != nil {
		return fmt.Errorf("write manifest data: %w", err)
	}

	return nil
}

func RestoreBackup(archivePath, targetDir string) error {
	if _, err := os.Stat(targetDir); os.IsNotExist(err) {
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			return fmt.Errorf("create target dir: %w", err)
		}
	}

	backupDir := targetDir + ".backup-" + time.Now().Format("20060102-150405")
	if err := os.Rename(targetDir, backupDir); err != nil && !os.IsNotExist(err) {
		if linkErr, ok := err.(*os.LinkError); ok {
			_ = linkErr
			backupDir = filepath.Join(os.TempDir(), "covo-backup-"+time.Now().Format("20060102-150405"))
			if err := copyDir(targetDir, backupDir); err != nil {
				return fmt.Errorf("backup existing data (copied): %w", err)
			}
		} else {
			return fmt.Errorf("backup existing data: %w", err)
		}
	}

	f, err := os.Open(archivePath)
	if err != nil {
		os.Rename(backupDir, targetDir)
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		os.Rename(backupDir, targetDir)
		return fmt.Errorf("read gzip: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	manifestValid := false
	var restoreErrors []string

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			restoreErrors = append(restoreErrors, fmt.Sprintf("read tar: %v", err))
			break
		}

		if hdr.Name == "manifest.json" {
			manifestValid = true
			continue
		}

		if strings.Contains(hdr.Name, "..") || strings.HasPrefix(hdr.Name, "/") {
			restoreErrors = append(restoreErrors, fmt.Sprintf("skipped unsafe path: %s", hdr.Name))
			continue
		}

		targetPath := filepath.Join(targetDir, hdr.Name)
		if !isPathSafe(targetDir, targetPath) {
			restoreErrors = append(restoreErrors, fmt.Sprintf("skipped path traversal: %s", hdr.Name))
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			restoreErrors = append(restoreErrors, fmt.Sprintf("mkdir %s: %v", filepath.Dir(hdr.Name), err))
			continue
		}

		data, err := io.ReadAll(tr)
		if err != nil {
			restoreErrors = append(restoreErrors, fmt.Sprintf("read %s: %v", hdr.Name, err))
			continue
		}

		if err := os.WriteFile(targetPath, data, os.FileMode(hdr.Mode)); err != nil {
			restoreErrors = append(restoreErrors, fmt.Sprintf("write %s: %v", hdr.Name, err))
			continue
		}
	}

	if !manifestValid {
		os.RemoveAll(targetDir)
		os.Rename(backupDir, targetDir)
		return fmt.Errorf("invalid archive: missing manifest.json")
	}

	if len(restoreErrors) > 0 {
		return fmt.Errorf("restore completed with %d error(s): %s", len(restoreErrors), restoreErrors[0])
	}

	os.RemoveAll(backupDir)
	return nil
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
