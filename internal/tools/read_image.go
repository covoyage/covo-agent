package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	toolsmedia "github.com/covoyage/covo-agent/internal/tools/media"
	"github.com/covoyage/covonaut/agentcore"
)

// readImageMaxBytes caps the image payload before base64 encoding. Larger
// files are rejected instead of silently blowing up the model context.
const readImageMaxBytes = 20 * 1024 * 1024

// providerIngestsImages reports whether the named provider renders image
// content blocks from tool results into the model context. Providers not in
// this set receive the textual form only, so read_image guides the model to
// the vision_analyze tool instead of attaching dead weight.
func providerIngestsImages(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "anthropic", "gemini", "openai", "openrouter":
		return true
	}
	return false
}

// imageToolResult carries the image into the model context as a block while
// keeping a textual fallback for display and non-multimodal providers.
type imageToolResult struct {
	text   string
	blocks []agentcore.ContentBlock
}

func (r *imageToolResult) ToolBlocks() []agentcore.ContentBlock { return r.blocks }
func (r *imageToolResult) ToolText() string                     { return r.text }

// BuildReadImageTool creates a tool that loads an image from a local path or
// HTTP(S) URL. On multimodal providers the image is attached directly to the
// tool result (multipart tool_result), letting the model see the pixels in
// the same turn without a separate vision-model round trip. On text-only
// providers it falls back to the configured vision analyzer; without one it
// returns guidance pointing at vision_analyze.
func BuildReadImageTool(providerName string, analyze toolsmedia.ImageAnalyzer) *agentcore.Tool {
	direct := providerIngestsImages(providerName)
	return &agentcore.Tool{
		Name: "read_image",
		Description: strings.Join([]string{
			"Load an image from a local file path or HTTP(S) URL so the model can see it.",
			"Prefer read for a local image path; use this tool for HTTP(S) URLs or when you already know the file is an image.",
			directExplanation(direct),
		}, " "),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Local file path or HTTP(S) URL of the image (png, jpeg, gif, webp)",
				},
			},
			"required": []string{"path"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			return AnalyzeImage(ctx, strings.TrimSpace(params.Path), providerName, analyze)
		},
	}
}

// AnalyzeImage loads a local path or HTTP(S) URL and either attaches the
// pixels to the tool result (multimodal providers) or falls back to a
// vision-model description.
func AnalyzeImage(ctx context.Context, path, providerName string, analyze toolsmedia.ImageAnalyzer) (any, error) {
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	data, mime, err := readImageBytes(ctx, path)
	if err != nil {
		return nil, err
	}
	if !providerIngestsImages(providerName) {
		if analyze != nil {
			dataURL := fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(data))
			analysis, analyzeErr := analyze(ctx,
				"Describe this image in detail: its content, any text it contains, and anything visually notable.",
				mime, dataURL)
			if analyzeErr != nil {
				return nil, fmt.Errorf("vision analysis failed: %w", analyzeErr)
			}
			return map[string]any{
				"loaded":    true,
				"path":      path,
				"mime_type": mime,
				"analysis":  analysis,
				"note":      "the current provider cannot ingest images directly; this is a vision-model description",
			}, nil
		}
		return map[string]any{
			"loaded":    true,
			"path":      path,
			"mime_type": mime,
			"size":      len(data),
			"note": "the current provider cannot ingest images directly; " +
				"call vision_analyze with this path to have a vision model describe it",
		}, nil
	}
	dataURL := fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(data))
	return &imageToolResult{
		text: fmt.Sprintf("Image %s attached (%s, %.1f KB).",
			path, mime, float64(len(data))/1024),
		blocks: []agentcore.ContentBlock{
			{Kind: agentcore.BlockKindImage, URL: dataURL, MediaType: mime},
		},
	}, nil
}

func directExplanation(direct bool) string {
	if direct {
		return "The image is attached directly to the tool result, so you can reason about its visual content immediately."
	}
	return "The current provider cannot receive images directly; the result explains how to analyze the image instead."
}

// readImageBytes loads image bytes from a local path or URL and sniffs the
// MIME type from the payload.
func readImageBytes(ctx context.Context, path string) ([]byte, string, error) {
	var (
		data []byte
		err  error
	)
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
		if reqErr != nil {
			return nil, "", fmt.Errorf("build request: %w", reqErr)
		}
		client := &http.Client{Timeout: 60 * time.Second}
		resp, reqErr := client.Do(req)
		if reqErr != nil {
			return nil, "", fmt.Errorf("fetch image: %w", reqErr)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, "", fmt.Errorf("fetch image: status %d", resp.StatusCode)
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, readImageMaxBytes+1))
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, "", fmt.Errorf("read image: %w", err)
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("image %s is empty", path)
	}
	if len(data) > readImageMaxBytes {
		return nil, "", fmt.Errorf("image exceeds %d bytes; export or resize it first", readImageMaxBytes)
	}

	mime := http.DetectContentType(data)
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return data, mime, nil
	case "application/pdf":
		return nil, "", fmt.Errorf("%s is a PDF, not an image; use read on the PDF path instead", filepath.Base(path))
	default:
		return nil, "", fmt.Errorf("unsupported image type %s for %s", mime, path)
	}
}
