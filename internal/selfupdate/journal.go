package selfupdate

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/covoyage/covo-agent/internal/fslock"
)

type updateJournal struct {
	Version int    `json:"version"`
	Target  string `json:"target"`
	Staged  string `json:"staged"`
	Backup  string `json:"backup"`
	OldHash string `json:"old_hash"`
	NewHash string `json:"new_hash"`
}

func updateHash(path string) (string, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 128<<20 {
		return "", fmt.Errorf("update artifact is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(file, 128<<20+1))
	if err != nil {
		return "", err
	}
	if count > 128<<20 {
		return "", fmt.Errorf("update artifact read budget exceeded")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func syncUpdateDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func writeUpdateJournal(path string, state updateJournal) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".update-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	return syncUpdateDir(filepath.Dir(path))
}

func decodeUpdateJournal(data []byte, state *updateJournal) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return fmt.Errorf("invalid update journal")
	}
	seen := map[string]bool{}
	allowed := map[string]bool{"version": true, "target": true, "staged": true, "backup": true, "old_hash": true, "new_hash": true}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok || seen[key] || !allowed[key] {
			return fmt.Errorf("duplicate or unknown update journal field")
		}
		seen[key] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("null update journal field")
		}
	}
	if len(seen) != len(allowed) {
		return fmt.Errorf("incomplete update journal")
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing update journal data")
	}
	strict := json.NewDecoder(bytes.NewReader(data))
	strict.DisallowUnknownFields()
	return strict.Decode(state)
}

func recoverUpdateLocked(target string) error {
	path := target + ".update.json"
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 16384 {
		return fmt.Errorf("invalid update journal file")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(file, 16385))
	file.Close()
	if err != nil || len(data) > 16384 {
		return fmt.Errorf("update journal read failed")
	}
	var state updateJournal
	if err := decodeUpdateJournal(data, &state); err != nil {
		return err
	}
	validHash := func(value string) bool {
		decoded, err := hex.DecodeString(value)
		return err == nil && len(decoded) == 32 && value == strings.ToLower(value)
	}
	dir, base := filepath.Dir(target), filepath.Base(target)
	if state.Version != 1 || state.Target != target || !validHash(state.OldHash) || !validHash(state.NewHash) || state.Staged != filepath.Clean(state.Staged) || state.Backup != filepath.Clean(state.Backup) || filepath.Dir(state.Staged) != dir || !strings.HasPrefix(filepath.Base(state.Staged), "."+base+".update-") || filepath.Dir(state.Backup) != dir || !strings.HasPrefix(filepath.Base(state.Backup), base+".rollback-") {
		return fmt.Errorf("invalid update recovery evidence")
	}
	live, err := updateHash(target)
	if err != nil {
		return err
	}
	staged, err := updateHash(state.Staged)
	if err != nil {
		return err
	}
	backup, err := updateHash(state.Backup)
	if err != nil {
		return err
	}
	if live == state.NewHash && (backup == state.OldHash || backup == "missing" && (staged == "missing" || state.OldHash == state.NewHash)) {
		if staged != "missing" && staged != state.NewHash {
			return fmt.Errorf("staged update evidence changed")
		}
	} else {
		if staged != state.NewHash {
			return fmt.Errorf("staged binary does not match recovery record")
		}
		switch {
		case live == state.OldHash && backup == "missing":
			if err := os.Rename(target, state.Backup); err != nil {
				return err
			}
			if err := syncUpdateDir(dir); err != nil {
				return err
			}
		case live == "missing" && backup == state.OldHash:
		default:
			return fmt.Errorf("live or rollback binary does not match recovery record")
		}
		if err := os.Rename(state.Staged, target); err != nil {
			restoreErr := os.Rename(state.Backup, target)
			return fmt.Errorf("activate update: %v; restore: %v", err, restoreErr)
		}
		if err := syncUpdateDir(dir); err != nil {
			return err
		}
	}
	verified, err := updateHash(target)
	if err != nil || verified != state.NewHash {
		return fmt.Errorf("activated binary does not match update record")
	}
	if staged != "missing" {
		if err := os.Remove(state.Staged); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	currentBackup, err := updateHash(state.Backup)
	if err != nil {
		return err
	}
	if currentBackup != "missing" {
		if currentBackup != state.OldHash {
			return fmt.Errorf("rollback binary changed")
		}
		if err := os.Remove(state.Backup); err != nil {
			return err
		}
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncUpdateDir(dir)
}

func installJournaled(staged, target string) error {
	oldHash, err := updateHash(target)
	if err != nil || oldHash == "missing" {
		return fmt.Errorf("current binary unavailable: %v", err)
	}
	newHash, err := updateHash(staged)
	if err != nil || newHash == "missing" {
		return fmt.Errorf("staged binary unavailable: %v", err)
	}
	state := updateJournal{Version: 1, Target: target, Staged: staged, Backup: target + ".rollback-" + rand.Text(), OldHash: oldHash, NewHash: newHash}
	if err := writeUpdateJournal(target+".update.json", state); err != nil {
		return err
	}
	return recoverUpdateLocked(target)
}

func RecoverPendingUpdate(target string) error {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(absolute + ".update.json"); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	release, err := fslock.Acquire(absolute + ".update.lock")
	if err != nil {
		return err
	}
	defer release()
	return recoverUpdateLocked(absolute)
}
