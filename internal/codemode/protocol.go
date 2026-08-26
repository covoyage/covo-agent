package codemode

import "encoding/json"

// ToolCallRequest is sent from the child process (code) to the parent.
type ToolCallRequest struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

// ToolCallResponse is sent from the parent to the child process.
type ToolCallResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}
