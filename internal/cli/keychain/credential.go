// Package keychain provides platform-native credential storage using the
// system keychain (macOS Keychain, Windows Credential Manager, Linux Secret
// Service).
//
// Strategy (OAuthCredentialsStoreMode):
//
//	Auto    – try Keychain first, fall back to file (default).
//	Keyring – Keychain only, fail on error.
//	File    – plain file only, skip Keychain.
//
// The stored value format is plain UTF-8 (OAuth tokens or API keys).
// Keychain entries are identified by service="covo-agent" and an account
// derived from the environment variable name (e.g. "OPENAI_API_KEY").
package keychain

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StoreMode controls where credentials are persisted.
type StoreMode string

const (
	Auto    StoreMode = "auto"
	Keyring StoreMode = "keyring"
	File    StoreMode = "file"
)

const serviceName = "covo-agent"

// Get retrieves a credential from the configured store.
//
//   - Auto / Keyring: reads from the system keychain.
//   - File: reads from the fallback JSON file.
//
// Returns ("", nil) when the entry does not exist.
func Get(account string, mode StoreMode) (string, error) {
	switch mode {
	case File:
		return fileGet(account)
	case Keyring:
		return keyringGet(account)
	default: // Auto
		val, err := keyringGet(account)
		if err != nil {
			// Keychain unavailable – try file fallback.
			if val2, err2 := fileGet(account); err2 == nil {
				return val2, nil
			}
			return "", fmt.Errorf("keychain: %w (file fallback also failed)", err)
		}
		if val != "" {
			return val, nil
		}
		// Keychain returned empty – try file.
		return fileGet(account)
	}
}

// Set stores a credential.
func Set(account, value string, mode StoreMode) error {
	switch mode {
	case File:
		return fileSet(account, value)
	case Keyring:
		return keyringSet(account, value)
	default: // Auto
		if err := keyringSet(account, value); err != nil {
			// Keychain failed – fall back to file.
			return fileSet(account, value)
		}
		// Keychain succeeded — remove stale file entry if present.
		_ = fileDelete(account)
		return nil
	}
}

// Delete removes a credential from both stores.
func Delete(account string, mode StoreMode) error {
	var errs []error
	if mode != File {
		if err := keyringDelete(account); err != nil {
			errs = append(errs, err)
		}
	}
	if mode != Keyring {
		if err := fileDelete(account); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("delete: %v", errs)
	}
	return nil
}

// resolveAccount normalises env var names to keychain account identifiers.
// We base64-encode so that arbitrary env var names work (keychain imposes few
// restrictions but base64 avoids any ambiguity).
func resolveAccount(envName string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.ToUpper(envName)))
}

// ---------------------------------------------------------------------------
// File backend — ~/.covo-agent/.credentials.json
// ---------------------------------------------------------------------------

func credentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".covo-agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, ".credentials.json"), nil
}
