package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

func BuildTranscribeTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "transcribe",
		Description: strings.Join([]string{
			"Transcribe speech from audio or video files to text.",
			"Supports MP3, WAV, M4A, OGG, FLAC, MP4, WEBM, and more.",
			"",
			"Backends (auto-detected in order):",
			"  1. whisper-cli (local, via whisper.cpp) - fastest, offline",
			"  2. whisper (python openai-whisper) - local, more accurate",
			"  3. OpenAI Whisper API - cloud, requires OPENAI_API_KEY",
			"",
			"Options:",
			"  - language: ISO 639-1 code (e.g. 'en', 'zh', 'ja'). Auto-detect if empty.",
			"  - model: whisper model size. For local: tiny/base/small/medium/large.",
			"    For API: whisper-1. Default: small (local), whisper-1 (API).",
			"  - format: Output format: 'text' (plain), 'srt' (subtitles), 'json' (verbose). Default: text.",
			"  - prompt: Optional context text to guide transcription style/spelling.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file": map[string]any{
					"type":        "string",
					"description": "Path to the audio or video file to transcribe.",
				},
				"language": map[string]any{
					"type":        "string",
					"description": "Language code (e.g. 'en', 'zh', 'ja'). Auto-detect if empty.",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Model: tiny/base/small/medium/large (local) or whisper-1 (API). Default: small.",
				},
				"format": map[string]any{
					"type":        "string",
					"description": "Output format: 'text', 'srt', 'json'. Default: text.",
					"enum":        []string{"text", "srt", "json"},
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "Optional context text to guide transcription.",
				},
			},
			"required": []string{"file"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				File     string `json:"file"`
				Language string `json:"language"`
				Model    string `json:"model"`
				Format   string `json:"format"`
				Prompt   string `json:"prompt"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(params.File) == "" {
				return nil, fmt.Errorf("file path is required")
			}
			if params.Format == "" {
				params.Format = "text"
			}
			if params.Model == "" {
				params.Model = "small"
			}

			absPath, err := filepath.Abs(params.File)
			if err != nil {
				return nil, fmt.Errorf("resolve path: %w", err)
			}
			if _, err := os.Stat(absPath); err != nil {
				return nil, fmt.Errorf("file not found: %s", absPath)
			}

			backend := detectTranscribeBackend()
			switch backend {
			case "whisper-cli":
				return transcribeWhisperCLI(ctx, absPath, params.Language, params.Model, params.Format)
			case "whisper":
				return transcribeWhisperPy(ctx, absPath, params.Language, params.Model, params.Format)
			case "api":
				return transcribeOpenAI(ctx, absPath, params.Language, params.Model, params.Format, params.Prompt)
			default:
				return nil, fmt.Errorf("no transcription backend available. Install whisper.cpp, openai-whisper, or set OPENAI_API_KEY")
			}
		},
	}
}

// TranscribeFile transcribes an audio/video file at path to plain text,
// auto-detecting the best available backend (whisper-cli, local whisper,
// or the OpenAI API). It is exported so callers outside the tool-call
// pipeline (e.g. the gateway's automatic voice-message handling) can reuse
// the same transcription logic without going through the agentcore.Tool
// JSON-argument interface.
func TranscribeFile(ctx context.Context, path, language, model string) (string, error) {
	if model == "" {
		model = "small"
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	if _, err := os.Stat(absPath); err != nil {
		return "", fmt.Errorf("file not found: %s", absPath)
	}

	backend := detectTranscribeBackend()
	var result any
	switch backend {
	case "whisper-cli":
		result, err = transcribeWhisperCLI(ctx, absPath, language, model, "text")
	case "whisper":
		result, err = transcribeWhisperPy(ctx, absPath, language, model, "text")
	case "api":
		result, err = transcribeOpenAI(ctx, absPath, language, model, "text", "")
	default:
		return "", fmt.Errorf("no transcription backend available. Install whisper.cpp, openai-whisper, or set OPENAI_API_KEY")
	}
	if err != nil {
		return "", err
	}

	if m, ok := result.(map[string]any); ok {
		if text, ok := m["text"].(string); ok {
			return text, nil
		}
	}
	return "", fmt.Errorf("transcription produced no text")
}

func detectTranscribeBackend() string {
	if _, err := exec.LookPath("whisper-cli"); err == nil {
		return "whisper-cli"
	}
	if _, err := exec.LookPath("whisper"); err == nil {
		return "whisper"
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		return "api"
	}
	return ""
}

func transcribeWhisperCLI(ctx context.Context, path, language, model, format string) (any, error) {
	args := []string{"-f", path, "-m", modelPath(model), "-otxt"}
	if language != "" {
		args = append(args, "-l", language)
	}
	if format == "srt" {
		args = append(args, "-osrt")
	}
	if format == "json" {
		args = append(args, "-oj")
	}

	cmd := exec.CommandContext(ctx, "whisper-cli", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("whisper-cli failed: %s", stderr.String())
	}

	output := stdout.String()
	if format == "json" {
		var result map[string]any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			return map[string]any{
				"backend": "whisper-cli",
				"text":    output,
			}, nil
		}
		result["backend"] = "whisper-cli"
		return result, nil
	}

	return map[string]any{
		"backend": "whisper-cli",
		"text":    strings.TrimSpace(output),
		"model":   model,
	}, nil
}

func transcribeWhisperPy(ctx context.Context, path, language, model, format string) (any, error) {
	args := []string{"-m", "whisper", path, "--model", model}
	if language != "" {
		args = append(args, "--language", language)
	}
	switch format {
	case "srt":
		args = append(args, "--output_format", "srt")
	case "json":
		args = append(args, "--output_format", "json")
	default:
		args = append(args, "--output_format", "txt")
	}
	args = append(args, "--output_dir", os.TempDir())

	cmd := exec.CommandContext(ctx, "python3", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("whisper failed: %s", stderr.String())
	}

	output := stdout.String()
	if output == "" {
		output = stderr.String()
	}

	return map[string]any{
		"backend": "whisper",
		"text":    strings.TrimSpace(output),
		"model":   model,
	}, nil
}

func transcribeOpenAI(ctx context.Context, path, language, model, format, prompt string) (any, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY not set")
	}
	if model == "small" || model == "" {
		model = "whisper-1"
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	writer.WriteField("model", model)
	if language != "" {
		writer.WriteField("language", language)
	}
	if prompt != "" {
		writer.WriteField("prompt", prompt)
	}

	switch format {
	case "srt":
		writer.WriteField("response_format", "srt")
	case "json":
		writer.WriteField("response_format", "verbose_json")
	default:
		writer.WriteField("response_format", "text")
	}

	part, err := writer.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, fmt.Errorf("copy file: %w", err)
	}
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/audio/transcriptions", body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api request: %w", err)
	}
	defer resp.Body.Close()

	respBody := new(bytes.Buffer)
	respBody.ReadFrom(resp.Body)
	respText := respBody.String()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("OpenAI API error (status %d): %s", resp.StatusCode, respText)
	}

	if format == "json" {
		var result map[string]any
		if err := json.Unmarshal([]byte(respText), &result); err == nil {
			result["backend"] = "openai"
			return result, nil
		}
	}

	return map[string]any{
		"backend": "openai",
		"text":    strings.TrimSpace(respText),
		"model":   model,
	}, nil
}

func modelPath(model string) string {
	home, _ := os.UserHomeDir()
	modelsDir := filepath.Join(home, ".cache", "whisper")
	modelFile := fmt.Sprintf("ggml-%s.bin", model)
	if model == "large" || model == "large-v3" {
		modelFile = fmt.Sprintf("ggml-%s.bin", model)
	} else if model == "turbo" {
		modelFile = "ggml-large-v3-turbo.bin"
	}
	return filepath.Join(modelsDir, modelFile)
}
