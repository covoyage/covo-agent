package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

func TestMutationWrapperIsIdempotent(t *testing.T) {
	core := agentcore.New(agentcore.Config{})
	defer core.Close()
	tool := &agentcore.Tool{Name: "edit", Func: func(context.Context, json.RawMessage) (any, error) { return "ok", nil }}
	core.RegisterTools(tool)
	ApplyToolContracts(core)
	ApplyMutationLocks(core, t.TempDir())
	ApplyMutationLocks(core, t.TempDir())
	done := make(chan error, 1)
	go func() { _, err := tool.Func(context.Background(), json.RawMessage(`{"path":"sample"}`)); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("duplicate wrapper deadlocked")
	}
}

func TestLookupContractReadOnly(t *testing.T) {
	c, ok := LookupContract("read")
	if !ok || !c.ReadOnly || !c.ConcurrentSafe {
		t.Fatalf("read contract = %+v ok=%v", c, ok)
	}
	want := slices.Clone(c.RetrievalTerms)
	c.RetrievalTerms[0] = "caller mutation"
	current, _ := LookupContract("read")
	if !slices.Equal(current.RetrievalTerms, want) {
		t.Fatal("caller mutated the static retrieval declaration")
	}
	if ContractReadOnly("bash") {
		t.Fatal("bash must not be read-only")
	}
	c, ok = LookupContract("bash")
	if !ok || c.MaxOutputBytes != 32_000 || c.SideEffectScope != ScopeSystem {
		t.Fatalf("bash contract = %+v", c)
	}
}

func TestLookupContractUnknown(t *testing.T) {
	if _, ok := LookupContract("not_a_tool"); ok {
		t.Fatal("unknown tool should miss")
	}
}

func TestRetrievalTermsDoNotDeclarePermissions(t *testing.T) {
	core := agentcore.New(agentcore.Config{})
	defer core.Close()
	for _, name := range []string{"memory", "move", "human_handoff", "mcp__remote__memory"} {
		core.RegisterTools(&agentcore.Tool{Name: name})
	}
	ApplyToolContracts(core)
	for name, terms := range additionalToolRetrievalTerms {
		if _, declared := LookupContract(name); declared {
			t.Fatalf("retrieval hints declared a permission contract for %s", name)
		}
		tool, _ := core.GetTool(name)
		first := slices.Clone(tool.Tags)
		ApplyToolContracts(core)
		if !slices.Equal(first, tool.Tags) {
			t.Fatalf("retrieval tags were not idempotent for %s", name)
		}
		for _, term := range terms {
			if !slices.Contains(tool.Tags, term) {
				t.Fatalf("missing retrieval capability for %s", name)
			}
		}
	}
	remote, _ := core.GetTool("mcp__remote__memory")
	if len(remote.Tags) != 0 {
		t.Fatal("remote identity inherited native retrieval metadata")
	}
}

func TestApplyToolContractsRetrievalTerms(t *testing.T) {
	core := agentcore.New(agentcore.Config{})
	defer core.Close()
	for _, name := range []string{"read", "edit", "write_file", "delete", "search_files", "web_search", "mcp__remote__read", "custom_tool"} {
		tool := &agentcore.Tool{Name: name, Description: "Original description", Tags: []string{"original-tag"}, Parameters: map[string]any{"type": "object"}}
		core.RegisterTools(tool)
	}
	ApplyToolContracts(core)
	for _, name := range core.ToolNames() {
		tool, _ := core.GetTool(name)
		first := slices.Clone(tool.Tags)
		ApplyToolContracts(core)
		if !slices.Equal(first, tool.Tags) {
			t.Fatalf("repeated registration changed %s tags", name)
		}
		if !slices.Contains(tool.Tags, "original-tag") {
			t.Fatalf("lost caller metadata for %s", name)
		}
		if tool.Description != "Original description" || !reflect.DeepEqual(tool.Parameters, map[string]any{"type": "object"}) {
			t.Fatalf("retrieval annotation changed declaration for %s", name)
		}
		contract, known := LookupContract(name)
		if !known {
			if !slices.Equal(tool.Tags, []string{"original-tag"}) {
				t.Fatalf("unknown/remote tool inherited local hints: %s", name)
			}
			continue
		}
		if len(contract.RetrievalTerms) == 0 {
			t.Fatalf("missing retrieval terms: %s", name)
		}
		definition := agentcore.ToolDefinition{Name: name, Description: tool.Description, Parameters: tool.Parameters, SearchTerms: tool.Tags}
		encoded, err := json.Marshal(definition)
		if err != nil {
			t.Fatal(err)
		}
		for _, term := range contract.RetrievalTerms {
			if !slices.Contains(tool.Tags, term) {
				t.Fatalf("missing retrieval term %q for %s", term, name)
			}
			if strings.Contains(string(encoded), term) {
				t.Fatalf("retrieval-only term leaked into model declaration: %s", encoded)
			}
		}
	}
}
