package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"errors"
	"github.com/covoyage/covo-agent/internal/fslock"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateRecoveryVerifiesArtifactsBeforeMutation(t *testing.T) {
	for _, phase := range []string{"prepared", "backed_up", "installed", "cleanup_done", "tampered", "operator_replaced", "unclean_path"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "agent")
			staged := filepath.Join(dir, ".agent.update-test")
			backup := target + ".rollback-test"
			if err := os.WriteFile(target, []byte("old"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(staged, []byte("new"), 0700); err != nil {
				t.Fatal(err)
			}
			oldHash, _ := updateHash(target)
			newHash, _ := updateHash(staged)
			state := updateJournal{Version: 1, Target: target, Staged: staged, Backup: backup, OldHash: oldHash, NewHash: newHash}
			if phase == "unclean_path" {
				state.Staged = dir + string(os.PathSeparator) + "sub" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + filepath.Base(staged)
			}
			if err := writeUpdateJournal(target+".update.json", state); err != nil {
				t.Fatal(err)
			}
			if phase == "backed_up" || phase == "installed" || phase == "cleanup_done" {
				if err := os.Rename(target, backup); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "installed" || phase == "cleanup_done" {
				if err := os.Rename(staged, target); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "tampered" {
				if err := os.WriteFile(staged, []byte("other"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "cleanup_done" {
				if err := os.Remove(backup); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "operator_replaced" {
				if err := os.WriteFile(target, []byte("operator"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			err := RecoverPendingUpdate(target)
			if phase == "tampered" || phase == "operator_replaced" || phase == "unclean_path" {
				if err == nil {
					t.Fatal("tampered stage accepted")
				}
				data, _ := os.ReadFile(target)
				expected := "old"
				if phase == "operator_replaced" {
					expected = "operator"
				}
				if string(data) != expected {
					t.Fatal("live binary changed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(target)
			if string(data) != "new" {
				t.Fatal("update not recovered")
			}
			if err := RecoverPendingUpdate(target); err != nil {
				t.Fatal("recovery not idempotent", err)
			}
		})
	}
}

func TestUpdateJournalRejectsAmbiguityAndLocks(t *testing.T) {
	var state updateJournal
	for _, data := range []string{`{"version":1,"version":1}`, `{"version":null}`, `{"Version":1}`, `{} {}`} {
		if err := decodeUpdateJournal([]byte(data), &state); err == nil {
			t.Fatal("ambiguous journal accepted", data)
		}
	}
	data, _ := json.Marshal(updateJournal{Version: 1})
	if err := decodeUpdateJournal(data, &state); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "lock")
	release, err := fslock.Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if unlock, err := fslock.Acquire(path); err == nil {
		unlock()
		t.Fatal("concurrent lock acquired")
	}
	release()
	unlock, err := fslock.Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}

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

func TestInstallerChecksumFailsClosed(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	content, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	start := strings.Index(script, "verify_checksum() {")
	if start < 0 {
		t.Fatal("checksum function not found")
	}
	end := strings.Index(script[start:], "\ndownload_and_install()")
	if start < 0 || end < 0 {
		t.Fatal("checksum function not found")
	}
	function := script[start : start+end]
	digest := strings.Repeat("a", 64)
	for _, test := range []struct {
		name, output, actual    string
		curlFail, noTool, valid bool
	}{
		{name: "matching", output: digest + "  archive.tar.gz", actual: digest, valid: true},
		{name: "unavailable", curlFail: true},
		{name: "missing entry", output: digest + "  other.tar.gz"},
		{name: "duplicate entry", output: digest + "  archive.tar.gz\n" + digest + "  archive.tar.gz"},
		{name: "malformed digest", output: "invalid  archive.tar.gz"},
		{name: "mismatch", output: digest + "  archive.tar.gz", actual: strings.Repeat("b", 64)},
		{name: "missing tool", noTool: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command("bash")
			command.Env = append(os.Environ(), "CHECKSUM_OUTPUT="+test.output, "CHECKSUM_ACTUAL="+test.actual)
			setup := "set -euo pipefail\nRED='' NC='' MUTED='' REPO=test requested_version=1\n"
			setup += "curl() { printf '%s\\n' \"$CHECKSUM_OUTPUT\"; }\nsha256sum() { printf '%s  file\\n' \"$CHECKSUM_ACTUAL\"; }\n"
			if test.curlFail {
				setup += "curl() { return 22; }\n"
			}
			if test.noTool {
				setup += "command() { return 1; }\n"
			}
			command.Stdin = strings.NewReader(setup + function + "\nverify_checksum ignored archive.tar.gz\n")
			output, err := command.CombinedOutput()
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v output=%s", test.valid, err, output)
			}
		})
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
