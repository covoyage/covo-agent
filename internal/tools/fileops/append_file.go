package fileops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

func BuildAppendFileTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "append_file",
		Description: strings.Join([]string{
			"Append content to a file without overwriting existing content.",
			"Use this instead of 'write' when you need to add lines to a file",
			"(e.g. appending to logs, adding entries to config, building files incrementally).",
			"Creates the file if it doesn't exist.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "Absolute path to the file to append to.",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Content to append to the file.",
				},
			},
			"required": []string{"file_path", "content"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				FilePath string `json:"file_path"`
				Content  string `json:"content"`
			}
			json.Unmarshal(args, &params)

			if strings.TrimSpace(params.FilePath) == "" {
				return nil, fmt.Errorf("file_path is required")
			}
			params.FilePath = resolveReadPath(params.FilePath, "")

			dir := filepath.Dir(params.FilePath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("create directory: %w", err)
			}

			f, err := os.OpenFile(params.FilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return nil, fmt.Errorf("open file: %w", err)
			}
			defer f.Close()

			n, err := f.WriteString(params.Content)
			if err != nil {
				return nil, fmt.Errorf("append: %w", err)
			}
			notifyAfterWrite(params.FilePath, "append_file", "")

			return &agentcore.DualToolOutput{
				ForLLM:  fmt.Sprintf("Appended %d bytes to %s", n, params.FilePath),
				ForUser: fmt.Sprintf("➕ Appended %d bytes to %s", n, filepath.Base(params.FilePath)),
				Silent:  true,
			}, nil
		},
	}
}
