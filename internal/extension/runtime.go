package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// ToolCallRequest is the JSON structure sent to the extension binary via stdin.
type ToolCallRequest struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

// ToolCallResponse is the JSON structure the extension binary writes to stdout.
type ToolCallResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// ExecuteTool runs an extension tool as a subprocess.
// It finds the extension by name, runs the binary with the tool name as
// the first argument, and passes the args as JSON on stdin.
func (m *Manager) ExecuteTool(ctx context.Context, extName, toolName string, args json.RawMessage) (json.RawMessage, error) {
	ext := m.Get(extName)
	if ext == nil {
		return nil, fmt.Errorf("extension %q not found", extName)
	}
	if !ext.Enabled {
		return nil, fmt.Errorf("extension %q is disabled", extName)
	}
	if ext.BinaryPath == "" {
		return nil, fmt.Errorf("extension %q has no binary", extName)
	}

	req := ToolCallRequest{
		Tool: toolName,
		Args: args,
	}

	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	cmd := exec.CommandContext(ctx, ext.BinaryPath, toolName)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start extension %s: %w", extName, err)
	}

	_, _ = stdin.Write(reqData)
	stdin.Close()

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("extension %s failed: %s", extName, string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("extension %s: %w", extName, err)
	}

	var resp ToolCallResponse
	if err := json.Unmarshal(output, &resp); err != nil {
		return nil, fmt.Errorf("parse extension %s response: %w", extName, err)
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("extension %s: %s", extName, resp.Error)
	}

	return resp.Result, nil
}
