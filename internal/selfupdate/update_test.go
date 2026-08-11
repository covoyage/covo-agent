package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSelectReleaseAssetSupportsGoReleaserArchitectureNames(t *testing.T) {
	assets := []releaseAsset{
		{Name: "covo-agent_1.2.3_darwin_x86_64.tar.gz", BrowserDownloadURL: "darwin"},
		{Name: "covo-agent_1.2.3_windows_x86_64.zip", BrowserDownloadURL: "windows"},
	}
	if asset, ok := selectReleaseAsset(assets, "darwin", "amd64"); !ok || asset.BrowserDownloadURL != "darwin" {
		t.Fatalf("darwin amd64 asset = %+v, %v", asset, ok)
	}
	if asset, ok := selectReleaseAsset(assets, "windows", "amd64"); !ok || asset.BrowserDownloadURL != "windows" {
		t.Fatalf("windows amd64 asset = %+v, %v", asset, ok)
	}
}

func TestNormalizeVersion(t *testing.T) {
	if normalizeVersion("v1.2.3") != normalizeVersion("1.2.3") {
		t.Fatal("version tag prefix should not affect comparison")
	}
}

func TestExtractBinaryFromReleaseArchives(t *testing.T) {
	tests := []struct {
		name   string
		create func(string) error
	}{
		{name: "tar.gz", create: createTarGz},
		{name: "zip", create: createZip},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			archive := filepath.Join(dir, "release."+test.name)
			if err := test.create(archive); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(dir, "covo-agent")
			if err := extractBinary(archive, destination); err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(destination)
			if err != nil || string(content) != "binary" {
				t.Fatalf("extracted content = %q, %v", content, err)
			}
		})
	}
}

func TestDownloadArchiveRejectsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "not found", http.StatusNotFound)
	}))
	defer server.Close()
	destination := filepath.Join(t.TempDir(), "release.tar.gz")
	if err := downloadArchive(server.Client(), server.URL, destination, ""); err == nil {
		t.Fatal("expected HTTP status error")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("failed download created archive: %v", err)
	}
}

func TestStageAndInstallExecutable(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "extracted")
	target := filepath.Join(dir, "covo-agent")
	if err := os.WriteFile(source, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	staged, err := stageExecutable(source, target)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(staged) != dir {
		t.Fatalf("staged outside target directory: %s", staged)
	}
	if err := installExecutable(staged, target); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "new" {
		t.Fatalf("installed content = %q, %v", content, err)
	}
}

func TestReplaceWithBackupRollsBackActivationFailure(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, "staged")
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(staged, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	rename := func(oldPath, newPath string) error {
		calls++
		if calls == 2 {
			return errors.New("injected activation failure")
		}
		return os.Rename(oldPath, newPath)
	}
	if err := replaceWithBackup(staged, target, rename); err == nil {
		t.Fatal("expected activation failure")
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "old" {
		t.Fatalf("rollback content = %q, %v", content, err)
	}
}

func createTarGz(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	content := []byte("binary")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "covo-agent", Mode: 0755, Size: int64(len(content))}); err != nil {
		return err
	}
	if _, err := tarWriter.Write(content); err != nil {
		return err
	}
	if err := tarWriter.Close(); err != nil {
		return err
	}
	if err := gzipWriter.Close(); err != nil {
		return err
	}
	return file.Close()
}

func createZip(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("covo-agent")
	if err != nil {
		return err
	}
	if _, err := entry.Write([]byte("binary")); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return file.Close()
}
