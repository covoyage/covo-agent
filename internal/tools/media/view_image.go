package media

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

const (
	viewImageMaxBytes   = 4 * 1024 * 1024
	viewImageMaxChars   = 1200
	viewImageMaxImages  = 8
	viewImageMultiChars = 400
)

// ImageAnalyzer describes an image given a data URL.
type ImageAnalyzer func(ctx context.Context, prompt, mediaType, dataURL string) (string, error)

func BuildViewImageTool(workDir string, analyze ImageAnalyzer) *agentcore.Tool {
	return BuildViewImageToolWithTransport(workDir, analyze, nil)
}

// BuildViewImageToolWithTransport is BuildViewImageTool plus optional HTTP
// transport for http(s) image URLs (SSRF-safe when the caller passes one).
func BuildViewImageToolWithTransport(workDir string, analyze ImageAnalyzer, transport http.RoundTripper) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "view_image",
		Description: strings.Join([]string{
			"Analyze png/jpeg/gif/webp image(s) with the vision model.",
			"Pass path for one image, or paths for several (max 8). Local workspace paths or http(s) URLs.",
			"Remote fetches require a safe transport (SSRF-checked) and are capped at 4MB each.",
			"Screenshots from computer_use can be passed by path.",
			"For video files, use video_analyze (ffmpeg stills) instead of this tool.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Local image path or http(s) URL.",
				},
				"paths": map[string]any{
					"type":        "array",
					"description": "Multiple image paths or URLs (max 8). Combined with path if both are set.",
					"items":       map[string]any{"type": "string"},
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "Optional question or focus for the vision model.",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Path   string   `json:"path"`
				Paths  []string `json:"paths"`
				Prompt string   `json:"prompt"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			paths := collectImagePaths(params.Path, params.Paths)
			if len(paths) == 0 {
				return nil, fmt.Errorf("path or paths is required")
			}
			if len(paths) > viewImageMaxImages {
				return nil, fmt.Errorf("at most %d images per call", viewImageMaxImages)
			}
			prompt := strings.TrimSpace(params.Prompt)
			if prompt == "" {
				prompt = "Describe this image. Call out any text, UI, errors, or details useful for a coding agent."
			}
			if analyze == nil {
				return nil, fmt.Errorf("vision model is not configured")
			}
			if len(paths) == 1 {
				return describeOneImage(ctx, paths[0], workDir, transport, analyze, prompt, viewImageMaxChars)
			}
			perCap := viewImageMultiChars
			images := make([]map[string]any, 0, len(paths))
			var combined strings.Builder
			anyOK := false
			truncated := false
			for i, p := range paths {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				item, err := describeOneImage(ctx, p, workDir, transport, analyze, prompt, perCap)
				if err != nil {
					images = append(images, map[string]any{"path": p, "error": err.Error()})
					continue
				}
				anyOK = true
				if t, _ := item["truncated"].(bool); t {
					truncated = true
				}
				images = append(images, item)
				if combined.Len() > 0 {
					combined.WriteByte('\n')
				}
				fmt.Fprintf(&combined, "Image %d (%s): %s", i+1, item["path"], item["description"])
			}
			if !anyOK {
				return nil, fmt.Errorf("vision model failed on all %d images", len(paths))
			}
			summary, sumTrunc := truncateRunes(combined.String(), viewImageMaxChars)
			return map[string]any{
				"images":      images,
				"count":       len(images),
				"prompt":      prompt,
				"description": summary,
				"truncated":   truncated || sumTrunc,
			}, nil
		},
	}
}

func collectImagePaths(path string, paths []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	add(path)
	for _, p := range paths {
		add(p)
	}
	return out
}

func describeOneImage(ctx context.Context, path, workDir string, transport http.RoundTripper, analyze ImageAnalyzer, prompt string, maxChars int) (map[string]any, error) {
	abs, mediaType, data, err := loadImage(ctx, path, workDir, transport)
	if err != nil {
		return nil, err
	}
	dataURL := "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)
	desc, err := analyze(ctx, prompt, mediaType, dataURL)
	if err != nil {
		return nil, err
	}
	desc, truncated := truncateRunes(desc, maxChars)
	return map[string]any{
		"path":        abs,
		"media_type":  mediaType,
		"bytes":       len(data),
		"prompt":      prompt,
		"description": desc,
		"truncated":   truncated,
	}, nil
}

func loadImage(ctx context.Context, path, workDir string, transport http.RoundTripper) (abs, mediaType string, data []byte, err error) {
	if u, perr := url.Parse(path); perr == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return fetchRemoteImage(ctx, u, transport)
	}
	return loadLocalImage(path, workDir)
}

func fetchRemoteImage(ctx context.Context, u *url.URL, transport http.RoundTripper) (abs, mediaType string, data []byte, err error) {
	if transport == nil {
		return "", "", nil, fmt.Errorf("remote URLs require a safe HTTP transport; pass a local file path")
	}
	if u.Host == "" {
		return "", "", nil, fmt.Errorf("invalid image url")
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: transport}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", "", nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", nil, fmt.Errorf("fetch image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", nil, fmt.Errorf("fetch image: HTTP %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, viewImageMaxBytes+1)
	data, err = io.ReadAll(limited)
	if err != nil {
		return "", "", nil, fmt.Errorf("read image: %w", err)
	}
	if len(data) > viewImageMaxBytes {
		return "", "", nil, fmt.Errorf("image too large (%d bytes, max %d)", len(data), viewImageMaxBytes)
	}
	mediaType = sniffImageMediaType(data, resp.Header.Get("Content-Type"), u.Path)
	if mediaType == "" {
		return "", "", nil, fmt.Errorf("unsupported image type (use png, jpeg, gif, or webp)")
	}
	return u.String(), mediaType, data, nil
}

func sniffImageMediaType(data []byte, contentType, path string) string {
	if len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n" {
		return "image/png"
	}
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "image/jpeg"
	}
	if len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a") {
		return "image/gif"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	ct := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch ct {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return ct
	case "image/jpg":
		return "image/jpeg"
	}
	return imageMediaType(path)
}

func loadLocalImage(path, workDir string) (abs, mediaType string, data []byte, err error) {
	if strings.Contains(path, "://") {
		if u, perr := url.Parse(path); perr == nil && u.Scheme != "" && u.Scheme != "file" {
			return "", "", nil, fmt.Errorf("remote URLs are not allowed; pass a local file path")
		}
	}
	if !filepath.IsAbs(path) && workDir != "" {
		path = filepath.Join(workDir, path)
	}
	abs, err = filepath.Abs(path)
	if err != nil {
		return "", "", nil, fmt.Errorf("resolve path: %w", err)
	}
	if workDir != "" {
		if err := allowMediaPath(abs, workDir, "image"); err != nil {
			return "", "", nil, err
		}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", "", nil, fmt.Errorf("stat image: %w", err)
	}
	if info.IsDir() {
		return "", "", nil, fmt.Errorf("path is a directory")
	}
	if info.Size() > viewImageMaxBytes {
		return "", "", nil, fmt.Errorf("image too large (%d bytes, max %d)", info.Size(), viewImageMaxBytes)
	}
	mediaType = imageMediaType(abs)
	if mediaType == "" {
		return "", "", nil, fmt.Errorf("unsupported image type (use png, jpeg, gif, or webp)")
	}
	data, err = os.ReadFile(abs)
	if err != nil {
		return "", "", nil, fmt.Errorf("read image: %w", err)
	}
	return abs, mediaType, data, nil
}

func allowImagePath(abs, workDir string) error {
	return allowMediaPath(abs, workDir, "image")
}

func allowMediaPath(abs, workDir, kind string) error {
	if kind == "" {
		kind = "file"
	}
	ws, err := filepath.Abs(workDir)
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	if isUnder(abs, ws) {
		return nil
	}
	if tmp, err := filepath.Abs(filepath.Join(os.TempDir(), "covo-computer")); err == nil && isUnder(abs, tmp) {
		return nil
	}
	return fmt.Errorf("%s path %q is outside the workspace", kind, abs)
}

func isUnder(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func imageMediaType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}
