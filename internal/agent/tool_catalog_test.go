package agent

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/toolset"
	"github.com/covoyage/covonaut/agentcore"
)

// updateToolCatalog regenerates docs/tool-catalog.md from the live registry
// when true: go test ./internal/agent -run TestToolCatalog -update-tool-catalog
var updateToolCatalog = flag.Bool("update-tool-catalog", false, "regenerate docs/tool-catalog.md from the registered tool set")

// catalogNameRe extracts tool names from catalog table rows like "| `read` | ...".
var catalogNameRe = regexp.MustCompile(`(?m)^\| \` + "`" + `([a-z_0-9]+)` + "`")

// catalogRelPath is the documented tool catalog, relative to this package.
const catalogRelPath = "../../docs/tool-catalog.md"

// buildFullRegistryAgent constructs a throwaway agent with the full tool
// profile so tests can inspect the real registry contents.
func buildFullRegistryAgent(t testing.TB) *CovoAgent {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelError)}))
	ca, err := NewCovoAgent(CovoAgentConfig{
		Mode:         ModeGeneral,
		Provider:     &streamMockProvider{},
		ProviderName: "mock",
		Model:        "mock-model",
		WorkingDir:   t.TempDir(),
		HomeDir:      t.TempDir(),
		Logger:       logger,
		ToolProfile:  "full",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { ca.Close() })
	return ca
}

func TestToolDiscoveryUsesActiveAgentPolicy(t *testing.T) {
	ca := buildFullRegistryAgent(t)
	const target = "discovery_policy_fixture"
	called := false
	ca.core.RegisterTools(&agentcore.Tool{Name: target, Description: "Policy fixture", Func: func(context.Context, json.RawMessage) (any, error) {
		called = true
		return "unexpected", nil
	}})
	filter := ca.ToolsetFilter()
	filter.PlatformRef().SetOverride("cli", []string{"read", "tool_search", "tool_describe", "tool_call"})
	filter.InvalidateTools()
	query, _ := json.Marshal(map[string]any{"query": "select:" + target})
	search := func(want int) {
		t.Helper()
		text, err := ca.core.InvokeTool(context.Background(), "tool_search", query)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Matches []json.RawMessage `json:"matches"`
		}
		if err := json.Unmarshal([]byte(text), &result); err != nil || len(result.Matches) != want {
			t.Fatalf("want %d results: %s, %v", want, text, err)
		}
	}
	search(0)
	filter.PlatformRef().SetOverride("cli", []string{"read", "tool_search", "tool_describe", "tool_call", target})
	filter.InvalidateTools()
	search(1)
	filter.Availability().RegisterCheck(target, func() (string, bool) { return "not configured", false })
	filter.InvalidateAvailability()
	search(0)
	args, _ := json.Marshal(map[string]any{"name": target})
	if text, err := ca.core.InvokeTool(context.Background(), "tool_describe", args); err == nil {
		t.Fatalf("describe exposed unavailable schema: %s", text)
	}
	bridge, ok := ca.core.GetTool("tool_call")
	if !ok {
		t.Fatal("tool_call is not registered")
	}
	out, err := bridge.Func(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result := out.(map[string]any); result["status"] != "unavailable" || called {
		t.Fatalf("bridge bypassed availability: %#v, called=%v", result, called)
	}
}

func TestToolDiscoveryMCPHTTPPolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		if len(rpc.ID) == 0 {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch rpc.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "inventory", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "stock", "description": "Inspect inventory", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"sku": map[string]any{"type": "string"}}, "required": []string{"sku"}}}}}
		default:
			t.Errorf("discovery unexpectedly invoked MCP method %s", rpc.Method)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := json.NewEncoder(writer).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	ca := buildFullRegistryAgent(t)
	mcp, _ := ca.core.GetTool("mcp")
	connect, _ := json.Marshal(map[string]any{"action": "connect", "name": "inventory", "transport": "http", "endpoint": server.URL})
	if _, err := mcp.Func(context.Background(), connect); err != nil {
		t.Fatal(err)
	}
	filter := ca.ToolsetFilter()
	filter.PlatformRef().SetOverride("cli", []string{"mcp", "discovery"})
	filter.InvalidateTools()
	check := func(want int) {
		t.Helper()
		text, err := ca.core.InvokeTool(context.Background(), "tool_search", json.RawMessage(`{"query":"select:mcp__inventory__stock"}`))
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Matches []struct {
				Parameters map[string]any `json:"parameters"`
				Source     map[string]any `json:"source"`
			} `json:"matches"`
		}
		if err := json.Unmarshal([]byte(text), &result); err != nil || len(result.Matches) != want {
			t.Fatalf("want %d matches: %s, %v", want, text, err)
		}
		if want > 0 && (result.Matches[0].Parameters["required"] == nil || result.Matches[0].Source["server"] != "inventory") {
			t.Fatalf("remote schema/source missing: %s", text)
		}
	}
	check(1)
	filter.PlatformRef().SetOverride("cli", []string{"discovery"})
	filter.InvalidateTools()
	check(0)
	args := json.RawMessage(`{"name":"mcp__inventory__stock","arguments":{"sku":"ABC"}}`)
	if _, err := ca.core.InvokeTool(context.Background(), "tool_describe", args); err == nil {
		t.Fatal("describe exposed disabled MCP tool")
	}
	bridge, _ := ca.core.GetTool("tool_call")
	result, err := bridge.Func(context.Background(), args)
	if err != nil || result.(map[string]any)["status"] != "unavailable" {
		t.Fatalf("disabled MCP target was callable: %#v, %v", result, err)
	}
	filter.PlatformRef().SetOverride("cli", []string{"mcp", "discovery"})
	filter.InvalidateTools()
	check(1)
	filter.Availability().RegisterCheck("mcp__inventory__stock", func() (string, bool) { return "disabled", false })
	filter.InvalidateAvailability()
	check(0)
}

func discoveryCatalog(ca *CovoAgent) []agentcore.ToolDefinition {
	allowed := ca.ToolsetFilter().DiscoveryPreview(context.Background(), ca.core.ToolNames()).Kept
	var defs []agentcore.ToolDefinition
	for _, name := range allowed {
		if name == "tool_search" || name == "tool_describe" || name == "tool_call" || toolset.CyberOnlyTools[name] {
			continue
		}
		if tool, ok := ca.core.GetTool(name); ok {
			defs = append(defs, agentcore.ToolDefinition{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters})
		}
	}
	return defs
}

func legacyDiscovery(defs []agentcore.ToolDefinition, query string, limit int) []map[string]any {
	type candidate struct {
		definition agentcore.ToolDefinition
		score      int
	}
	var candidates []candidate
	for _, def := range defs {
		name, description := strings.ToLower(def.Name), strings.ToLower(def.Description)
		score := 0
		for _, term := range strings.Fields(strings.ToLower(query)) {
			if strings.Contains(name, term) {
				score += 10
			}
			if strings.Contains(description, term) {
				score += 2
			}
			if name == term {
				score += 50
			}
		}
		if score > 0 {
			candidates = append(candidates, candidate{def, score})
		}
	}
	for left := range candidates {
		for right := left + 1; right < len(candidates); right++ {
			if candidates[right].score > candidates[left].score {
				candidates[left], candidates[right] = candidates[right], candidates[left]
			}
		}
	}
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	matches := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		description := candidate.definition.Description
		if len(description) > 300 {
			description = description[:300] + "..."
		}
		matches = append(matches, map[string]any{"name": candidate.definition.Name, "description": description})
	}
	return matches
}

func TestToolSearchRealCatalogRetrieval(t *testing.T) {
	ca := buildFullRegistryAgent(t)
	ca.ToolsetFilter().PlatformRef().SetOverride("cli", []string{"full"})
	ca.ToolsetFilter().InvalidateTools()
	defs := discoveryCatalog(ca)
	search, ok := ca.core.GetTool("tool_search")
	if !ok {
		t.Fatal("tool_search is not registered")
	}
	cases := []struct{ query, expected string }{
		{"download_cancel", "browser"}, {"tab_switch", "browser"}, {"cancel download", "browser"},
		{"switch tab", "browser"}, {"read file", "read"}, {"search files", "search_files"},
		{"session_search", "session_search"}, {"git_status", "git_status"}, {"update_plan", "update_plan"}, {"mcp", "mcp"},
	}
	oldHits, newHits, oldBytes, newBytes, sharedOldBytes, sharedNewBytes := 0, 0, 0, 0, 0, 0
	oldMRR, newMRR := 0.0, 0.0
	for _, test := range cases {
		args, _ := json.Marshal(map[string]any{"query": test.query, "limit": 5})
		out, err := search.Func(context.Background(), args)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Matches []struct {
				Name string `json:"name"`
			} `json:"matches"`
		}
		if err := json.Unmarshal(encoded, &result); err != nil {
			t.Fatal(err)
		}
		old := legacyDiscovery(defs, test.query, 5)
		legacyBytes, _ := json.Marshal(map[string]any{"query": test.query, "total_available": len(defs), "matches": old})
		oldRank, newRank := 0, 0
		for position, match := range old {
			if match["name"] == test.expected {
				oldRank = position + 1
				break
			}
		}
		for position, match := range result.Matches {
			if match.Name == test.expected {
				newRank = position + 1
				break
			}
		}
		describeBytes := 0
		if oldRank > 0 {
			oldHits++
			oldMRR += 1 / float64(oldRank)
			for _, def := range defs {
				if def.Name == test.expected {
					declaration, _ := json.Marshal(def)
					describeBytes = len(declaration)
					break
				}
			}
		}
		if newRank > 0 {
			newHits++
			newMRR += 1 / float64(newRank)
		}
		oldBytes += len(legacyBytes) + describeBytes
		newBytes += len(encoded)
		if oldRank > 0 && newRank > 0 {
			sharedOldBytes += len(legacyBytes) + describeBytes
			sharedNewBytes += len(encoded)
		}
		t.Logf("query=%q expected=%s old_rank=%d new_rank=%d new_bytes=%d", test.query, test.expected, oldRank, newRank, len(encoded))
		if newRank == 0 {
			t.Errorf("expected %s in first five results for %q", test.expected, test.query)
		}
	}
	count := float64(len(cases))
	t.Logf("catalog=%d queries=%d old_recall@5=%.3f new_recall@5=%.3f old_mrr@5=%.3f new_mrr@5=%.3f old_workflow_bytes=%d new_workflow_bytes=%d (old adds one describe on successful discovery; bytes are not provider token counts)", len(defs), len(cases), float64(oldHits)/count, float64(newHits)/count, oldMRR/count, newMRR/count, oldBytes, newBytes)
	t.Logf("shared_success_old_bytes=%d shared_success_new_bytes=%d approximate_output_tokens_at_4_bytes_per_token: old=%d new=%d (heuristic only; excludes model inputs and failed-query retries)", sharedOldBytes, sharedNewBytes, (sharedOldBytes+3)/4, (sharedNewBytes+3)/4)
}

func BenchmarkToolDiscoveryRealCatalog(b *testing.B) {
	ca := buildFullRegistryAgent(b)
	ca.ToolsetFilter().PlatformRef().SetOverride("cli", []string{"full"})
	ca.ToolsetFilter().InvalidateTools()
	search, ok := ca.core.GetTool("tool_search")
	if !ok {
		b.Fatal("tool_search is not registered")
	}
	args := json.RawMessage(`{"query":"cancel download","limit":5}`)
	if _, err := search.Func(context.Background(), args); err != nil {
		b.Fatal(err)
	}
	b.Run("legacy", func(b *testing.B) {
		b.ReportAllocs()
		for position := 0; position < b.N; position++ {
			defs := discoveryCatalog(ca)
			if _, err := json.Marshal(map[string]any{"query": "cancel download", "total_available": len(defs), "matches": legacyDiscovery(defs, "cancel download", 5)}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("indexed_cached", func(b *testing.B) {
		b.ReportAllocs()
		for position := 0; position < b.N; position++ {
			out, err := search.Func(context.Background(), args)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := json.Marshal(out); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// registeredTools returns the sorted name+description pairs from the agent's
// live registry.
func registeredTools(ca *CovoAgent) []toolRow {
	names := ca.core.ToolNames()
	rows := make([]toolRow, 0, len(names))
	for _, name := range names {
		tool, ok := ca.core.GetTool(name)
		if !ok {
			continue
		}
		safe := "undeclared"
		if tool.ConcurrencySafe != nil {
			if *tool.ConcurrencySafe {
				safe = "safe"
			} else {
				safe = "exclusive"
			}
		}
		rows = append(rows, toolRow{Name: name, Summary: firstSentence(tool.Description), Safety: safe})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

type toolRow struct {
	Name    string
	Summary string
	Safety  string
}

// firstSentence trims a description to its first sentence for the catalog.
func firstSentence(desc string) string {
	desc = strings.ReplaceAll(desc, "\n", " ")
	if idx := strings.Index(desc, ". "); idx >= 0 {
		return desc[:idx+1]
	}
	if len(desc) > 120 {
		return desc[:117] + "..."
	}
	return desc
}

// TestToolCatalogCoversRegistry is the documentation CI gate: every tool in
// the live registry must have a row in docs/tool-catalog.md. A new tool
// without a catalog entry fails the build until documented (dsh-style
// "docs as tests"). Regenerate with -update-tool-catalog.
func TestToolCatalogCoversRegistry(t *testing.T) {
	ca := buildFullRegistryAgent(t)
	rows := registeredTools(ca)
	if len(rows) < 50 {
		t.Fatalf("registry suspiciously small (%d tools); construction path changed?", len(rows))
	}

	catalogPath := filepath.Join(catalogRelPath)
	if *updateToolCatalog {
		if err := writeToolCatalog(catalogPath, rows); err != nil {
			t.Fatalf("write catalog: %v", err)
		}
		t.Logf("regenerated %s with %d tools", catalogPath, len(rows))
		return
	}

	data, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("read catalog: %v (regenerate with go test ./internal/agent -run TestToolCatalog -update-tool-catalog)", err)
	}
	documented := map[string]bool{}
	for _, m := range catalogNameRe.FindAllStringSubmatch(string(data), -1) {
		documented[m[1]] = true
	}

	var missing []string
	for _, row := range rows {
		if !documented[row.Name] {
			missing = append(missing, row.Name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d tools missing from docs/tool-catalog.md — document them (or regenerate with -update-tool-catalog):\n%s",
			len(missing), strings.Join(missing, "\n"))
	}
}

func writeToolCatalog(path string, rows []toolRow) error {
	var b strings.Builder
	b.WriteString("# Tool Catalog\n\n")
	b.WriteString("Auto-checked by TestToolCatalogCoversRegistry: every registered tool must have a row here.\n")
	b.WriteString("Regenerate with: go test ./internal/agent -run TestToolCatalog -update-tool-catalog\n\n")
	b.WriteString("| 工具 | 功能 | 并发声明 |\n")
	b.WriteString("|------|------|----------|\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", row.Name, row.Summary, row.Safety)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
