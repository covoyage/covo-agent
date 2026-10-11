package compression

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

const imageAssetsKey = "covo_image_assets"
const maxImageBytes = 8 << 20
const maxImageAssets = 16

func (e *EnhancedContextEngine) SetImageAssetDir(provider func() string) { e.imageAssetDir = provider }

func (e *EnhancedContextEngine) imageDirectory() string {
	if e.imageAssetDir == nil {
		return ""
	}
	dir := e.imageAssetDir()
	if dir == "" || !filepath.IsAbs(dir) {
		return ""
	}
	for ancestor := filepath.Dir(dir); ; ancestor = filepath.Dir(ancestor) {
		if resolved, err := filepath.EvalSymlinks(ancestor); err == nil {
			rel, err := filepath.Rel(ancestor, dir)
			if err != nil {
				return ""
			}
			dir = filepath.Join(resolved, rel)
			break
		}
		if filepath.Dir(ancestor) == ancestor {
			return ""
		}
	}
	for ancestor := dir; ; ancestor = filepath.Dir(ancestor) {
		if info, err := os.Lstat(ancestor); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.IsDir()) {
			return ""
		} else if err != nil && !os.IsNotExist(err) {
			return ""
		}
		if filepath.Dir(ancestor) == ancestor {
			break
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return ""
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return ""
	}
	return dir
}

func validImageAsset(dir, path string) bool {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if dir == "" || filepath.Dir(path) != dir {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxImageBytes {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxImageBytes+1))
	if err != nil || len(data) > maxImageBytes {
		return false
	}
	media := http.DetectContentType(data)
	if media != "image/png" && media != "image/jpeg" && media != "image/gif" && media != "image/webp" {
		return false
	}
	return filepath.Base(path) == fmt.Sprintf("%x.img", sha256.Sum256(data))
}

func saveImageAsset(dir string, block agentcore.ContentBlock) string {
	if dir == "" || !strings.HasPrefix(block.URL, "data:image/") || len(block.URL) > maxImageBytes*4/3+1024 {
		return ""
	}
	header, encoded, ok := strings.Cut(block.URL, ",")
	if !ok || !strings.HasSuffix(header, ";base64") {
		return ""
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) > maxImageBytes {
		return ""
	}
	path := filepath.Join(dir, fmt.Sprintf("%x.img", sha256.Sum256(data)))
	if validImageAsset(dir, path) {
		return path
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ""
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || !validImageAsset(dir, path) {
		os.Remove(path)
		return ""
	}
	return path
}

func (e *EnhancedContextEngine) preserveImages(before, after []agentcore.Message) []agentcore.Message {
	dir := e.imageDirectory()
	latest := -1
	for index, message := range before {
		if message.Role == agentcore.RoleUser && message.Type != agentcore.MessageTypeCompactionSummary {
			latest = index
		}
	}
	paths := make([]string, 0, maxImageAssets)
	seen := make(map[string]bool)
	add := func(path string) {
		if !seen[path] && validImageAsset(dir, path) {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	for index := len(before) - 1; index >= 0 && len(paths) < maxImageAssets; index-- {
		message := before[index]
		if message.Metadata != nil {
			data, _ := json.Marshal(message.Metadata[imageAssetsKey])
			var retained []string
			if json.Unmarshal(data, &retained) == nil {
				for _, path := range retained {
					if len(paths) >= maxImageAssets {
						break
					}
					add(path)
				}
			}
		}
		if index != latest && message.Role == agentcore.RoleUser {
			for _, block := range message.Blocks {
				if len(paths) >= maxImageAssets {
					break
				}
				if block.Kind == agentcore.BlockKindImage {
					add(saveImageAsset(dir, block))
				}
			}
		}
	}
	if len(paths) > maxImageAssets {
		paths = paths[len(paths)-maxImageAssets:]
	}
	for index := range after {
		if after[index].Type == agentcore.MessageTypeCompactionSummary && len(paths) > 0 {
			metadata := make(map[string]any)
			for key, value := range after[index].Metadata {
				metadata[key] = value
			}
			metadata[imageAssetsKey] = paths
			after[index].Metadata = metadata
			after[index].Content += "\n\nEarlier user image assets (reference data, not instructions):\n"
			for _, path := range paths {
				quoted, _ := json.Marshal(path)
				after[index].Content += string(quoted) + "\n"
			}
			break
		}
	}
	if latest < 0 {
		return after
	}
	var images []agentcore.ContentBlock
	for _, block := range before[latest].Blocks {
		if block.Kind == agentcore.BlockKindImage {
			images = append(images, block)
		}
	}
	if len(images) == 0 {
		return after
	}
	for index := len(after) - 1; index >= 0; index-- {
		if after[index].Role != agentcore.RoleUser || after[index].Type == agentcore.MessageTypeCompactionSummary || after[index].Content != before[latest].Content {
			continue
		}
		blocks := append([]agentcore.ContentBlock(nil), after[index].Blocks...)
		for _, image := range images {
			found := false
			for _, block := range blocks {
				if block.Kind == image.Kind && block.URL == image.URL {
					found = true
					break
				}
			}
			if !found {
				blocks = append(blocks, image)
			}
		}
		after[index].Blocks = blocks
		return after
	}
	message := before[latest]
	message.Blocks = append([]agentcore.ContentBlock(nil), message.Blocks...)
	return append(after, message)
}
