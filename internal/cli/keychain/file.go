package keychain

import (
	"encoding/json"
	"errors"
	"os"
)

// fileGet reads a single key from the fallback credentials JSON.
func fileGet(key string) (string, error) {
	store, err := readStore()
	if err != nil {
		return "", err
	}
	val, ok := store[key]
	if !ok {
		return "", nil
	}
	return val, nil
}

// fileSet writes a single key into the fallback credentials JSON,
// preserving other entries.
func fileSet(key, value string) error {
	store, err := readStore()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if store == nil {
		store = make(map[string]string)
	}
	store[key] = value
	return writeStore(store)
}

// fileDelete removes a key from the fallback credentials JSON.
func fileDelete(key string) error {
	store, err := readStore()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	delete(store, key)
	return writeStore(store)
}

func readStore() (map[string]string, error) {
	path, err := credentialsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func writeStore(m map[string]string) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
