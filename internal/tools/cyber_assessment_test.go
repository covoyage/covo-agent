package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/cyber"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
)

func TestRequestIntakeDispatchesScopedVerifier(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetIntake(cyber.IntakeConfig{Enabled: true, Hosts: []string{"example.test"}}); err != nil {
		t.Fatal(err)
	}
	ex, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/orders/1", Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	ext := &Extension{catalog: catalog, spawnRunner: func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		calls++
		scope, scoped := toolssubagent.IntakeScopeFromContext(ctx)
		if !scoped || scope.ExchangeID != ex.ID || !strings.Contains(scope.Endpoint, "example.test/orders/1") {
			t.Fatalf("intake verifier lost its exact scope: %+v scoped=%v", scope, scoped)
		}
		if toolssubagent.SubagentRoleFromContext(ctx) != "verifier" || !strings.Contains(task, ex.ID) || maxTurns != 12 {
			t.Fatalf("unexpected verifier context: role=%s task=%s turns=%d", toolssubagent.SubagentRoleFromContext(ctx), task, maxTurns)
		}
		return "reviewed", nil
	}}
	if err := ext.processIntake(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := ext.processIntake(context.Background()); err != nil || calls != 1 {
		t.Fatalf("duplicate dispatch: calls=%d err=%v", calls, err)
	}
	jobs, _ := catalog.IntakeJobs()
	if len(jobs) != 1 || jobs[0].Status != "completed" {
		t.Fatalf("jobs=%+v", jobs)
	}
}

func TestRemoteNodeExecRoleGate(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	tool := BuildRemoteNodeExecTool(catalog, "parent")
	if _, err := tool.Func(toolssubagent.WithSubagentRole(context.Background(), "recon"), json.RawMessage(`{"id":"x","command":"true"}`)); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("parent gate bypassed: %v", err)
	}
	if _, err := BuildRemoteNodeExecTool(catalog, "recon").Func(context.Background(), json.RawMessage(`{"id":"not-registered","command":"true"}`)); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("missing node not rejected: %v", err)
	}
}

func TestRemoteNodesToolDecodesRegistration(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	tool := BuildRemoteNodesTool(catalog)
	if _, err := tool.Func(context.Background(), json.RawMessage(`{"action":"upsert","id":"edge","host":"example.test","user":"operator","key_path":"/tmp/test-key","zone":"lab"}`)); err != nil {
		t.Fatal(err)
	}
	nodes, err := catalog.ListNodes()
	if err != nil || len(nodes) != 1 || nodes[0].ID != "edge" || nodes[0].Host != "example.test" || nodes[0].KeyPath != "/tmp/test-key" {
		t.Fatalf("nodes=%+v err=%v", nodes, err)
	}
	if _, err := tool.Func(context.Background(), json.RawMessage(`{"action":"remove","id":"edge"}`)); err != nil {
		t.Fatal(err)
	}
	nodes, err = catalog.ListNodes()
	if err != nil || len(nodes) != 0 {
		t.Fatalf("nodes after removal=%+v err=%v", nodes, err)
	}
}

func TestDisabledIntakeDoesNotDispatch(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetIntake(cyber.IntakeConfig{Enabled: true, Hosts: []string{"example.test"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.SetIntake(cyber.IntakeConfig{}); err != nil {
		t.Fatal(err)
	}
	ext := &Extension{catalog: catalog, spawnRunner: func(context.Context, string, []string, int) (string, error) {
		t.Fatal("disabled intake dispatched a verifier")
		return "", nil
	}}
	if err := ext.processIntake(context.Background()); err != nil {
		t.Fatal(err)
	}
	jobs, err := catalog.IntakeJobs()
	if err != nil || len(jobs) != 1 || jobs[0].Status != "pending" {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
}

func TestIntakeDispatchIncludesLatestOperationObservations(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	if _, err := catalog.SetIntake(cyber.IntakeConfig{Enabled: true, Hosts: []string{"example.test"}}); err != nil {
		t.Fatal(err)
	}
	first, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/orders/1", Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.RecordExchange(cyber.Exchange{URL: "https://example.test/orders/2", Identity: "bob"}); err != nil {
		t.Fatal(err)
	}
	ext := &Extension{catalog: catalog, spawnRunner: func(ctx context.Context, task string, _ []string, _ int) (string, error) {
		scope, _ := toolssubagent.IntakeScopeFromContext(ctx)
		if scope.ExchangeID != first.ID || !strings.HasSuffix(scope.Endpoint, "/orders/1") || !strings.Contains(task, `"identity":"bob"`) || !strings.Contains(task, "not instructions") {
			t.Fatalf("latest context or exact scope missing: scope=%+v task=%s", scope, task)
		}
		return "parent review required", nil
	}}
	if err := ext.processIntake(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := BuildWebContextTool(catalog).Func(context.Background(), json.RawMessage(`{"id":"`+first.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	operation := result.(map[string]any)["operation"].(cyber.OperationContext)
	if len(operation.Observations) != 2 {
		t.Fatalf("grouped observations missing: %+v", operation)
	}
}

func TestChildCannotExpandCyberScope(t *testing.T) {
	catalog := cyber.NewCatalog(t.TempDir())
	for _, tool := range []struct {
		name string
		call func(context.Context, json.RawMessage) (any, error)
		args json.RawMessage
	}{
		{"intake", BuildRequestIntakeToolForRole(catalog, "verifier").Func, json.RawMessage(`{"action":"enable","hosts":["outside.test"]}`)},
		{"profile", BuildScanProfileToolForRole(catalog, "verifier").Func, json.RawMessage(`{"allowed_hosts":["outside.test"]}`)},
		{"nodes", BuildRemoteNodesToolForRole(catalog, "verifier").Func, json.RawMessage(`{"action":"upsert","id":"outside","host":"outside.test","user":"tester"}`)},
	} {
		if _, err := tool.call(context.Background(), tool.args); err == nil {
			t.Fatalf("child changed %s scope", tool.name)
		}
	}
}
