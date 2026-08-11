package media

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

//go:embed scripts/voice_listen.py
var voiceListenScript []byte

//go:embed scripts/voice_record.py
var voiceRecordScript []byte

func BuildVoiceListenTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "voice_listen",
		Description: strings.Join([]string{
			"Start continuous voice listening with wake word detection.",
			"Runs in the background, detecting a wake word (default: 'hey covo').",
			"When the wake word is detected, it records audio for a fixed duration",
			"and transcribes it using the whisper backend.",
			"",
			"Returns the transcription result. The process runs until cancelled",
			"or until max_duration seconds elapse.",
			"",
			"Requires Python 3 with: pip install openwakeword sounddevice numpy",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"wake_word": map[string]any{
					"type":        "string",
					"description": "Wake word phrase (default: 'hey covo').",
				},
				"record_seconds": map[string]any{
					"type":        "number",
					"description": "Seconds to record after wake word detection (default: 10).",
				},
				"max_duration": map[string]any{
					"type":        "number",
					"description": "Maximum total listening duration in seconds (default: 300).",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				WakeWord    string `json:"wake_word"`
				RecordSec   int    `json:"record_seconds"`
				MaxDuration int    `json:"max_duration"`
			}
			json.Unmarshal(args, &params)
			if params.WakeWord == "" {
				params.WakeWord = "hey covo"
			}
			if params.RecordSec <= 0 {
				params.RecordSec = 10
			}
			if params.MaxDuration <= 0 {
				params.MaxDuration = 300
			}

			tmpDir := filepath.Join(os.TempDir(), "covo-voice")
			os.MkdirAll(tmpDir, 0o755)
			scriptPath := filepath.Join(tmpDir, "voice_listen.py")
			if err := os.WriteFile(scriptPath, voiceListenScript, 0o644); err != nil {
				return nil, fmt.Errorf("write voice script: %w", err)
			}
			defer os.Remove(scriptPath)

			cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(params.MaxDuration+30)*time.Second)
			defer cancel()

			cmd := exec.CommandContext(cmdCtx, "python3", scriptPath,
				params.WakeWord,
				fmt.Sprintf("%d", params.RecordSec),
				fmt.Sprintf("%d", params.MaxDuration),
			)
			cmd.Stderr = os.Stderr
			output, err := cmd.Output()
			if err != nil {
				return nil, fmt.Errorf("voice listen failed: %w", err)
			}

			lines := strings.Split(string(output), "\n")
			var recordedFile string
			for _, line := range lines {
				if strings.HasPrefix(line, "RECORDED:") {
					recordedFile = strings.TrimPrefix(line, "RECORDED:")
				}
			}

			if recordedFile == "" {
				return map[string]any{
					"status":  "no_voice",
					"message": "No voice detected within the listening duration",
				}, nil
			}

			transcript, err := TranscribeFile(ctx, recordedFile, "", "")
			os.Remove(recordedFile)
			if err != nil {
				return nil, fmt.Errorf("transcribe recorded audio: %w", err)
			}

			return map[string]any{
				"status":        "transcribed",
				"transcription": strings.TrimSpace(transcript),
			}, nil
		},
	}
}

func BuildVoiceRecordTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "voice_record",
		Description: strings.Join([]string{
			"Push-to-talk: record audio from the microphone for a fixed duration",
			"and transcribe it. Use this when the user wants to speak a message.",
			"",
			"Records for the specified duration, then transcribes via whisper.",
			"Returns the transcribed text.",
			"",
			"Requires Python 3 with: pip install sounddevice numpy",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"duration": map[string]any{
					"type":        "number",
					"description": "Recording duration in seconds (default: 15).",
				},
				"language": map[string]any{
					"type":        "string",
					"description": "Language hint for transcription (e.g. 'en', 'zh').",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Duration int    `json:"duration"`
				Language string `json:"language"`
			}
			json.Unmarshal(args, &params)
			if params.Duration <= 0 {
				params.Duration = 15
			}

			tmpDir := filepath.Join(os.TempDir(), "covo-voice")
			os.MkdirAll(tmpDir, 0o755)
			scriptPath := filepath.Join(tmpDir, "voice_record.py")
			if err := os.WriteFile(scriptPath, voiceRecordScript, 0o644); err != nil {
				return nil, fmt.Errorf("write record script: %w", err)
			}
			defer os.Remove(scriptPath)

			cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(params.Duration+10)*time.Second)
			defer cancel()

			cmd := exec.CommandContext(cmdCtx, "python3", scriptPath,
				fmt.Sprintf("%d", params.Duration),
				params.Language,
			)
			cmd.Stderr = os.Stderr
			output, err := cmd.Output()
			if err != nil {
				return nil, fmt.Errorf("voice record failed: %w", err)
			}

			lines := strings.Split(string(output), "\n")
			var recordedFile string
			for _, line := range lines {
				if strings.HasPrefix(line, "RECORDED:") {
					recordedFile = strings.TrimPrefix(line, "RECORDED:")
				}
				if strings.HasPrefix(line, "SILENCE:") {
					return map[string]any{
						"status":  "silence",
						"message": "No audible speech detected during recording",
					}, nil
				}
			}

			if recordedFile == "" {
				return map[string]any{
					"status":  "error",
					"message": "Recording failed to produce output",
				}, nil
			}

			transcript, err := TranscribeFile(ctx, recordedFile, params.Language, "")
			os.Remove(recordedFile)
			if err != nil {
				return nil, fmt.Errorf("transcribe: %w", err)
			}

			return map[string]any{
				"status":        "transcribed",
				"transcription": strings.TrimSpace(transcript),
			}, nil
		},
	}
}
