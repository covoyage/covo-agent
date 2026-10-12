package cli

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"github.com/covoyage/covo-agent/internal/fslock"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

var configSaveMu sync.Mutex

func configHash(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func (cfg *Config) captureSaveBase(path string, data []byte) {
	cfg.savePath, _ = filepath.Abs(path)
	cfg.saveHash = configHash(data)
	cfg.saveBase, _ = yaml.Marshal(cfg)
}

func configDocument(data []byte) (*yaml.Node, error) {
	var document yaml.Node
	if len(bytes.TrimSpace(data)) == 0 {
		document.Kind = yaml.DocumentNode
		document.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
		return &document, nil
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(yaml.Node)); err != io.EOF {
		return nil, fmt.Errorf("config must contain one YAML document")
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config must be a YAML mapping")
	}
	return &document, nil
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func equalConfigNode(first, second *yaml.Node) bool {
	if first == nil || second == nil {
		return first == second
	}
	left, _ := yaml.Marshal(first)
	right, _ := yaml.Marshal(second)
	return bytes.Equal(left, right)
}

func mergeConfigChanges(current, base, next *yaml.Node) {
	keys := map[string]bool{}
	for _, node := range []*yaml.Node{base, next} {
		if node != nil && node.Kind == yaml.MappingNode {
			for index := 0; index < len(node.Content); index += 2 {
				keys[node.Content[index].Value] = true
			}
		}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		before, after := mappingValue(base, key), mappingValue(next, key)
		if equalConfigNode(before, after) {
			continue
		}
		index := -1
		for candidate := 0; candidate < len(current.Content); candidate += 2 {
			if current.Content[candidate].Value == key {
				index = candidate
				break
			}
		}
		if after == nil {
			if index >= 0 {
				current.Content = append(current.Content[:index], current.Content[index+2:]...)
			}
			continue
		}
		if index < 0 {
			current.Content = append(current.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, after)
			continue
		}
		existing := current.Content[index+1]
		if existing.Kind == yaml.MappingNode && after.Kind == yaml.MappingNode {
			mergeConfigChanges(existing, before, after)
		} else {
			after.HeadComment, after.LineComment, after.FootComment = existing.HeadComment, existing.LineComment, existing.FootComment
			current.Content[index+1] = after
		}
	}
}

func saveConfigFile(cfg *Config, path string) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	configSaveMu.Lock()
	defer configSaveMu.Unlock()
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	release, err := fslock.Acquire(absolute + ".lock")
	if err != nil {
		return err
	}
	defer release()
	mode := os.FileMode(0600)
	if info, err := os.Lstat(absolute); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("config is not a regular file")
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := os.ReadFile(absolute)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if cfg.savePath == absolute && cfg.saveHash != configHash(data) {
		return fmt.Errorf("config changed since loading; reload before saving")
	}
	current, err := configDocument(data)
	if err != nil {
		return err
	}
	base, err := configDocument(cfg.saveBase)
	if err != nil {
		return err
	}
	if cfg.savePath != absolute {
		base, _ = configDocument(nil)
	}
	proposed, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	next, err := configDocument(proposed)
	if err != nil {
		return err
	}
	mergeConfigChanges(current.Content[0], base.Content[0], next.Content[0])
	var encoded bytes.Buffer
	encoder := yaml.NewEncoder(&encoded)
	encoder.SetIndent(2)
	if err := encoder.Encode(current); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(absolute), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(encoded.Bytes()); err != nil {
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
	if err := os.Rename(file.Name(), absolute); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		dir, err := os.Open(filepath.Dir(absolute))
		if err != nil {
			return err
		}
		err = dir.Sync()
		dir.Close()
		if err != nil {
			return err
		}
	}
	cfg.captureSaveBase(absolute, encoded.Bytes())
	return nil
}
