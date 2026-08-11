package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

var falVideoModels = map[string]string{
	"minimax-video": "fal-ai/minimax-video",
	"kling-video":   "fal-ai/kling-video/v1",
	"wan":           "fal-ai/wan/v2.1/1.3b",
	"wan-fast":      "fal-ai/wan/v2.1/1.3b/fast",
	"hailuo-video":  "fal-ai/hailuo-video",
	"pixverse":      "fal-ai/pixverse/v5",
}

func BuildVideoGenerateTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "video_generate",
		Description: strings.Join([]string{
			"Generate videos from text prompts using AI models via FAL.ai.",
			"Requires FAL_KEY environment variable.",
			"",
			"Available models:",
			"- wan (default, fast, good quality)",
			"- wan-fast (fastest)",
			"- minimax-video (high quality)",
			"- kling-video (high quality)",
			"- hailuo-video (high quality)",
			"- pixverse (high quality)",
			"",
			"Videos are saved to a temp directory and the local path is returned.",
			"Generation may take 30-120 seconds depending on the model.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":        "string",
					"description": "Text description of the video to generate. Be detailed about motion, scene, and style.",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Model to use (default: wan). Options: wan, wan-fast, minimax-video, kling-video, hailuo-video, pixverse.",
				},
				"aspect_ratio": map[string]any{
					"type":        "string",
					"description": "Video aspect ratio (default: 16:9). Options: 16:9, 9:16, 1:1.",
				},
				"duration": map[string]any{
					"type":        "number",
					"description": "Video duration in seconds (default: 5, max varies by model).",
				},
			},
			"required": []string{"prompt"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Prompt      string  `json:"prompt"`
				Model       string  `json:"model"`
				AspectRatio string  `json:"aspect_ratio"`
				Duration    float64 `json:"duration"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if strings.TrimSpace(params.Prompt) == "" {
				return nil, fmt.Errorf("prompt is required")
			}

			apiKey := os.Getenv("FAL_KEY")
			if apiKey == "" {
				return nil, fmt.Errorf("FAL_KEY environment variable is not set")
			}

			if params.Model == "" {
				params.Model = "wan"
			}
			if params.AspectRatio == "" {
				params.AspectRatio = "16:9"
			}
			if params.Duration <= 0 {
				params.Duration = 5
			}

			modelPath, ok := falVideoModels[params.Model]
			if !ok {
				return nil, fmt.Errorf("unknown model %q. Available: wan, wan-fast, minimax-video, kling-video, hailuo-video, pixverse", params.Model)
			}

			payload := map[string]any{
				"prompt":       params.Prompt,
				"aspect_ratio": params.AspectRatio,
				"duration":     params.Duration,
			}

			return callFALMedia(ctx, apiKey, modelPath, payload, "video")
		},
	}
}

func BuildMusicGenerateTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "music_generate",
		Description: strings.Join([]string{
			"Generate music tracks from text prompts using AI models via FAL.ai.",
			"Requires FAL_KEY environment variable.",
			"",
			"Available models:",
			"- stable-audio (default, good quality)",
			"- musicgen (Meta's MusicGen)",
			"",
			"Music is saved to a temp directory and the local path is returned.",
			"Supports specifying duration and output format.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":        "string",
					"description": "Text description of the music to generate. Describe genre, mood, instruments, tempo.",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Model to use (default: stable-audio). Options: stable-audio, musicgen.",
				},
				"duration": map[string]any{
					"type":        "number",
					"description": "Duration in seconds (default: 30, max: 120).",
				},
				"format": map[string]any{
					"type":        "string",
					"description": "Output format: mp3 (default) or wav.",
					"enum":        []string{"mp3", "wav"},
				},
			},
			"required": []string{"prompt"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Prompt   string  `json:"prompt"`
				Model    string  `json:"model"`
				Duration float64 `json:"duration"`
				Format   string  `json:"format"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if strings.TrimSpace(params.Prompt) == "" {
				return nil, fmt.Errorf("prompt is required")
			}

			apiKey := os.Getenv("FAL_KEY")
			if apiKey == "" {
				return nil, fmt.Errorf("FAL_KEY environment variable is not set")
			}

			if params.Model == "" {
				params.Model = "stable-audio"
			}
			if params.Duration <= 0 {
				params.Duration = 30
			}
			if params.Duration > 120 {
				params.Duration = 120
			}
			if params.Format == "" {
				params.Format = "mp3"
			}

			var modelPath string
			switch params.Model {
			case "stable-audio":
				modelPath = "fal-ai/stable-audio"
			case "musicgen":
				modelPath = "fal-ai/musicgen"
			default:
				return nil, fmt.Errorf("unknown model %q. Available: stable-audio, musicgen", params.Model)
			}

			payload := map[string]any{
				"prompt":        params.Prompt,
				"duration":      params.Duration,
				"output_format": params.Format,
			}

			return callFALMedia(ctx, apiKey, modelPath, payload, "audio")
		},
	}
}

// callFALMedia is a shared helper for video/music generation via FAL queue API.
func callFALMedia(ctx context.Context, apiKey, modelPath string, payload map[string]any, mediaType string) (any, error) {
	url := fmt.Sprintf("https://queue.fal.run/%s", modelPath)

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Key "+apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("FAL request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("FAL API error (%d): %s", resp.StatusCode, string(respBody))
	}

	var queueResp struct {
		RequestID string `json:"request_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(respBody, &queueResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if queueResp.RequestID == "" {
		return nil, fmt.Errorf("no request_id in response: %s", string(respBody))
	}

	// Poll for result (media generation takes longer)
	statusURL := fmt.Sprintf("https://queue.fal.run/%s/requests/%s/status", modelPath, queueResp.RequestID)
	resultURL := fmt.Sprintf("https://queue.fal.run/%s/requests/%s", modelPath, queueResp.RequestID)

	for i := 0; i < 180; i++ { // max 180 polls = ~3 minutes
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		time.Sleep(1 * time.Second)

		statusReq, _ := http.NewRequestWithContext(ctx, "GET", statusURL, nil)
		statusReq.Header.Set("Authorization", "Key "+apiKey)
		statusResp, err := client.Do(statusReq)
		if err != nil {
			continue
		}
		statusBody, _ := io.ReadAll(statusResp.Body)
		statusResp.Body.Close()

		var status struct {
			Status string `json:"status"`
		}
		json.Unmarshal(statusBody, &status)

		if status.Status == "COMPLETED" || status.Status == "completed" {
			resultReq, _ := http.NewRequestWithContext(ctx, "GET", resultURL, nil)
			resultReq.Header.Set("Authorization", "Key "+apiKey)
			resultResp, err := client.Do(resultReq)
			if err != nil {
				return nil, fmt.Errorf("fetch result: %w", err)
			}
			defer resultResp.Body.Close()
			resultBody, _ := io.ReadAll(resultResp.Body)

			// Parse result — different models return different structures
			var result map[string]any
			if err := json.Unmarshal(resultBody, &result); err != nil {
				return nil, fmt.Errorf("parse result: %w", err)
			}

			// Try to find the media URL in common response formats
			mediaURL := extractMediaURL(result, mediaType)
			if mediaURL == "" {
				return map[string]any{
					"status": "generated",
					"model":  modelPath,
					"result": result,
				}, nil
			}

			// Download to local file
			tmpDir := filepath.Join(os.TempDir(), "covo-"+mediaType)
			_ = os.MkdirAll(tmpDir, 0755)

			ext := ".mp4"
			if mediaType == "audio" {
				ext = ".mp3"
			}

			localPath, err := downloadFile(ctx, client, mediaURL, tmpDir, ext)
			if err != nil {
				return map[string]any{
					"status": "generated",
					"model":  modelPath,
					"url":    mediaURL,
					"error":  err.Error(),
				}, nil
			}

			info, _ := os.Stat(localPath)
			size := int64(0)
			if info != nil {
				size = info.Size()
			}

			return map[string]any{
				"status":  "generated",
				"model":   modelPath,
				"path":    localPath,
				"url":     mediaURL,
				"size_kb": size / 1024,
			}, nil
		}

		if status.Status == "FAILED" || status.Status == "failed" {
			return nil, fmt.Errorf("%s generation failed: %s", mediaType, string(statusBody))
		}
	}

	return nil, fmt.Errorf("timeout waiting for %s generation (3 minutes)", mediaType)
}

func extractMediaURL(result map[string]any, mediaType string) string {
	// Try common response formats
	for _, key := range []string{"video", "audio", "url", "file"} {
		if v, ok := result[key]; ok {
			switch val := v.(type) {
			case string:
				if strings.HasPrefix(val, "http") {
					return val
				}
			case map[string]any:
				if u, ok := val["url"].(string); ok && strings.HasPrefix(u, "http") {
					return u
				}
			}
		}
	}

	// Try nested in array
	for _, key := range []string{"videos", "audios", "files", "outputs"} {
		if arr, ok := result[key].([]any); ok && len(arr) > 0 {
			if m, ok := arr[0].(map[string]any); ok {
				if u, ok := m["url"].(string); ok && strings.HasPrefix(u, "http") {
					return u
				}
			}
		}
	}

	return ""
}

func downloadFile(ctx context.Context, client *http.Client, url, dir, ext string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: %d", resp.StatusCode)
	}

	outFile, err := os.CreateTemp(dir, "media-*"+ext)
	if err != nil {
		return "", err
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, resp.Body); err != nil {
		os.Remove(outFile.Name())
		return "", err
	}

	return outFile.Name(), nil
}
