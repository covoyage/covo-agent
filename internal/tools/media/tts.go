package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

func BuildTtsTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "tts",
		Description: strings.Join([]string{
			"Convert text to speech and save as an audio file. Uses Edge TTS (free, no API key).",
			"Supports 300+ voices across 70+ languages.",
			"",
			"The audio file is saved to a temp directory and the path is returned.",
			"Requires the edge-tts Python package. Install it explicitly with: pip install edge-tts.",
			"",
			"Popular voices:",
			"  en-US: en-US-AriaNeural (female), en-US-GuyNeural (male), en-US-JennyNeural (female)",
			"  en-GB: en-GB-SoniaNeural (female), en-GB-RyanNeural (male)",
			"  zh-CN: zh-CN-XiaoxiaoNeural (female), zh-CN-YunxiNeural (male)",
			"  ja-JP: ja-JP-NanamiNeural (female)",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "Text to convert to speech. Max 10,000 characters.",
				},
				"voice": map[string]any{
					"type":        "string",
					"description": "Voice name (default: en-US-AriaNeural). Use 'list_voices' action to see all.",
				},
				"rate": map[string]any{
					"type":        "string",
					"description": "Speech rate adjustment. E.g. '+20%', '-10%', '+0Hz'. Default: '+0%'.",
				},
				"volume": map[string]any{
					"type":        "string",
					"description": "Volume adjustment. E.g. '+50%', '-20%'. Default: '+0%'.",
				},
				"action": map[string]any{
					"type":        "string",
					"description": "Action: 'generate' (default) or 'list_voices'.",
					"enum":        []string{"generate", "list_voices"},
				},
				"language": map[string]any{
					"type":        "string",
					"description": "Filter voices by language code (for list_voices). E.g. 'en', 'zh', 'ja'.",
				},
			},
			"required": []string{},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Text     string `json:"text"`
				Voice    string `json:"voice"`
				Rate     string `json:"rate"`
				Volume   string `json:"volume"`
				Action   string `json:"action"`
				Language string `json:"language"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if params.Action == "" {
				params.Action = "generate"
			}

			switch params.Action {
			case "list_voices":
				return listVoices(ctx, params.Language)
			case "generate":
				return generateSpeech(ctx, params.Text, params.Voice, params.Rate, params.Volume)
			default:
				return nil, fmt.Errorf("unknown action %q", params.Action)
			}
		},
	}
}

func ensureEdgeTTS(ctx context.Context) error {
	// Check if edge-tts is available
	if _, err := exec.LookPath("edge-tts"); err == nil {
		return nil
	}
	// Try python -m edge_tts
	cmd := exec.CommandContext(ctx, "python3", "-m", "edge_tts", "--version")
	if cmd.Run() == nil {
		return nil
	}
	return fmt.Errorf("edge-tts is not installed. Install it explicitly with: pip install edge-tts")
}

func generateSpeech(ctx context.Context, text, voice, rate, volume string) (any, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("text is required")
	}
	if len(text) > 10000 {
		text = text[:10000]
	}

	if err := ensureEdgeTTS(ctx); err != nil {
		return nil, err
	}

	if voice == "" {
		voice = "en-US-AriaNeural"
	}

	// Create temp file
	tmpDir := filepath.Join(os.TempDir(), "covo-tts")
	_ = os.MkdirAll(tmpDir, 0755)
	outFile, err := os.CreateTemp(tmpDir, "tts-*.mp3")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	outPath := outFile.Name()
	outFile.Close()

	args := []string{"-m", "edge_tts", "--voice", voice, "--text", text, "--write-media", outPath}
	if rate != "" {
		args = append(args, "--rate", rate)
	}
	if volume != "" {
		args = append(args, "--volume", volume)
	}

	cmd := exec.CommandContext(ctx, "python3", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(outPath)
		return nil, fmt.Errorf("edge-tts failed: %s", stderr.String())
	}

	info, _ := os.Stat(outPath)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}

	return map[string]any{
		"status":  "generated",
		"path":    outPath,
		"voice":   voice,
		"size_kb": size / 1024,
	}, nil
}

func listVoices(ctx context.Context, language string) (any, error) {
	if err := ensureEdgeTTS(ctx); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, "python3", "-m", "edge_tts", "--list-voices")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("list voices failed: %s", stderr.String())
	}

	lines := strings.Split(stdout.String(), "\n")
	var voices []map[string]string
	var currentVoice map[string]string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Name: ") {
			if currentVoice != nil {
				voices = append(voices, currentVoice)
			}
			currentVoice = map[string]string{
				"name": strings.TrimPrefix(line, "Name: "),
			}
		} else if currentVoice != nil && strings.HasPrefix(line, "Gender: ") {
			currentVoice["gender"] = strings.TrimPrefix(line, "Gender: ")
		} else if currentVoice != nil && strings.HasPrefix(line, "Locale: ") {
			currentVoice["locale"] = strings.TrimPrefix(line, "Locale: ")
		}
	}
	if currentVoice != nil {
		voices = append(voices, currentVoice)
	}

	// Filter by language if specified
	if language != "" {
		var filtered []map[string]string
		langLower := strings.ToLower(language)
		for _, v := range voices {
			if strings.Contains(strings.ToLower(v["locale"]), langLower) {
				filtered = append(filtered, v)
			}
		}
		voices = filtered
	}

	return map[string]any{
		"count":  len(voices),
		"voices": voices,
	}, nil
}
