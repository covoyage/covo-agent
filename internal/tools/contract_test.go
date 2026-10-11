package tools

import (
	"context"
	"encoding/json"
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
