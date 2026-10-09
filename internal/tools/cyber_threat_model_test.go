package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/covoyage/covo-agent/internal/cyber"
)

func TestThreatModelInfersAction(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	tool := BuildThreatModelTool(catalog)
	_, err := tool.Func(context.Background(), json.RawMessage(`{
		"target":"https://example.test",
		"overview":"public API",
		"attacker":"unauthenticated user",
		"trust_boundaries":["internet to API"],
		"attack_surface":["/api"],
		"severity_calibration":"tenant escape is high"
	}`))
	if err != nil {
		t.Fatalf("inferred save: %v", err)
	}
	model, ok, err := catalog.GetThreatModel("https://example.test")
	if err != nil || !ok || model.Attacker != "unauthenticated user" {
		t.Fatalf("model=%#v ok=%v err=%v", model, ok, err)
	}
	if _, err := tool.Func(context.Background(), json.RawMessage(`{"target":"https://example.test"}`)); err != nil {
		t.Fatalf("inferred get: %v", err)
	}
	if _, err := tool.Func(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Fatalf("inferred list: %v", err)
	}
}
