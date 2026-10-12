package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/covoyage/covo-agent/internal/cyber"
	"github.com/covoyage/covo-agent/internal/tools/capability"
	"github.com/covoyage/covonaut/agentcore"
)

func BuildCyberStorageTool(catalog *cyber.Catalog, role string) *agentcore.Tool {
	tool := &agentcore.Tool{Name: "cyber_storage", Description: "Preview old Cyber output/report cleanup. Apply requires the unchanged preview revision; referenced evidence and active work are protected.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"retention_days": map[string]any{"type": "integer", "minimum": 1}, "apply": map[string]any{"type": "boolean"}, "revision": map[string]any{"type": "string"}}}}
	tool.Func = func(_ context.Context, args json.RawMessage) (any, error) {
		var params struct {
			Days     int    `json:"retention_days"`
			Apply    bool   `json:"apply"`
			Revision string `json:"revision"`
		}
		decoder := json.NewDecoder(bytes.NewReader(args))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&params); err != nil {
			return nil, err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return nil, fmt.Errorf("storage expects one JSON object")
		}
		if params.Days == 0 {
			params.Days = 30
		}
		if catalog == nil {
			return nil, fmt.Errorf("catalog unavailable")
		}
		if params.Apply && role != "parent" {
			return nil, fmt.Errorf("only the parent may clean storage")
		}
		return catalog.CleanStorage(params.Days, params.Revision, params.Apply)
	}
	capability.Declare(tool, capability.Metadata{Kind: capability.Execute})
	return tool
}
