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

var falModels = map[string]string{
	"flux-pro":     "fal-ai/flux-pro/v1.1",
	"flux-schnell": "fal-ai/flux/schnell",
	"flux-dev":     "fal-ai/flux/dev",
	"flux-realism": "fal-ai/flux-realism",
	"sdxl":         "fal-ai/fast-sdxl",
}

func BuildImageGenerateTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "image_generate",
		Description: strings.Join([]string{
			"Generate images from text prompts using AI models via FAL.ai.",
			"Requires FAL_KEY environment variable (get one at https://fal.ai).",
			"",
			"Available models:",
			"- flux-schnell (default, fastest, ~1s)",
			"- flux-dev (balanced quality/speed)",
			"- flux-pro (highest quality, slower)",
			"- flux-realism (photorealistic)",
			"- sdxl (Stable Diffusion XL)",
			"",
			"Images are saved to a temp directory and the path is returned.",
			"Supports aspect_ratio: 1:1, 16:9, 9:16, 4:3, 3:4, etc.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":        "string",
					"description": "Text description of the image to generate. Be detailed and specific.",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Model to use (default: flux-schnell). Options: flux-schnell, flux-dev, flux-pro, flux-realism, sdxl.",
				},
				"aspect_ratio": map[string]any{
					"type":        "string",
					"description": "Image aspect ratio (default: 1:1). Options: 1:1, 16:9, 9:16, 4:3, 3:4.",
				},
				"num_images": map[string]any{
					"type":        "integer",
					"description": "Number of images to generate (default: 1, max: 4).",
				},
				"seed": map[string]any{
					"type":        "integer",
					"description": "Random seed for reproducibility (optional).",
				},
			},
			"required": []string{"prompt"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Prompt      string `json:"prompt"`
				Model       string `json:"model"`
				AspectRatio string `json:"aspect_ratio"`
				NumImages   int    `json:"num_images"`
				Seed        *int   `json:"seed"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if strings.TrimSpace(params.Prompt) == "" {
				return nil, fmt.Errorf("prompt is required")
			}

			apiKey := os.Getenv("FAL_KEY")
			if apiKey == "" {
				return nil, fmt.Errorf("FAL_KEY environment variable is not set. Get an API key at https://fal.ai")
			}

			if params.Model == "" {
				params.Model = "flux-schnell"
			}
			if params.AspectRatio == "" {
				params.AspectRatio = "1:1"
			}
			if params.NumImages <= 0 {
				params.NumImages = 1
			}
			if params.NumImages > 4 {
				params.NumImages = 4
			}

			modelPath, ok := falModels[params.Model]
			if !ok {
				return nil, fmt.Errorf("unknown model %q. Available: flux-schnell, flux-dev, flux-pro, flux-realism, sdxl", params.Model)
			}

			return callFAL(ctx, apiKey, modelPath, params.Prompt, params.AspectRatio, params.NumImages, params.Seed)
		},
	}
}

func callFAL(ctx context.Context, apiKey, modelPath, prompt, aspectRatio string, numImages int, seed *int) (any, error) {
	url := fmt.Sprintf("https://queue.fal.run/%s", modelPath)

	payload := map[string]any{
		"prompt":       prompt,
		"aspect_ratio": aspectRatio,
		"num_images":   numImages,
	}
	if seed != nil {
		payload["seed"] = *seed
	}

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

	// Poll for result
	statusURL := fmt.Sprintf("https://queue.fal.run/%s/requests/%s/status", modelPath, queueResp.RequestID)
	resultURL := fmt.Sprintf("https://queue.fal.run/%s/requests/%s", modelPath, queueResp.RequestID)

	for i := 0; i < 60; i++ { // max 60 polls = ~60s
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
			// Fetch result
			resultReq, _ := http.NewRequestWithContext(ctx, "GET", resultURL, nil)
			resultReq.Header.Set("Authorization", "Key "+apiKey)
			resultResp, err := client.Do(resultReq)
			if err != nil {
				return nil, fmt.Errorf("fetch result: %w", err)
			}
			defer resultResp.Body.Close()
			resultBody, _ := io.ReadAll(resultResp.Body)

			var result struct {
				Images []struct {
					URL    string `json:"url"`
					Width  int    `json:"width"`
					Height int    `json:"height"`
				} `json:"images"`
			}
			if err := json.Unmarshal(resultBody, &result); err != nil {
				return nil, fmt.Errorf("parse result: %w (body: %s)", err, string(resultBody))
			}

			if len(result.Images) == 0 {
				return nil, fmt.Errorf("no images in response: %s", string(resultBody))
			}

			// Download images to local files
			tmpDir := filepath.Join(os.TempDir(), "covo-images")
			_ = os.MkdirAll(tmpDir, 0755)

			var saved []map[string]any
			for i, img := range result.Images {
				localPath, err := downloadImage(ctx, client, img.URL, tmpDir)
				if err != nil {
					saved = append(saved, map[string]any{
						"url":   img.URL,
						"error": err.Error(),
					})
					continue
				}
				saved = append(saved, map[string]any{
					"path":   localPath,
					"url":    img.URL,
					"width":  img.Width,
					"height": img.Height,
					"index":  i,
				})
			}

			return map[string]any{
				"status": "generated",
				"model":  modelPath,
				"images": saved,
			}, nil
		}

		if status.Status == "FAILED" || status.Status == "failed" {
			return nil, fmt.Errorf("image generation failed: %s", string(statusBody))
		}
	}

	return nil, fmt.Errorf("timeout waiting for image generation (60s)")
}

func downloadImage(ctx context.Context, client *http.Client, url, dir string) (string, error) {
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

	// Determine extension from content type
	ext := ".png"
	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"):
		ext = ".jpg"
	case strings.Contains(ct, "webp"):
		ext = ".webp"
	}

	outFile, err := os.CreateTemp(dir, "img-*"+ext)
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
