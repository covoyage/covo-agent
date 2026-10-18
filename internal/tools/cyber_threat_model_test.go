package tools

import (
	"context"
	"encoding/json"
	"reflect"
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

func TestThreatModelMissingFieldsDoesNotSave(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	tool := BuildThreatModelTool(catalog)
	for _, args := range []string{
		`{"action":"save","target":"app.test"}`,
		`{"target":"app.test","overview":" ","trust_boundaries":[" "],"attack_surface":["\t"]}`,
	} {
		result, err := tool.Func(context.Background(), json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		response := result.(map[string]any)
		expected := []string{"overview", "attacker", "trust_boundaries", "attack_surface", "severity_calibration"}
		if response["status"] != "validation_required" || response["saved"] != false || !reflect.DeepEqual(response["missing_fields"], expected) {
			t.Fatalf("missing explicit unsaved validation: %+v", response)
		}
		if _, found, err := catalog.GetThreatModel("app.test"); err != nil || found {
			t.Fatalf("incomplete model was saved: found=%v error=%v", found, err)
		}
	}
}

func TestThreatModelInvalidUpdatePreservesModel(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	tool := BuildThreatModelTool(catalog)
	original := cyber.ThreatModel{Target: "app.test", Overview: "public API", Attacker: "anonymous user", TrustBoundaries: []string{"client to API"}, AttackSurface: []string{"/api"}, SeverityCalibration: "observed tenant boundary controls"}
	saved, err := catalog.SaveThreatModel(original)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Func(context.Background(), json.RawMessage(`{"action":"save","target":"app.test","overview":"incomplete update"}`))
	if err != nil || result.(map[string]any)["saved"] != false {
		t.Fatalf("invalid update was not rejected: %+v, %v", result, err)
	}
	model, found, err := catalog.GetThreatModel("app.test")
	if err != nil || !found || !reflect.DeepEqual(model, saved) {
		t.Fatalf("invalid update overwrote model: %+v, %v", model, err)
	}
}
