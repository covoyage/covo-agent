package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/cyber"
	"github.com/covoyage/covo-agent/internal/evolution"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
	"github.com/covoyage/covonaut/agentcore"
)

func TestHideCyberOnlyTools(t *testing.T) {
	defs := []agentcore.ToolDefinition{
		{Name: "read"},
		{Name: "scan"},
		{Name: "finding_upsert"},
	}
	hidden := hideCyberOnlyTools(defs, false)
	for _, td := range hidden {
		if td.Name == "scan" || td.Name == "finding_upsert" {
			t.Fatalf("cyber-only tool %q leaked to tool_search outside cyber mode", td.Name)
		}
	}
	shown := hideCyberOnlyTools(defs, true)
	if len(shown) != 3 {
		t.Fatalf("cyber mode should keep all tools, got %d", len(shown))
	}
}

func TestCyberDelegationRequiresExplicitRole(t *testing.T) {
	for _, test := range []struct {
		cyberMode         bool
		role, contextRole string
	}{
		{false, "", ""},
		{true, "parent", "recon"},
		{true, "recon", "parent"},
	} {
		cyberMode := test.cyberMode
		home := t.TempDir()
		extension := NewExtension(ExtensionConfig{
			HomeDir: home, SessionsDir: filepath.Join(home, "sessions"), WorkDir: home, CyberMode: cyberMode, SubagentRole: test.role,
		})
		t.Cleanup(func() { _ = extension.Dispose() })
		workshop := evolution.NewWorkshopStore(home)
		if err := workshop.Init(); err != nil {
			t.Fatal(err)
		}
		extension.SetWorkshopStore(workshop)
		core := agentcore.New(agentcore.Config{})
		t.Cleanup(core.Close)
		if err := extension.Init(context.Background(), core); err != nil {
			t.Fatal(err)
		}
		seen := 0
		for _, tool := range extension.tools {
			if cyberMode && tool.Name == "scan" {
				ctx := toolssubagent.WithSubagentRole(context.Background(), test.contextRole)
				result, err := tool.Func(ctx, json.RawMessage(`{"recipe":"list"}`))
				if test.role == "parent" && err == nil {
					t.Fatalf("parent inherited recon context and ran scan: %+v", result)
				}
				if test.role == "recon" && err != nil {
					t.Fatalf("fixed recon role could not run scan with stale context: %v", err)
				}
			}
			if tool.Name != "sessions_spawn" && tool.Name != "sessions_spawn_batch" {
				continue
			}
			seen++
			schema := tool.Parameters
			if tool.Name == "sessions_spawn_batch" {
				schema = schema["properties"].(map[string]any)["tasks"].(map[string]any)["items"].(map[string]any)
			}
			requiredRole := false
			for _, field := range schema["required"].([]string) {
				requiredRole = requiredRole || field == "role"
			}
			if requiredRole != cyberMode {
				t.Fatalf("%s cyber=%v required role=%v", tool.Name, cyberMode, requiredRole)
			}
			if cyberMode && !strings.Contains(tool.Description, "leaf has no hands-on tools") {
				t.Fatalf("missing Cyber delegation guidance: %s", tool.Description)
			}
		}
		if seen != 2 {
			t.Fatalf("expected both delegation tools, got %d", seen)
		}
	}
}

func TestToolCallRejectsSelf(t *testing.T) {
	ag := agentcore.New(agentcore.Config{})
	tool := buildToolCallTool(ag)
	raw, _ := json.Marshal(map[string]any{
		"name":      "tool_call",
		"arguments": map[string]any{},
	})
	out, err := tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	result := out.(map[string]any)
	if result["status"] != "invalid_request" || !strings.Contains(result["error"].(string), "cannot invoke itself") {
		t.Fatalf("expected recoverable self-call rejection, got %#v", out)
	}
}

func TestToolCallMissingNameIsRecoverable(t *testing.T) {
	ag := agentcore.New(agentcore.Config{})
	out, err := buildToolCallTool(ag).Func(context.Background(), json.RawMessage(`{"arguments":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	result := out.(map[string]any)
	if result["status"] != "invalid_request" || !strings.Contains(result["error"].(string), "name is required") {
		t.Fatalf("unexpected result %#v", out)
	}
}

func TestToolSearchOmitsDiscoveryMetaTools(t *testing.T) {
	tool := buildToolSearchTool(func() []agentcore.ToolDefinition {
		return []agentcore.ToolDefinition{
			{Name: "tool_call", Description: "invoke tool"},
			{Name: "tool_describe", Description: "describe tool"},
			{Name: "scan", Description: "scan target with tool"},
		}
	})
	out, err := tool.Func(context.Background(), json.RawMessage(`{"query":"tool"}`))
	if err != nil {
		t.Fatal(err)
	}
	result := out.(map[string]any)
	matches := result["matches"].([]map[string]any)
	if len(matches) != 1 || matches[0]["name"] != "scan" {
		t.Fatalf("meta tools leaked into search: %#v", matches)
	}
}

func TestToolSearchFindsSchemaCapabilities(t *testing.T) {
	parameters := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{"navigate", "download_cancel"},
			},
		},
		"required": []string{"action"},
	}
	tool := buildToolSearchTool(func() []agentcore.ToolDefinition {
		return []agentcore.ToolDefinition{{
			Name: "browser", Description: "Control a page", Parameters: parameters,
		}}
	})
	for _, query := range []string{"download_cancel", "page"} {
		t.Run(query, func(t *testing.T) {
			args, err := json.Marshal(map[string]any{"query": query})
			if err != nil {
				t.Fatal(err)
			}
			out, err := tool.Func(context.Background(), args)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Matches []struct {
					Name       string          `json:"name"`
					Parameters json.RawMessage `json:"parameters"`
				} `json:"matches"`
			}
			if err := json.Unmarshal(encoded, &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Matches) != 1 || result.Matches[0].Name != "browser" {
				t.Fatalf("schema capability was not discovered: %s", encoded)
			}
			want, err := json.Marshal(parameters)
			if err != nil {
				t.Fatal(err)
			}
			if string(result.Matches[0].Parameters) != string(want) {
				t.Fatalf("search must return the full schema: %s", encoded)
			}
		})
	}
}

func TestToolSearchIndexesSchemaContentOnly(t *testing.T) {
	tool := buildToolSearchTool(func() []agentcore.ToolDefinition {
		return []agentcore.ToolDefinition{{
			Name: "browser", Description: "Control a page",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"steps": map[string]any{
						"type": "array",
						"items": map[string]any{
							"oneOf": []any{map[string]any{
								"properties": map[string]any{
									"action":  map[string]any{"enum": []string{"download_cancel"}},
									"timeout": map[string]any{"description": "Deadline in milliseconds"},
								},
							}},
						},
					},
				},
			},
		}}
	})
	for _, test := range []struct {
		query string
		want  int
	}{
		{"download_cancel", 1}, {"cancel download", 1}, {"milliseconds", 1}, {"timeout", 1},
		{"object", 0}, {"oneOf", 0}, {"properties", 0},
	} {
		t.Run(test.query, func(t *testing.T) {
			args, _ := json.Marshal(map[string]any{"query": test.query})
			out, err := tool.Func(context.Background(), args)
			if err != nil {
				t.Fatal(err)
			}
			matches := out.(map[string]any)["matches"].([]map[string]any)
			if len(matches) != test.want {
				t.Fatalf("query %q: got %d matches, want %d", test.query, len(matches), test.want)
			}
		})
	}
}

func TestToolSearchQueryModes(t *testing.T) {
	defs := []agentcore.ToolDefinition{
		{Name: "browser", Description: "Navigate pages"},
		{Name: "web_fetch", Description: "Fetch browser pages"},
		{Name: "Echo", Description: "Echo a value"},
		{Name: "echo", Description: "Echo a value"},
		{Name: "secret", Description: "Hidden capability"},
		{Name: "tool_call", Description: "Invoke a tool"},
	}
	tool := buildToolSearchTool(func() []agentcore.ToolDefinition { return defs }, func(_ context.Context, name string) bool {
		return name != "secret"
	})
	for _, test := range []struct {
		query string
		limit int
		want  string
	}{
		{" select:'BROWSER',browser,web_fetch ", 10, "browser,web_fetch"},
		{"select:web_fetch,browser", 1, "web_fetch"},
		{"select:Echo", 10, "Echo"},
		{"select:ECHO", 10, ""},
		{"select:secret,missing,tool_call", 10, ""},
		{"+browser fetch", 10, "browser"},
		{"+web +fetch browser", 10, "web_fetch"},
		{"+absent browser", 10, ""},
		{"the and for", 10, ""},
		{"!!!", 10, ""},
		{"select:", 10, ""},
	} {
		t.Run(test.query, func(t *testing.T) {
			args, _ := json.Marshal(map[string]any{"query": test.query, "limit": test.limit})
			out, err := tool.Func(context.Background(), args)
			if err != nil {
				t.Fatal(err)
			}
			result := out.(map[string]any)
			var names []string
			for _, match := range result["matches"].([]map[string]any) {
				names = append(names, match["name"].(string))
			}
			if strings.Join(names, ",") != test.want || result["total_available"] != 4 {
				t.Fatalf("unexpected selection: %#v", result)
			}
			switch test.query {
			case "select:web_fetch,browser":
				if strings.Join(result["truncated"].([]string), ",") != "browser" || result["total_matches"] != 2 {
					t.Fatalf("missing truncation diagnostic: %#v", result)
				}
			case "select:ECHO":
				if len(result["ambiguous"].(map[string][]string)["ECHO"]) != 2 {
					t.Fatalf("missing ambiguity diagnostic: %#v", result)
				}
			case "select:secret,missing,tool_call":
				if len(result["unavailable"].([]string)) != 3 {
					t.Fatalf("missing unavailable diagnostic: %#v", result)
				}
			}
		})
	}
}

func TestToolSearchBM25RankingAndLimits(t *testing.T) {
	defs := []agentcore.ToolDefinition{
		{Name: "z_resumable", Description: "download resumable"},
		{Name: "a_generic", Description: "download download"},
		{Name: "browser", Description: "Navigate pages"},
		{Name: "browser_helper", Description: strings.Repeat("browser ", 30)},
	}
	for position := range 60 {
		defs = append(defs, agentcore.ToolDefinition{Name: fmt.Sprintf("other_%02d", position), Description: "download common"})
	}
	tool := buildToolSearchTool(func() []agentcore.ToolDefinition { return defs })
	for _, test := range []struct {
		query string
		limit int
		count int
		first string
	}{
		{"download resumable", 1, 1, "z_resumable"},
		{"browser", 1, 1, "browser"},
		{"download", 0, 10, ""},
		{"download", 100, 50, ""},
		{"common", 2, 2, "other_00"},
	} {
		args, _ := json.Marshal(map[string]any{"query": test.query, "limit": test.limit})
		out, err := tool.Func(context.Background(), args)
		if err != nil {
			t.Fatal(err)
		}
		matches := out.(map[string]any)["matches"].([]map[string]any)
		if len(matches) != test.count || (test.first != "" && matches[0]["name"] != test.first) {
			t.Fatalf("query %q limit %d: %#v", test.query, test.limit, matches)
		}
	}
	empty := buildToolSearchTool(func() []agentcore.ToolDefinition { return nil })
	if out, err := empty.Func(context.Background(), json.RawMessage(`{"query":"download"}`)); err != nil || out.(map[string]any)["status"] != "no_matches" {
		t.Fatalf("empty catalog: %#v, %v", out, err)
	}
}

func TestToolSearchNamePrefixes(t *testing.T) {
	defs := []agentcore.ToolDefinition{
		{Name: "browser", Description: "Control a page"},
		{Name: "mcp__inventory__navigator", Description: "Control a route"},
		{Name: "notes", Description: "Documentation browser"},
		{Name: "\u4e2d\u6587\u52a9\u624b\u5de5\u5177", Description: "辅助工具"},
	}
	tool := buildToolSearchTool(func() []agentcore.ToolDefinition { return defs })
	for _, test := range []struct{ query, want string }{
		{"browse", "browser"}, {"navig", "mcp__inventory__navigator"},
		{"bro", ""}, {"docu", ""}, {"select:browse", ""}, {"+browse", "browser"}, {"+browsing", ""},
		{"\u4e2d\u6587", ""}, {"\u4e2d\u6587\u52a9\u624b", "\u4e2d\u6587\u52a9\u624b\u5de5\u5177"},
	} {
		t.Run(test.query, func(t *testing.T) {
			args, err := json.Marshal(map[string]any{"query": test.query})
			if err != nil {
				t.Fatal(err)
			}
			out, err := tool.Func(context.Background(), args)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, match := range out.(map[string]any)["matches"].([]map[string]any) {
				names = append(names, match["name"].(string))
			}
			if strings.Join(names, ",") != test.want {
				t.Fatalf("query %q: got %v, want %q", test.query, names, test.want)
			}
		})
	}
}

func TestToolSearchWordForms(t *testing.T) {
	defs := []agentcore.ToolDefinition{
		{Name: "exporter", Description: "Export audit reports"},
		{Name: "profile", Description: "Store user preferences"},
		{Name: "runner", Description: "Execute tasks"},
		{Name: "read", Description: "Inspect documents"},
		{Name: "a_reference", Description: "Reading"},
		{Name: "stem:read", Description: "Auxiliary entry"},
		{Name: "scanner", Parameters: map[string]any{"properties": map[string]any{"entry": map[string]any{"description": "Inspect directories"}}}},
	}
	tool := buildToolSearchTool(func() []agentcore.ToolDefinition { return defs })
	for _, test := range []struct{ query, first string }{
		{"exporting audits", "exporter"}, {"preference", "profile"},
		{"execution", "runner"}, {"inspected directory", "scanner"},
		{"reading", "read"},
		{"select:exporting", ""}, {"+executed", ""},
	} {
		t.Run(test.query, func(t *testing.T) {
			args, err := json.Marshal(map[string]any{"query": test.query})
			if err != nil {
				t.Fatal(err)
			}
			out, err := tool.Func(context.Background(), args)
			if err != nil {
				t.Fatal(err)
			}
			matches := out.(map[string]any)["matches"].([]map[string]any)
			if test.first == "" {
				if len(matches) != 0 {
					t.Fatalf("unexpected expansion: %v", matches)
				}
			} else if len(matches) == 0 || matches[0]["name"] != test.first {
				t.Fatalf("query %q: got %v, want %q first", test.query, matches, test.first)
			}
		})
	}
	for _, token := range []string{"x", "v2", "stock_query", "中文"} {
		if toolSearchStemKey(token) != "" {
			t.Fatalf("non-English word was stemmed: %q", token)
		}
	}
}

func TestToolSearchPrimarySummary(t *testing.T) {
	defs := []agentcore.ToolDefinition{{
		Name: "executor", Description: "Execute shell commands. " + strings.Repeat("Detailed guidance. ", 300),
		Parameters: map[string]any{"properties": map[string]any{"options": map[string]any{"description": strings.Repeat("Additional guidance. ", 300)}}},
	}}
	for position := 0; position < 8; position++ {
		defs = append(defs, agentcore.ToolDefinition{
			Name: fmt.Sprintf("other_%02d", position), Description: "Manage jobs",
			Parameters: map[string]any{"properties": map[string]any{"command": map[string]any{"description": "A shell command"}}},
		})
	}
	tool := buildToolSearchTool(func() []agentcore.ToolDefinition { return defs })
	out, err := tool.Func(context.Background(), json.RawMessage(`{"query":"shell command","limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	matches := out.(map[string]any)["matches"].([]map[string]any)
	if len(matches) != 1 || matches[0]["name"] != "executor" {
		t.Fatalf("primary purpose lost to incidental fields: %v", matches)
	}
}

func TestToolSearchFieldedRanking(t *testing.T) {
	defs := []agentcore.ToolDefinition{
		{Name: "executor", Description: "Execute shell commands. " + strings.Repeat("Detailed guidance. ", 300),
			Parameters: map[string]any{"properties": map[string]any{"options": map[string]any{"description": strings.Repeat("Additional guidance. ", 300)}}}},
		{Name: "preferences", Description: "Configure preferences for shell commands within the reusable automation environment. " + strings.Repeat("shell commands ", 100)},
	}
	tool := buildToolSearchTool(func() []agentcore.ToolDefinition { return defs })
	out, err := tool.Func(context.Background(), json.RawMessage(`{"query":"shell commands","limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	matches := out.(map[string]any)["matches"].([]map[string]any)
	if len(matches) != 1 {
		t.Fatalf("expected one result, got %d", len(matches))
	}
	if name := matches[0]["name"]; name != "executor" {
		t.Fatalf("incidental repetition outweighed the primary capability: %v", name)
	}
}

func TestToolSearchContextualNames(t *testing.T) {
	defs := []agentcore.ToolDefinition{
		{Name: "find", Description: "Find files by filename"},
		{Name: "git_log", Description: "Show commit history"},
		{Name: "mcp", Description: "Manage MCP server connections"},
		{Name: "mcp_audit", Description: "Audit MCP metadata for suspicious changes"},
	}
	tool := buildToolSearchTool(func() []agentcore.ToolDefinition { return defs })
	for _, test := range []struct{ query, first string }{
		{"find commit history", "git_log"},
		{"audit MCP metadata for suspicious changes", "mcp_audit"},
		{"find", "find"}, {"select:mcp", "mcp"},
	} {
		t.Run(test.query, func(t *testing.T) {
			args, _ := json.Marshal(map[string]any{"query": test.query, "limit": 1})
			out, err := tool.Func(context.Background(), args)
			if err != nil {
				t.Fatal(err)
			}
			matches := out.(map[string]any)["matches"].([]map[string]any)
			if len(matches) != 1 || matches[0]["name"] != test.first {
				t.Fatalf("query %q: expected %s first, got %v", test.query, test.first, matches)
			}
		})
	}
}

func TestToolSearchIdentifierEvidence(t *testing.T) {
	defs := []agentcore.ToolDefinition{
		{Name: "writer", Description: "Create calendar-free text files"},
		{Name: "find", Description: "Find files by filename"},
		{Name: "git_status", Description: "Show repository status"},
		{Name: "browser", Parameters: map[string]any{"properties": map[string]any{"action": map[string]any{"enum": []string{"download_cancel"}}}}},
	}
	tool := buildToolSearchTool(func() []agentcore.ToolDefinition { return defs })
	for _, test := range []struct{ query, first string }{
		{"calendar_event_create", ""}, {"unknown_service_status", ""},
		{"download_cancel", "browser"}, {"git_status", "git_status"},
		{"find_files", "find"},
	} {
		t.Run(test.query, func(t *testing.T) {
			args, _ := json.Marshal(map[string]any{"query": test.query, "limit": 1})
			out, err := tool.Func(context.Background(), args)
			if err != nil {
				t.Fatal(err)
			}
			matches := out.(map[string]any)["matches"].([]map[string]any)
			if test.first == "" {
				if len(matches) != 0 {
					t.Fatalf("unsupported identifier returned %s", matches[0]["name"])
				}
			} else if len(matches) != 1 || matches[0]["name"] != test.first {
				t.Fatalf("query %q: expected %s, got %v", test.query, test.first, matches)
			}
		})
	}
}

func TestToolSearchPrimaryRetrievalTerms(t *testing.T) {
	defs := []agentcore.ToolDefinition{
		{Name: "reader", Description: "Load a resource. " + strings.Repeat("Additional usage guidance. ", 300), SearchTerms: []string{"raster image"}},
		{Name: "templates", Description: "Manage templates", Parameters: map[string]any{"properties": map[string]any{"options": map[string]any{"description": strings.Repeat("raster image ", 100)}}}},
	}
	tool := buildToolSearchTool(func() []agentcore.ToolDefinition { return defs })
	out, err := tool.Func(context.Background(), json.RawMessage(`{"query":"raster image","limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	matches := out.(map[string]any)["matches"].([]map[string]any)
	if len(matches) != 1 || matches[0]["name"] != "reader" {
		t.Fatal("declared retrieval capability lost to incidental schema text")
	}
}

func TestToolSearchQueryNoise(t *testing.T) {
	defs := []agentcore.ToolDefinition{
		{Name: "reader", Description: "Inspect file lines"},
		{Name: "config", Description: "Inspect config yaml settings"},
	}
	tool := buildToolSearchTool(func() []agentcore.ToolDefinition { return defs })
	out, err := tool.Func(context.Background(), json.RawMessage(`{"query":"Could you please inspect config.yaml at lines 120 through 150","limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	matches := out.(map[string]any)["matches"].([]map[string]any)
	if len(matches) != 1 || matches[0]["name"] != "reader" {
		t.Fatal("filename or conversational noise displaced the file-reading intent")
	}
}

func TestToolSearchCacheRefresh(t *testing.T) {
	cache := &toolSearchCache{}
	defs := []agentcore.ToolDefinition{
		{Name: "browser", Description: "Navigate", Parameters: map[string]any{"properties": map[string]any{"action": map[string]any{"enum": []string{"navigate"}}}}},
		{Name: "echo", Description: "Echo"},
	}
	first, err := cache.snapshot(defs)
	if err != nil {
		t.Fatal(err)
	}
	browserDocument, echoDocument := cache.documents["browser"], cache.documents["echo"]
	second, err := cache.snapshot([]agentcore.ToolDefinition{defs[1], defs[0]})
	if err != nil || first != second {
		t.Fatalf("unchanged catalog should reuse index: %v", err)
	}
	defs[0].Parameters["properties"].(map[string]any)["action"].(map[string]any)["enum"] = []string{"download_cancel"}
	updated, err := cache.snapshot(defs)
	if err != nil || first == updated {
		t.Fatalf("schema change must rebuild index: %v", err)
	}
	if cache.documents["browser"] == browserDocument || cache.documents["echo"] != echoDocument {
		t.Fatal("a single schema change must replace only its own document")
	}
	if len(first.search("download_cancel", 10)["matches"].([]map[string]any)) != 0 || len(updated.search("download_cancel", 10)["matches"].([]map[string]any)) != 1 {
		t.Fatal("cached schema was mutated or not refreshed")
	}
	defs[0].Description = "Screenshot capture"
	updated, err = cache.snapshot(defs[:1])
	if err != nil || len(updated.documents) != 1 || len(updated.search("screenshot", 10)["matches"].([]map[string]any)) != 1 {
		t.Fatalf("registry or description change was not refreshed: %v", err)
	}
	if len(cache.documents) != 1 || cache.documents["echo"] != nil {
		t.Fatal("removed tool retained cached metadata")
	}
	out := updated.search("screenshot", 10)["matches"].([]map[string]any)
	out[0]["parameters"].(json.RawMessage)[0] = '!'
	if !json.Valid(updated.search("screenshot", 10)["matches"].([]map[string]any)[0]["parameters"].(json.RawMessage)) {
		t.Fatal("caller mutated cached schema")
	}
}

func TestToolSearchCacheJSONShapes(t *testing.T) {
	parameters := map[string]any{
		"nil": nil, "nil_map": map[string]any(nil), "nil_list": []string(nil),
		"empty_map": map[string]any{}, "empty_list": []string{},
		"required": []string{"action"}, "nested": []any{map[string]any{"minimum": 3}},
		"maximum": json.Number("9007199254740993"),
	}
	cache := &toolSearchCache{}
	defs := []agentcore.ToolDefinition{{Name: "fixture", Parameters: parameters}}
	first, err := cache.snapshot(defs)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cache.snapshot(defs)
	if err != nil || first != second {
		t.Fatalf("JSON-equivalent shapes did not reuse index: %v", err)
	}
	parameters["empty_list"] = nil
	third, err := cache.snapshot(defs)
	if err != nil || third == first {
		t.Fatalf("empty list to null did not invalidate: %v", err)
	}
	parameters["maximum"] = json.Number("9007199254740992")
	fourth, err := cache.snapshot(defs)
	if err != nil || fourth == third {
		t.Fatalf("large integer change did not invalidate: %v", err)
	}
	defs[0].Parameters = nil
	fifth, err := cache.snapshot(defs)
	if err != nil || fifth == fourth {
		t.Fatalf("root schema to null did not invalidate: %v", err)
	}
	defs[0].Parameters = map[string]any{}
	sixth, err := cache.snapshot(defs)
	if err != nil || sixth == fifth {
		t.Fatalf("root null to empty schema did not invalidate: %v", err)
	}
}

func TestToolSearchCacheDoesNotMutateDefinitions(t *testing.T) {
	parameters := func() map[string]any {
		return map[string]any{
			"type": "object", "maxProperties": 3,
			"properties": map[string]any{
				"action": map[string]any{"enum": []string{"navigate", "download_cancel"}},
			},
		}
	}
	definition := agentcore.ToolDefinition{Name: "browser", Parameters: parameters()}
	cache := &toolSearchCache{}
	if _, err := cache.snapshot([]agentcore.ToolDefinition{definition}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(definition.Parameters, parameters()) {
		t.Fatalf("building the search index mutated the registered schema: %#v", definition.Parameters)
	}
}

func TestToolDiscoveryOutputBudget(t *testing.T) {
	parameters := map[string]any{"type": "object", "properties": map[string]any{"action": map[string]any{"enum": []string{"inspect"}}}}
	defs := []agentcore.ToolDefinition{
		{Name: "first", Description: "Inspect", Parameters: parameters},
		{Name: "second", Description: strings.Repeat("description ", 400), Parameters: parameters},
	}
	search := buildToolSearchTool(func() []agentcore.ToolDefinition { return defs })
	out, err := search.Func(context.Background(), json.RawMessage(`{"query":"select:first,second","max_output_bytes":1024}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(out)
	result := out.(map[string]any)
	if err != nil || len(encoded) > 1024 || result["status"] != "budget_limited" {
		t.Fatalf("invalid bounded response (%d bytes): %s, %v", len(encoded), encoded, err)
	}
	matches := result["matches"].([]map[string]any)
	if len(matches) != 1 || matches[0]["name"] != "first" || !json.Valid(matches[0]["parameters"].(json.RawMessage)) || result["deferred_count"] != 1 {
		t.Fatalf("declarations were truncated or not deferred: %s", encoded)
	}
	describe := buildToolDescribeTool(func() []agentcore.ToolDefinition { return defs })
	out, err = describe.Func(context.Background(), json.RawMessage(`{"name":"second","max_output_bytes":1024}`))
	if err != nil {
		t.Fatal(err)
	}
	result = out.(map[string]any)
	if result["status"] != "output_budget_exceeded" || result["parameters"] != nil {
		t.Fatalf("partial schema was returned: %#v", result)
	}
	args, _ := json.Marshal(map[string]any{"name": "second", "max_output_bytes": result["required_bytes"]})
	out, err = describe.Func(context.Background(), args)
	if err != nil || out.(map[string]any)["parameters"] == nil {
		t.Fatalf("raising budget did not return the complete schema: %#v, %v", out, err)
	}
	defs[1].Description = strings.Repeat("x", maxToolDiscoveryBytes)
	out, err = describe.Func(context.Background(), json.RawMessage(`{"name":"second","max_output_bytes":128000}`))
	if err != nil || out.(map[string]any)["status"] != "schema_too_large" || out.(map[string]any)["parameters"] != nil {
		t.Fatalf("oversized schema did not produce a terminal diagnostic: %#v, %v", out, err)
	}
	for _, budget := range []int{-1, 1, maxToolDiscoveryBytes + 1} {
		args, _ := json.Marshal(map[string]any{"query": "first", "max_output_bytes": budget})
		if _, err := search.Func(context.Background(), args); err == nil {
			t.Fatalf("invalid budget %d was accepted", budget)
		}
	}
}

func TestToolSearchBudgetBoundsDiagnostics(t *testing.T) {
	var defs []agentcore.ToolDefinition
	var names []string
	for position := range 50 {
		name := fmt.Sprintf("long_tool_%02d_%s", position, strings.Repeat("name", 50))
		names = append(names, name)
		defs = append(defs, agentcore.ToolDefinition{Name: name, Description: strings.Repeat("description ", 100)})
	}
	tool := buildToolSearchTool(func() []agentcore.ToolDefinition { return defs })
	args, _ := json.Marshal(map[string]any{"query": "select:" + strings.Join(names, ","), "limit": 50, "max_output_bytes": 1024})
	out, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(out)
	if err != nil || len(encoded) > 1024 || !json.Valid(encoded) {
		t.Fatalf("diagnostics exceeded budget: %d bytes, %v", len(encoded), err)
	}
}

func TestToolSearchLocalTagsAndNumericContracts(t *testing.T) {
	cache := &toolSearchCache{}
	defs := []agentcore.ToolDefinition{{Name: "browser", SearchTerms: []string{"printout"}, Parameters: map[string]any{"maximum": uint64(9007199254740993)}}}
	first, err := cache.snapshot(defs)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.search("printout", 10)["matches"].([]map[string]any)) != 1 {
		t.Fatal("local retrieval tag was not indexed")
	}
	if string(first.documents[0].parameters) != `{"maximum":9007199254740993}` {
		t.Fatalf("schema number lost precision: %s", first.documents[0].parameters)
	}
	defs[0].SearchTerms[0] = "hardcopy"
	second, err := cache.snapshot(defs)
	if err != nil || first == second || len(second.search("printout", 10)["matches"].([]map[string]any)) != 0 || len(second.search("hardcopy", 10)["matches"].([]map[string]any)) != 1 {
		t.Fatalf("retrieval tag change did not invalidate cache: %v", err)
	}
	before, err := toolSchemaFingerprint("browser", json.RawMessage(`{"maximum":9007199254740992}`))
	if err != nil {
		t.Fatal(err)
	}
	after, err := toolSchemaFingerprint("browser", first.documents[0].parameters)
	if err != nil || before == after {
		t.Fatalf("numeric contract change went undetected: %v", err)
	}
}

func TestToolSearchConcurrentVisibility(t *testing.T) {
	type visibilityKey struct{}
	parameters := map[string]any{
		"type": "object", "properties": map[string]any{"action": map[string]any{"enum": []string{"inspect"}}},
	}
	tool := buildToolSearchTool(func() []agentcore.ToolDefinition {
		return []agentcore.ToolDefinition{
			{Name: "first", Description: "Capability", Parameters: parameters},
			{Name: "second", Description: "Capability", Parameters: parameters},
		}
	}, func(ctx context.Context, name string) bool { return name == ctx.Value(visibilityKey{}) })
	var workers sync.WaitGroup
	for position := range 40 {
		name := "first"
		if position%2 != 0 {
			name = "second"
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			ctx := context.WithValue(context.Background(), visibilityKey{}, name)
			out, err := tool.Func(ctx, json.RawMessage(`{"query":"capability"}`))
			if err != nil {
				t.Error(err)
				return
			}
			result := out.(map[string]any)
			matches := result["matches"].([]map[string]any)
			if len(matches) != 1 || matches[0]["name"] != name || result["total_available"] != 1 {
				t.Errorf("visibility leaked across cached contexts: %#v", result)
			}
		}()
	}
	workers.Wait()
}

func TestCyberRoleVisibilityAppliesToSearchAndCall(t *testing.T) {
	called := false
	ag := agentcore.New(agentcore.Config{})
	ag.RegisterTools(&agentcore.Tool{
		Name: "scan",
		Func: func(context.Context, json.RawMessage) (any, error) {
			called = true
			return "ok", nil
		},
	})
	visibility := func(ctx context.Context, name string) bool {
		return cyber.AllowOrchestratorTool(toolssubagent.SubagentRoleFromContext(ctx), name)
	}
	defs := func() []agentcore.ToolDefinition {
		return []agentcore.ToolDefinition{{Name: "scan", Description: "scan target"}}
	}
	search := buildToolSearchTool(defs, visibility)
	parentResult, err := search.Func(context.Background(), json.RawMessage(`{"query":"scan"}`))
	if err != nil {
		t.Fatal(err)
	}
	if matches := parentResult.(map[string]any)["matches"].([]map[string]any); len(matches) != 0 {
		t.Fatalf("parent discovered hands-on tool: %#v", matches)
	}

	call := buildToolCallTool(ag, visibility)
	parentCall, err := call.Func(context.Background(), json.RawMessage(`{"name":"scan","arguments":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if parentCall.(map[string]any)["status"] != "unavailable" || called {
		t.Fatalf("parent invoked hands-on tool: %#v called=%v", parentCall, called)
	}

	reconCtx := toolssubagent.WithSubagentRole(context.Background(), "recon")
	reconResult, err := search.Func(reconCtx, json.RawMessage(`{"query":"scan"}`))
	if err != nil {
		t.Fatal(err)
	}
	if matches := reconResult.(map[string]any)["matches"].([]map[string]any); len(matches) != 1 {
		t.Fatalf("recon could not discover scan: %#v", matches)
	}
	retainToolSchema(t, ag, "scan")
	if _, err := call.Func(reconCtx, json.RawMessage(`{"name":"scan","arguments":{}}`)); err != nil || !called {
		t.Fatalf("recon could not invoke scan: err=%v called=%v", err, called)
	}
}

func TestToolCallRunsInvokeToolHooks(t *testing.T) {
	var seen []string
	ag := agentcore.New(agentcore.Config{
		ExecutionConfig: agentcore.ExecutionConfig{
			GlobalBefore: []agentcore.BeforeHook{
				func(_ context.Context, hc *agentcore.HookContext) error {
					seen = append(seen, hc.ToolName)
					return nil
				},
			},
		},
	})
	echo := &agentcore.Tool{
		Name: "echo",
		Func: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "pong", nil
		},
	}
	ag.RegisterTools(echo)
	if _, err := echo.Func(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 0 {
		t.Fatalf("direct Func must not run hooks, seen %v", seen)
	}

	call := buildToolCallTool(ag)
	retainToolSchema(t, ag, "echo")
	raw, _ := json.Marshal(map[string]any{
		"name":      "echo",
		"arguments": map[string]any{},
	})
	out, err := call.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["status"] != "ok" {
		t.Fatalf("unexpected result %#v", out)
	}
	if len(seen) != 1 || seen[0] != "echo" {
		t.Fatalf("InvokeTool should run hooks, seen %v", seen)
	}
}

func TestToolCallRequiresCurrentSchema(t *testing.T) {
	agent := agentcore.New(agentcore.Config{})
	t.Cleanup(agent.Close)
	called := 0
	target := &agentcore.Tool{
		Name: "echo", Description: "Echo text",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}},
		Func:       func(context.Context, json.RawMessage) (any, error) { called++; return "ok", nil },
	}
	agent.RegisterTools(target)
	call := buildToolCallTool(agent)
	check := func(want string) {
		t.Helper()
		out, err := call.Func(context.Background(), json.RawMessage(`{"name":"ECHO","arguments":{"value":"hello"}}`))
		if err != nil || out.(map[string]any)["status"] != want {
			t.Fatalf("want %q: %#v, %v", want, out, err)
		}
	}
	check("schema_required")
	retainToolSchema(t, agent, "echo")
	retained := agent.State().Messages()
	check("ok")
	target.Description = "Echo with a different description"
	check("ok")
	target.Parameters = map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "integer"}}}
	check("schema_changed")
	describe := buildToolDescribeTool(func() []agentcore.ToolDefinition {
		return []agentcore.ToolDefinition{{Name: target.Name, Parameters: target.Parameters}}
	})
	out, err := describe.Func(context.Background(), json.RawMessage(`{"name":"ECHO"}`))
	if err != nil {
		t.Fatal(err)
	}
	retainDiscoveryResult(t, agent, "tool_describe", out)
	check("ok")
	agent.State().ReplaceMessages(nil)
	check("schema_required")
	retained[1].Content = `{"matches":[`
	agent.State().ReplaceMessages(retained)
	check("schema_required")
	retained[1].Role = agentcore.RoleUser
	retained[1].Content = `{"matches":[{"name":"echo","parameters":{"type":"object"}}]}`
	agent.State().ReplaceMessages(retained)
	check("schema_required")
	if called != 3 {
		t.Fatalf("unreviewed call executed: %d calls", called)
	}
	other := &toolSchemaReview{}
	if other.check("echo", target.Parameters) != "schema_required" {
		t.Fatal("review leaked into another context")
	}
}

func TestToolCallRejectsDiscoveryAndAmbiguity(t *testing.T) {
	agent := agentcore.New(agentcore.Config{})
	t.Cleanup(agent.Close)
	agent.RegisterTools(&agentcore.Tool{Name: "Echo"}, &agentcore.Tool{Name: "echo"})
	call := buildToolCallTool(agent)
	for _, name := range []string{"tool_search", "tool_describe", "TOOL_CALL", "ECHO"} {
		args, _ := json.Marshal(map[string]any{"name": name})
		out, err := call.Func(context.Background(), args)
		if err != nil || out.(map[string]any)["status"] != "invalid_request" {
			t.Fatalf("name %q was not rejected: %#v, %v", name, out, err)
		}
	}
}

type toolSearchTestProvider struct {
	complete func(*agentcore.ProviderRequest) (*agentcore.ProviderResponse, error)
}

func (provider *toolSearchTestProvider) Complete(_ context.Context, request *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	return provider.complete(request)
}

func (*toolSearchTestProvider) Stream(context.Context, *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	return nil, fmt.Errorf("unexpected streaming request")
}

func TestToolDiscoveryAgentLoopUsesModelContext(t *testing.T) {
	for _, remote := range []bool{false, true} {
		for _, strip := range []bool{false, true} {
			t.Run(fmt.Sprintf("mcp=%v/strip=%v", remote, strip), func(t *testing.T) {
				home := t.TempDir()
				extension := NewExtension(ExtensionConfig{HomeDir: home, SessionsDir: filepath.Join(home, "sessions"), WorkDir: home})
				workshop := evolution.NewWorkshopStore(home)
				if err := workshop.Init(); err != nil {
					t.Fatal(err)
				}
				extension.SetWorkshopStore(workshop)
				targetName, query := "echo", "local_retrieval_label"
				client := &discoveryMCPClient{}
				if remote {
					targetName, query = "mcp__inventory__lookup", "select:mcp__inventory__lookup"
					extension.mcpStore.servers["inventory"] = &mcpServer{
						config: mcpServerConfig{Name: "inventory"}, connected: true, client: client, closeConn: func() {},
						tools: []mcpToolDef{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}},
					}
				}
				turn, called, hooks := 0, 0, 0
				provider := &toolSearchTestProvider{complete: func(request *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
					turn++
					switch turn {
					case 1:
						return &agentcore.ProviderResponse{ToolCalls: []agentcore.ToolCall{{ID: "discover", Name: "tool_search", Arguments: fmt.Sprintf(`{"query":%q}`, query)}}}, nil
					case 2:
						return &agentcore.ProviderResponse{ToolCalls: []agentcore.ToolCall{{ID: "invoke", Name: "tool_call", Arguments: fmt.Sprintf(`{"name":%q,"arguments":{}}`, targetName)}}}, nil
					default:
						want := "ok"
						if strip {
							want = "schema_required"
						}
						found := false
						for _, message := range request.Messages {
							if message.Role == agentcore.RoleTool && message.ToolCallID == "invoke" {
								var result map[string]any
								if err := json.Unmarshal([]byte(message.Content), &result); err != nil || result["status"] != want {
									t.Fatalf("want %q: %s, %v", want, message.Content, err)
								}
								found = true
							}
						}
						if !found {
							t.Fatal("missing tool_call result")
						}
						return &agentcore.ProviderResponse{Content: "done"}, nil
					}
				}}
				agent := agentcore.New(agentcore.Config{
					ModelConfig: agentcore.ModelConfig{Provider: provider}, Extensions: []agentcore.Extension{extension},
					TransformContext: func(_ context.Context, messages []agentcore.Message) []agentcore.Message {
						if !strip {
							return messages
						}
						var filtered []agentcore.Message
						for _, message := range messages {
							if message.ToolCallID == "discover" || (len(message.ToolCalls) > 0 && message.ToolCalls[0].ID == "discover") {
								continue
							}
							filtered = append(filtered, message)
						}
						return filtered
					},
					ExecutionConfig: agentcore.ExecutionConfig{GlobalBefore: []agentcore.BeforeHook{func(_ context.Context, hook *agentcore.HookContext) error {
						if hook.ToolName == "echo" || (remote && hook.ToolName == "mcp") {
							hooks++
						}
						return nil
					}}},
				})
				t.Cleanup(agent.Close)
				agent.RegisterTools(&agentcore.Tool{Name: "echo", Tags: []string{"local_retrieval_label"}, Func: func(context.Context, json.RawMessage) (any, error) { called++; return "pong", nil }})
				if output, err := agent.Run(context.Background(), "Discover echo and invoke it"); err != nil || output != "done" {
					t.Fatalf("agent loop: %q, %v", output, err)
				}
				if remote {
					called = client.calls
				}
				want := 1
				if strip {
					want = 0
				}
				if called != want || hooks != want {
					t.Fatalf("target calls=%d hooks=%d, want %d", called, hooks, want)
				}
				found := false
				for _, message := range agent.State().Messages() {
					found = found || message.ToolCallID == "discover"
				}
				if !found {
					t.Fatal("persistent history should retain the search result")
				}
			})
		}
	}
}

type discoveryMCPClient struct {
	calls     int
	tool      string
	arguments map[string]any
}

func (*discoveryMCPClient) ListTools(context.Context) ([]mcpToolDef, error) { return nil, nil }
func (*discoveryMCPClient) Close()                                          {}
func (client *discoveryMCPClient) CallTool(_ context.Context, tool string, arguments map[string]any) (json.RawMessage, error) {
	client.calls++
	client.tool, client.arguments = tool, arguments
	return json.RawMessage(`{"content":[{"type":"text","text":"in stock"}]}`), nil
}

func TestToolDiscoveryMCPRemoteSchema(t *testing.T) {
	home := t.TempDir()
	extension := NewExtension(ExtensionConfig{HomeDir: home, SessionsDir: filepath.Join(home, "sessions"), WorkDir: home})
	t.Cleanup(func() { _ = extension.Dispose() })
	workshop := evolution.NewWorkshopStore(home)
	if err := workshop.Init(); err != nil {
		t.Fatal(err)
	}
	extension.SetWorkshopStore(workshop)
	parameters := map[string]any{"type": "object", "properties": map[string]any{
		"action":    map[string]any{"type": "string", "enum": []string{"stock_query"}},
		"record_id": map[string]any{"type": "integer"},
	}, "required": []string{"action"}}
	client := &discoveryMCPClient{}
	extension.mcpStore.servers["inventory"] = &mcpServer{
		config: mcpServerConfig{Name: "inventory"}, connected: true,
		client: client, closeConn: func() {},
		tools: []mcpToolDef{{Name: "lookup", Description: "Inspect inventory", InputSchema: parameters}},
	}
	hooks := 0
	var beforeMCP func() error
	agent := agentcore.New(agentcore.Config{ExecutionConfig: agentcore.ExecutionConfig{GlobalBefore: []agentcore.BeforeHook{
		func(_ context.Context, hook *agentcore.HookContext) error {
			if hook.ToolName == "mcp" {
				hooks++
				if beforeMCP != nil {
					return beforeMCP()
				}
			}
			return nil
		},
	}}})
	t.Cleanup(agent.Close)
	if err := extension.Init(context.Background(), agent); err != nil {
		t.Fatal(err)
	}
	search, ok := agent.GetTool("tool_search")
	if !ok {
		t.Fatal("tool_search is missing")
	}
	out, err := search.Func(context.Background(), json.RawMessage(`{"query":"stock_query"}`))
	if err != nil {
		t.Fatal(err)
	}
	matches := out.(map[string]any)["matches"].([]map[string]any)
	if len(matches) == 0 || matches[0]["name"] != "mcp__inventory__lookup" {
		t.Fatalf("remote schema capability was not discovered: status=%v matches=%d", out.(map[string]any)["status"], len(matches))
	}
	encoded, _ := json.Marshal(matches[0]["parameters"])
	want, _ := json.Marshal(parameters)
	if string(encoded) != string(want) {
		t.Fatalf("remote schema changed: got %s, want %s", encoded, want)
	}
	bridge, _ := agent.GetTool("tool_call")
	call := func(arguments string, status string) {
		t.Helper()
		result, err := bridge.Func(context.Background(), json.RawMessage(`{"name":"MCP__INVENTORY__LOOKUP","arguments":`+arguments+`}`))
		if err != nil || result.(map[string]any)["status"] != status {
			t.Fatalf("want %s: %#v, %v", status, result, err)
		}
	}
	call(`{"action":"stock_query"}`, "schema_required")
	retainDiscoveryResult(t, agent, "tool_search", out)
	extension.toolReview.replace(agent.State().Messages())
	call(`{}`, "invalid_request")
	call(`{"action":"stock_query","record_id":9007199254740993}`, "ok")
	if client.calls != 1 || hooks != 1 || client.tool != "lookup" || client.arguments["action"] != "stock_query" {
		t.Fatalf("unexpected remote call: %+v", client)
	}
	if client.arguments["record_id"] != json.Number("9007199254740993") {
		t.Fatalf("MCP argument precision lost: %v", client.arguments)
	}
	retainToolSchema(t, agent, "mcp")
	extension.toolReview.replace(agent.State().Messages())
	for _, arguments := range []string{
		`{"action":"call","server":"inventory","tool":"lookup","arguments":{"action":"stock_query"}}`,
		`{"name":"mcp__inventory__lookup","arguments":{"action":"stock_query"}}`,
	} {
		generic, err := bridge.Func(context.Background(), json.RawMessage(`{"name":"mcp","arguments":`+arguments+`}`))
		if err != nil || generic.(map[string]any)["status"] != "schema_required" {
			t.Fatalf("generic schema bypassed the remote contract: %#v, %v", generic, err)
		}
	}
	server := extension.mcpStore.servers["inventory"]
	server.toolsMu.Lock()
	server.tools[0].Description = "Updated inventory documentation"
	server.discoveryCache = nil
	server.toolsMu.Unlock()
	call(`{"action":"stock_query"}`, "ok")
	server.toolsMu.Lock()
	server.tools = []mcpToolDef{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}}
	server.discoveryCache = nil
	server.toolsMu.Unlock()
	call(`{"action":"stock_query"}`, "schema_changed")
	server.toolsMu.Lock()
	server.tools = []mcpToolDef{{Name: "lookup", InputSchema: parameters}}
	server.discoveryCache = nil
	server.toolsMu.Unlock()
	extension.mcpStore.servers["inventory"] = &mcpServer{config: server.config, connected: true, client: client, closeConn: func() {}, tools: server.tools}
	call(`{"action":"stock_query"}`, "schema_changed")
	describe, _ := agent.GetTool("tool_describe")
	refresh := func() {
		t.Helper()
		declaration, err := describe.Func(context.Background(), json.RawMessage(`{"name":"mcp__inventory__lookup"}`))
		if err != nil {
			t.Fatal(err)
		}
		retainDiscoveryResult(t, agent, "tool_describe", declaration)
		extension.toolReview.replace(agent.State().Messages())
	}
	refresh()
	beforeMCP = func() error {
		extension.mcpStore.mu.Lock()
		extension.mcpStore.servers["inventory"] = &mcpServer{config: server.config, connected: true, client: client, closeConn: func() {}, tools: server.tools}
		extension.mcpStore.mu.Unlock()
		return nil
	}
	call(`{"action":"stock_query"}`, "error")
	beforeMCP = nil
	refresh()
	beforeMCP = func() error { return fmt.Errorf("approval denied") }
	call(`{"action":"stock_query"}`, "error")
	beforeMCP = nil
	extension.toolReview.replace(nil)
	call(`{"action":"stock_query"}`, "schema_required")
	delete(extension.mcpStore.servers, "inventory")
	call(`{"action":"stock_query"}`, "unavailable")
	if client.calls != 2 {
		t.Fatal("stale or unreviewed remote request executed")
	}
}

func TestToolDiscoveryMCPCatalogIsolationAndNames(t *testing.T) {
	parameters := map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "integer"}}}
	client := &discoveryMCPClient{}
	store := NewMCPStore()
	server := &mcpServer{config: mcpServerConfig{Name: "inventory"}, connected: true, client: client, closeConn: func() {}, tools: []mcpToolDef{{Name: "read__stock", InputSchema: parameters}}}
	store.servers["inventory"] = server
	catalog, err := store.discoveryCatalog()
	if err != nil || len(catalog) != 1 {
		t.Fatalf("catalog: %+v, %v", catalog, err)
	}
	catalog[0].Parameters["properties"].(map[string]any)["id"].(map[string]any)["type"] = "string"
	if parameters["properties"].(map[string]any)["id"].(map[string]any)["type"] != "integer" {
		t.Fatal("catalog mutated registered MCP schema")
	}
	catalog, err = store.discoveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	agent := agentcore.New(agentcore.Config{})
	t.Cleanup(agent.Close)
	agent.RegisterTools(buildMCPTool(store))
	review := &toolSchemaReview{}
	search := buildToolSearchToolWithCatalog(store.discoveryCatalog)
	out, err := search.Func(context.Background(), json.RawMessage(`{"query":"select:mcp__inventory__read__stock"}`))
	if err != nil {
		t.Fatal(err)
	}
	retainDiscoveryResult(t, agent, "tool_search", out)
	review.replace(agent.State().Messages())
	bridge := buildToolCallToolWithCatalog(agent, review.check, store.discoveryCatalog, review)
	result, err := bridge.Func(context.Background(), json.RawMessage(`{"name":"mcp__inventory__read__stock","arguments":{"id":1}}`))
	if err != nil || result.(map[string]any)["status"] != "ok" || client.tool != "read__stock" {
		t.Fatalf("double-underscore tool misrouted: %#v, %v, target=%q", result, err, client.tool)
	}
	var wait sync.WaitGroup
	for worker := range 4 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 20 {
				if worker == 0 {
					server.toolsMu.Lock()
					server.tools = []mcpToolDef{{Name: "read__stock", InputSchema: parameters}}
					server.discoveryCache = nil
					server.toolsMu.Unlock()
				} else if _, err := store.discoveryCatalog(); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wait.Wait()
	extension := &Extension{mcpStore: store}
	agent.RegisterTools(&agentcore.Tool{Name: "mcp__inventory__read__stock", Parameters: map[string]any{"type": "object"}})
	entries, err := extension.toolDiscoveryCatalog(agent)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if entry.Name == "mcp__inventory__read__stock" {
			count++
			if entry.Source != nil {
				t.Fatal("remote tool shadowed registered tool")
			}
		}
	}
	if count != 1 {
		t.Fatalf("duplicate tool identity: %d", count)
	}
}

func TestToolDiscoveryMCPSnapshotRefresh(t *testing.T) {
	client := &auditMCPClient{definitions: []mcpToolDef{{Name: "lookup", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"sku": map[string]any{"type": "string"}}}}}}
	store := NewMCPStore()
	server := &mcpServer{config: mcpServerConfig{Name: "inventory"}, connected: true, client: client, tools: client.definitions}
	store.servers["inventory"] = server
	first, err := store.discoverySnapshot()
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.discoverySnapshot()
	if err != nil || first[0].Source != second[0].Source {
		t.Fatalf("unchanged server rebuilt snapshot: %v", err)
	}
	server.toolsMu.RLock()
	finished := make(chan error, 1)
	go func() { _, snapshotErr := store.discoverySnapshot(); finished <- snapshotErr }()
	select {
	case snapshotErr := <-finished:
		server.toolsMu.RUnlock()
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
	case <-time.After(time.Second):
		server.toolsMu.RUnlock()
		<-finished
		t.Fatal("warm discovery waited for an active call's read lock")
	}
	client.definitions = []mcpToolDef{{Name: "updated", InputSchema: map[string]any{"type": "object"}}}
	if _, err := buildMCPTool(store).Func(context.Background(), json.RawMessage(`{"action":"refresh","server":"inventory"}`)); err != nil {
		t.Fatal(err)
	}
	third, err := store.discoverySnapshot()
	if err != nil || len(third) != 1 || third[0].Name != "mcp__inventory__updated" {
		t.Fatalf("refresh not reflected: %+v, %v", third, err)
	}
	if first[0].Name != "mcp__inventory__lookup" || third[0].Source.Connection != first[0].Source.Connection {
		t.Fatal("refresh mutated old snapshot or changed connection identity")
	}
	client.definitions = nil
	if _, err := buildMCPTool(store).Func(context.Background(), json.RawMessage(`{"action":"refresh","server":"inventory"}`)); err != nil {
		t.Fatal(err)
	}
	empty, err := store.discoverySnapshot()
	if err != nil || len(empty) != 0 {
		t.Fatalf("removed tools retained: %+v, %v", empty, err)
	}
}

func BenchmarkToolDiscoveryMCPSnapshot(b *testing.B) {
	client := &auditMCPClient{}
	for position := 0; position < 100; position++ {
		client.definitions = append(client.definitions, mcpToolDef{Name: fmt.Sprintf("lookup_%03d", position), Description: "Look up inventory by SKU", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"sku": map[string]any{"type": "string"}}, "required": []string{"sku"}}})
	}
	store := NewMCPStore()
	store.servers["inventory"] = &mcpServer{config: mcpServerConfig{Name: "inventory"}, connected: true, client: client, tools: client.definitions}
	if _, err := store.discoverySnapshot(); err != nil {
		b.Fatal(err)
	}
	for _, mode := range []string{"snapshot_cached", "defensive_copy"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for position := 0; position < b.N; position++ {
				var err error
				if mode == "snapshot_cached" {
					_, err = store.discoverySnapshot()
				} else {
					_, err = store.discoveryCatalog()
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func retainToolSchema(t *testing.T, agent *agentcore.Agent, name string) {
	t.Helper()
	tool, ok := agent.GetTool(name)
	if !ok {
		t.Fatalf("tool %q is not registered", name)
	}
	search := buildToolSearchTool(func() []agentcore.ToolDefinition {
		return []agentcore.ToolDefinition{{Name: name, Description: tool.Description, Parameters: tool.Parameters}}
	})
	args, _ := json.Marshal(map[string]any{"query": "select:" + name})
	out, err := search.Func(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	retainDiscoveryResult(t, agent, "tool_search", out)
}

func retainDiscoveryResult(t *testing.T, agent *agentcore.Agent, discovery string, out any) {
	t.Helper()
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("schema-%d", len(agent.State().Messages()))
	agent.State().AddMessage(agentcore.Message{Role: agentcore.RoleAssistant, ToolCalls: []agentcore.ToolCall{{ID: id, Name: discovery, Arguments: `{}`}}})
	agent.State().AddMessage(agentcore.Message{Role: agentcore.RoleTool, Name: discovery, ToolCallID: id, Content: string(encoded)})
}
