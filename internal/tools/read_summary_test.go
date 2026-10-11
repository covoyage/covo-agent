package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/tools/capability"
	"github.com/covoyage/covonaut/agentcore"
	frameworktools "github.com/covoyage/covonaut/tools"
)

func TestReadSummaryUsesActualTruncationDetails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample"), []byte("first\nsecond\nthird"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, args, complete, reason string
		lines                        int64
	}{
		{"full", `{"path":"sample"}`, "yes", "", 2000},
		{"window", `{"path":"sample","offset":2,"limit":1}`, "window", "", 2000},
		{"capped", `{"path":"sample"}`, "no", "lines", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			core := agentcore.New(agentcore.Config{})
			defer core.Close()
			tool := frameworktools.NewReadTool(dir, &frameworktools.ReadToolConfig{MaxLines: test.lines})
			core.RegisterTools(tool)
			ApplyToolContracts(core)
			ApplyReadSummaries(core)
			ApplyReadSummaries(core)
			result, err := tool.Func(context.Background(), json.RawMessage(test.args))
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(result)
			summary := ReadSummaryFromResult(string(data))
			if summary == nil || summary.Complete != test.complete || summary.LimitReason != test.reason {
				t.Fatalf("summary=%+v result=%s", summary, data)
			}
			if !strings.Contains(string(data), "content") {
				t.Fatal("original result lost")
			}
		})
	}
}

func TestReadSummaryDoesNotCertifyUnobservedSource(t *testing.T) {
	core := agentcore.New(agentcore.Config{})
	defer core.Close()
	tool := &agentcore.Tool{Name: "custom_read", Func: func(context.Context, json.RawMessage) (any, error) {
		return map[string]any{"content": "complete-looking text"}, nil
	}}
	capability.Declare(tool, capability.Metadata{Kind: capability.Read, ReadParam: "target"})
	core.RegisterTools(tool)
	ApplyReadSummaries(core)
	result, err := tool.Func(context.Background(), json.RawMessage(`{"target":"file"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	summary := ReadSummaryFromResult(string(data))
	if summary == nil || summary.Complete != "unknown" || summary.Truncated != nil {
		t.Fatal("unobserved source certified complete")
	}
}
