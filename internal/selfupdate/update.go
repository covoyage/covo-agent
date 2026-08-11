package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	owner = "covoyage"
	repo  = "covo-agent"
)

type githubRelease struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	// Digest is populated by the GitHub API as "sha256:<hex>" for assets
	// uploaded with content-addressable digests. Used to verify integrity.
	Digest string `json:"digest"`
}

// checkForUpdates checks if a newer version of covo-agent is available.
// Returns the latest version, a download URL, and the expected SHA-256
// digest (may be empty if GitHub did not report one for this asset) if an
// update is available.
func CheckForUpdates(currentVersion string) (latestVersion, downloadURL, expectedDigest string, err error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)

	client := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "covo-agent")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("check update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("GitHub API: HTTP %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", "", "", fmt.Errorf("parse: %w", err)
	}

	latestVersion = release.TagName
	if normalizeVersion(latestVersion) == normalizeVersion(currentVersion) || currentVersion == "dev" {
		return latestVersion, "", "", nil
	}

	if asset, ok := selectReleaseAsset(release.Assets, runtime.GOOS, runtime.GOARCH); ok {
		downloadURL = asset.BrowserDownloadURL
		expectedDigest = strings.TrimPrefix(strings.ToLower(asset.Digest), "sha256:")
	}

	return latestVersion, downloadURL, expectedDigest, nil
}

// performUpdate downloads and installs the latest version. When
// expectedDigest is non-empty (GitHub reported a SHA-256 digest for the
// asset), the downloaded archive's checksum is verified before it is
// extracted and used to replace the running binary; a mismatch aborts the
// update instead of silently installing tampered/corrupted content.
func PerformUpdate(downloadURL, expectedDigest string) error {
	extension := archiveExtension(downloadURL)
	if extension == "" {
		return fmt.Errorf("unsupported update archive: %s", downloadURL)
	}
	workDir, err := os.MkdirTemp("", "covo-update-*")
	if err != nil {
		return fmt.Errorf("create update directory: %w", err)
	}
	defer os.RemoveAll(workDir)
	tmpFile := filepath.Join(workDir, "release"+extension)

	client := &http.Client{Timeout: 120 * time.Second}
	if err := downloadArchive(client, downloadURL, tmpFile, expectedDigest); err != nil {
		return err
	}

	// Find current executable
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable: %w", err)
	}

	extractedName := "covo-agent"
	if runtime.GOOS == "windows" {
		extractedName += ".exe"
	}
	extractedPath := filepath.Join(workDir, extractedName)
	if err := extractBinary(tmpFile, extractedPath); err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	stagedPath, err := stageExecutable(extractedPath, exe)
	if err != nil {
		return fmt.Errorf("stage executable: %w", err)
	}
	defer os.Remove(stagedPath)
	if err := installExecutable(stagedPath, exe); err != nil {
		return fmt.Errorf("install executable: %w", err)
	}

	return nil
}

func downloadArchive(client *http.Client, downloadURL, destination, expectedDigest string) error {
	resp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	hasher := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(output, hasher), resp.Body)
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil {
		return fmt.Errorf("download body: %w", copyErr)
	}
	if syncErr != nil {
		return fmt.Errorf("sync archive: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close archive: %w", closeErr)
	}
	if expectedDigest != "" {
		actualDigest := hex.EncodeToString(hasher.Sum(nil))
		if actualDigest != strings.ToLower(expectedDigest) {
			return fmt.Errorf("checksum mismatch: expected %s, got %s (aborting update)", expectedDigest, actualDigest)
		}
	}
	return nil
}

func stageExecutable(source, target string) (string, error) {
	input, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer input.Close()
	output, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".update-*")
	if err != nil {
		return "", err
	}
	stagedPath := output.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(stagedPath)
		}
	}()
	if err := output.Chmod(0755); err != nil {
		output.Close()
		return "", err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return "", err
	}
	if err := output.Sync(); err != nil {
		output.Close()
		return "", err
	}
	if err := output.Close(); err != nil {
		return "", err
	}
	cleanup = false
	return stagedPath, nil
}

func replaceWithBackup(staged, target string, rename func(string, string) error) error {
	backup := target + ".old"
	_ = os.Remove(backup)
	if err := rename(target, backup); err != nil {
		return fmt.Errorf("back up current executable: %w", err)
	}
	if err := rename(staged, target); err != nil {
		if rollbackErr := rename(backup, target); rollbackErr != nil {
			return fmt.Errorf("activate update: %v; rollback failed: %w", err, rollbackErr)
		}
		return fmt.Errorf("activate update: %w (rolled back)", err)
	}
	_ = os.Remove(backup)
	return nil
}

func normalizeVersion(version string) string {
	return strings.TrimPrefix(strings.TrimSpace(version), "v")
}

func selectReleaseAsset(assets []releaseAsset, goos, goarch string) (releaseAsset, bool) {
	archiveSuffix := ".tar.gz"
	if goos == "windows" {
		archiveSuffix = ".zip"
	}
	arches := []string{goarch}
	switch goarch {
	case "amd64":
		arches = append(arches, "x86_64")
	case "386":
		arches = append(arches, "i386")
	}
	for _, arch := range arches {
		platform := "_" + goos + "_" + arch
		for _, asset := range assets {
			if strings.Contains(asset.Name, platform) && strings.HasSuffix(asset.Name, archiveSuffix) {
				return asset, true
			}
		}
	}
	return releaseAsset{}, false
}

func archiveExtension(downloadURL string) string {
	parsed, err := url.Parse(downloadURL)
	if err != nil {
		return ""
	}
	switch {
	case strings.HasSuffix(parsed.Path, ".tar.gz"):
		return ".tar.gz"
	case strings.HasSuffix(parsed.Path, ".zip"):
		return ".zip"
	default:
		return ""
	}
}

func extractBinary(archivePath, destination string) error {
	if strings.HasSuffix(archivePath, ".zip") {
		return extractBinaryFromZip(archivePath, destination)
	}
	return extractBinaryFromTarGz(archivePath, destination)
}

func extractBinaryFromTarGz(archivePath, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if header.Typeflag == tar.TypeReg && filepath.Base(header.Name) == filepath.Base(destination) {
			return writeExecutable(destination, reader)
		}
	}
	return fmt.Errorf("binary %q not found", filepath.Base(destination))
}

func extractBinaryFromZip(archivePath, destination string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, file := range reader.File {
		if filepath.Base(file.Name) != filepath.Base(destination) || file.FileInfo().IsDir() {
			continue
		}
		input, err := file.Open()
		if err != nil {
			return err
		}
		err = writeExecutable(destination, input)
		input.Close()
		return err
	}
	return fmt.Errorf("binary %q not found", filepath.Base(destination))
}

func writeExecutable(destination string, input io.Reader) error {
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}
