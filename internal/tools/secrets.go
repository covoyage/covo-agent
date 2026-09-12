package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/cli/keychain"
	"github.com/covoyage/covonaut/agentcore"
)

type SecretsStore struct {
	path string
	data map[string]string
}

func NewSecretsStore(homeDir string) *SecretsStore {
	path := filepath.Join(homeDir, "secrets.json")
	s := &SecretsStore{
		path: path,
		data: make(map[string]string),
	}
	s.load()
	return s
}

func (s *SecretsStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var entries []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return
	}
	for _, e := range entries {
		s.data[e.Key] = e.Value
	}
}

func (s *SecretsStore) save() error {
	var entries []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	for k, v := range s.data {
		entries = append(entries, struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}{k, v})
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0600)
}

func (s *SecretsStore) Set(key, value string) error {
	s.data[key] = value
	return s.save()
}

func (s *SecretsStore) Get(key string) (string, bool) {
	if v, err := keychain.Get(key, keychain.Auto); err == nil && v != "" {
		return v, true
	}
	v, ok := s.data[key]
	return v, ok
}

func (s *SecretsStore) Delete(key string) error {
	delete(s.data, key)
	return s.save()
}

func (s *SecretsStore) List() []string {
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	return keys
}

func buildSecretsTool(store *SecretsStore) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "secrets",
		Description: strings.Join([]string{
			"Resolve a named secret into the process environment without putting",
			"the value into the conversation. Prefer this over reading .env files",
			"or echoing tokens.",
			"",
			"Actions:",
			"- get: Load a secret into the environment (value is never returned)",
			"- list: List stored secret keys (values are never shown)",
			"- set: Store a secret key-value pair",
			"- delete: Remove a stored secret",
			"- bw_get: Load a Bitwarden secret into the environment (requires 'bw')",
			"- env: Same as get",
			"",
			"Use this for API tokens, connection strings, and passwords.",
			"Never print, quote, or write the secret value into files or replies.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Action: set, get, delete, list, bw_get, env",
					"enum":        []string{"set", "get", "delete", "list", "bw_get", "env"},
				},
				"key": map[string]any{
					"type":        "string",
					"description": "Secret key/name (required for set, get, delete, bw_get).",
				},
				"value": map[string]any{
					"type":        "string",
					"description": "Secret value (required for set).",
				},
				"bw_item_id": map[string]any{
					"type":        "string",
					"description": "Bitwarden item ID (for bw_get, alternative to key).",
				},
			},
			"required": []string{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Action   string `json:"action"`
				Key      string `json:"key"`
				Value    string `json:"value"`
				BwItemID string `json:"bw_item_id"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			switch params.Action {
			case "set":
				if strings.TrimSpace(params.Key) == "" {
					return nil, fmt.Errorf("key is required for set")
				}
				if strings.TrimSpace(params.Value) == "" {
					return nil, fmt.Errorf("value is required for set")
				}
				if err := store.Set(params.Key, params.Value); err != nil {
					return nil, fmt.Errorf("store secret: %w", err)
				}
				return map[string]any{
					"action": "set",
					"key":    params.Key,
					"stored": true,
				}, nil

			case "get":
				return exportSecret(store, params.Key)

			case "delete":
				if strings.TrimSpace(params.Key) == "" {
					return nil, fmt.Errorf("key is required for delete")
				}
				if err := store.Delete(params.Key); err != nil {
					return nil, fmt.Errorf("delete secret: %w", err)
				}
				return map[string]any{
					"action": "deleted",
					"key":    params.Key,
				}, nil

			case "list":
				keys := store.List()
				return map[string]any{
					"action": "list",
					"keys":   keys,
					"count":  len(keys),
				}, nil

			case "bw_get":
				if _, err := exec.LookPath("bw"); err != nil {
					return nil, fmt.Errorf("Bitwarden CLI (bw) not found. Install from https://bitwarden.com/download/")
				}
				lookup := params.Key
				if params.BwItemID != "" {
					lookup = params.BwItemID
				}
				if lookup == "" {
					return nil, fmt.Errorf("key or bw_item_id is required for bw_get")
				}

				ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()

				cmd := exec.CommandContext(ctx, "bw", "get", "password", lookup)
				cmd.Env = os.Environ()
				out, err := cmd.Output()
				if err != nil {
					if exitErr, ok := err.(*exec.ExitError); ok {
						return nil, fmt.Errorf("bw get failed: %s", string(exitErr.Stderr))
					}
					return nil, fmt.Errorf("bw get: %w", err)
				}

				val := strings.TrimSpace(string(out))
				if params.Key != "" {
					_ = store.Set(params.Key, val)
				}

				envKey := strings.ToUpper(strings.ReplaceAll(lookup, "-", "_"))
				if err := os.Setenv(envKey, val); err != nil {
					return nil, fmt.Errorf("export secret: %w", err)
				}
				return map[string]any{
					"action":   "bw_get",
					"key":      lookup,
					"env_var":  envKey,
					"exported": true,
					"cached":   params.Key != "",
				}, nil

			case "env":
				return exportSecret(store, params.Key)

			default:
				return nil, fmt.Errorf("unknown action: %s", params.Action)
			}
		},
	}
}

func exportSecret(store *SecretsStore, key string) (any, error) {
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("key is required")
	}
	val, ok := store.Get(key)
	if !ok {
		return nil, fmt.Errorf("secret %q not found", key)
	}
	envKey := strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
	if err := os.Setenv(envKey, val); err != nil {
		return nil, fmt.Errorf("export secret: %w", err)
	}
	return map[string]any{
		"action":   "get",
		"key":      key,
		"env_var":  envKey,
		"exported": true,
	}, nil
}
