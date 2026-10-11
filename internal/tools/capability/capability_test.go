package capability

import (
	"encoding/json"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestRenamedMutationRetainsTargetsAndBarrier(t *testing.T) {
	tool := &agentcore.Tool{Name: "custom_save"}
	Declare(tool, Metadata{Kind: Write, WriteParams: []string{"target"}})
	metadata, ok := Lookup(tool)
	if !ok || metadata.Kind != Write || tool.ConcurrencySafe == nil || *tool.ConcurrencySafe {
		t.Fatal("mutation capability lost")
	}
	paths, err := metadata.Paths(json.RawMessage(`{"target":"outside.txt"}`))
	if err != nil || len(paths) != 1 || paths[0] != "outside.txt" {
		t.Fatalf("targets=%v error=%v", paths, err)
	}
	if _, err := metadata.Paths(json.RawMessage(`{"path":"unrelated"}`)); err == nil {
		t.Fatal("missing declared target accepted")
	}
}
