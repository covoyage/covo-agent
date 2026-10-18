package toolset

import (
	"context"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestToolsetFilter_NameFilter(t *testing.T) {
	platform := NewPlatformToolsets("cli")
	platform.SetOverride("cli", []string{"read", "bash", "web_fetch"})

	tf := NewToolsetFilter(ToolsetFilterConfig{
		Platform: platform,
		ToolNames: func() []string {
			return []string{"read", "bash", "web_fetch"}
		},
		PlatformName: func() string { return "cli" },
		NameFilter:   func(name string) bool { return name != "bash" },
	})

	mcc := &agentcore.ModelCallContext{
		Request: &agentcore.ProviderRequest{
			Tools: []agentcore.ToolDefinition{
				{Name: "read"},
				{Name: "bash"},
				{Name: "web_fetch"},
			},
		},
	}
	if err := tf.BeforeModelCall(context.Background(), &agentcore.AgentRunContext{}, mcc); err != nil {
		t.Fatalf("BeforeModelCall: %v", err)
	}

	kept := make(map[string]bool)
	for _, def := range mcc.Request.Tools {
		kept[def.Name] = true
	}
	if kept["bash"] {
		t.Error("expected bash to be removed by NameFilter")
	}
	if !kept["read"] || !kept["web_fetch"] {
		t.Errorf("expected read and web_fetch to survive the NameFilter, kept=%v", kept)
	}
}

func TestToolsetFilter_ContextFilter(t *testing.T) {
	type roleKey struct{}
	platform := NewPlatformToolsets("cli")
	platform.SetOverride("cli", []string{"sessions_spawn", "scan", "bash"})
	tf := NewToolsetFilter(ToolsetFilterConfig{
		Platform:     platform,
		CyberMode:    true,
		ToolNames:    func() []string { return []string{"sessions_spawn", "scan", "bash"} },
		PlatformName: func() string { return "cli" },
		ContextFilter: func(ctx context.Context, name string) bool {
			role, _ := ctx.Value(roleKey{}).(string)
			return role != "" || (name != "scan" && name != "bash")
		},
	})
	filter := func(ctx context.Context) map[string]bool {
		mcc := &agentcore.ModelCallContext{Request: &agentcore.ProviderRequest{Tools: []agentcore.ToolDefinition{{Name: "sessions_spawn"}, {Name: "scan"}, {Name: "bash"}}}}
		if err := tf.BeforeModelCall(ctx, &agentcore.AgentRunContext{}, mcc); err != nil {
			t.Fatal(err)
		}
		kept := map[string]bool{}
		for _, def := range mcc.Request.Tools {
			kept[def.Name] = true
		}
		return kept
	}
	parent := filter(context.Background())
	if parent["scan"] || parent["bash"] || !parent["sessions_spawn"] {
		t.Fatalf("unexpected parent tools: %v", parent)
	}
	recon := filter(context.WithValue(context.Background(), roleKey{}, "recon"))
	if !recon["scan"] || !recon["bash"] {
		t.Fatalf("hands-on tools hidden from recon: %v", recon)
	}
}

func TestToolsetFilterRolePolicySurvivesResolutionError(t *testing.T) {
	const broken = "missing-role-test-toolset"
	Toolsets[broken] = ToolsetDef{Name: broken, Includes: []string{broken}}
	t.Cleanup(func() { delete(Toolsets, broken) })
	platform := NewPlatformToolsets("cli")
	platform.SetOverride("cli", []string{broken})
	names := []string{"sessions_spawn", "scan", "bash", "web_fetch"}
	filter := NewToolsetFilter(ToolsetFilterConfig{
		Platform: platform, CyberMode: true,
		ToolNames: func() []string { return names },
		ContextFilter: func(_ context.Context, name string) bool {
			return name != "scan" && name != "bash"
		},
	})
	request := &agentcore.ModelCallContext{Request: &agentcore.ProviderRequest{}}
	for _, name := range names {
		request.Request.Tools = append(request.Request.Tools, agentcore.ToolDefinition{Name: name})
	}
	if err := filter.BeforeModelCall(context.Background(), nil, request); err != nil {
		t.Fatal(err)
	}
	kept := []string{}
	for _, definition := range request.Request.Tools {
		kept = append(kept, definition.Name)
	}
	if len(kept) != 2 || !containsAll(kept, "sessions_spawn", "web_fetch") {
		t.Fatalf("policy bypassed on resolution failure: %v", kept)
	}
	preview := filter.Preview(names)
	if len(preview.Kept) != 2 || !containsAll(preview.Kept, "sessions_spawn", "web_fetch") || preview.Excluded["scan"] != "role policy" || preview.Excluded["bash"] != "role policy" {
		t.Fatalf("preview bypassed role policy: %+v", preview)
	}
}

func TestToolsetFilter_PreviewMatchesBeforeModelCall(t *testing.T) {
	platform := NewPlatformToolsets("cli")
	platform.SetOverride("cli", []string{"read", "mcp", "tool_search", "computer_use"})

	tf := NewToolsetFilter(ToolsetFilterConfig{
		Platform: platform,
		ToolNames: func() []string {
			return []string{"read", "mcp", "tool_search", "computer_use"}
		},
		PlatformName:    func() string { return "cli" },
		LeanMode:        true,
		PlanModeChecker: func() bool { return true },
	})

	names := []string{"read", "mcp", "tool_search", "computer_use"}
	preview := tf.Preview(names)
	if !containsAll(preview.Kept, "read", "tool_search") {
		t.Fatalf("preview kept=%v", preview.Kept)
	}
	if preview.Excluded["mcp"] != "lean" && preview.Excluded["mcp"] != "plan mode" {
		t.Fatalf("mcp reason=%q kept=%v excluded=%v", preview.Excluded["mcp"], preview.Kept, preview.Excluded)
	}
}

func containsAll(have []string, want ...string) bool {
	set := map[string]bool{}
	for _, n := range have {
		set[n] = true
	}
	for _, n := range want {
		if !set[n] {
			return false
		}
	}
	return true
}

func TestToolsetFilterDiscoveryPreservesPolicyExceptLean(t *testing.T) {
	type roleKey struct{}
	names := []string{"read", "mcp", "computer_use", "bash", "scan", "web_fetch", "edit"}
	platform := NewPlatformToolsets("cli")
	platform.SetOverride("cli", []string{"read", "mcp", "computer_use", "bash", "scan", "web_fetch"})
	availability := NewToolAvailability()
	availability.RegisterCheck("computer_use", func() (string, bool) { return "not configured", false })
	planMode := false
	filter := NewToolsetFilter(ToolsetFilterConfig{
		Platform: platform, Availability: availability, LeanMode: true, CyberMode: true,
		ToolNames:  func() []string { return names },
		NameFilter: func(name string) bool { return name != "bash" },
		ContextFilter: func(ctx context.Context, name string) bool {
			return name != "scan" || ctx.Value(roleKey{}) == "recon"
		},
		PlanModeChecker: func() bool { return planMode },
		PlanModeAllowed: map[string]bool{"read": true},
	})
	ctx := context.WithValue(context.Background(), roleKey{}, "recon")
	discovery := filter.DiscoveryPreview(ctx, names)
	if len(discovery.Kept) != 4 || !containsAll(discovery.Kept, "read", "mcp", "scan", "web_fetch") {
		t.Fatalf("unexpected discovery tools: %+v", discovery)
	}
	parent := filter.DiscoveryPreview(context.Background(), names)
	if parent.Excluded["scan"] != "role policy" || parent.Excluded["bash"] != "name filter" || parent.Excluded["computer_use"] == "" || parent.Excluded["edit"] == "" {
		t.Fatalf("discovery bypassed policy: %+v", parent)
	}
	request := &agentcore.ModelCallContext{Request: &agentcore.ProviderRequest{}}
	for _, name := range names {
		request.Request.Tools = append(request.Request.Tools, agentcore.ToolDefinition{Name: name})
	}
	if err := filter.BeforeModelCall(ctx, nil, request); err != nil {
		t.Fatal(err)
	}
	if len(request.Request.Tools) != 1 || request.Request.Tools[0].Name != "read" {
		t.Fatalf("lean schemas were not hidden: %+v", request.Request.Tools)
	}
	planMode = true
	discovery = filter.DiscoveryPreview(ctx, names)
	if len(discovery.Kept) != 1 || discovery.Kept[0] != "read" || discovery.Excluded["mcp"] != "plan mode" {
		t.Fatalf("discovery bypassed dynamic plan policy: %+v", discovery)
	}
}

func TestToolsetFilter_LeanModeHidesLongTail(t *testing.T) {
	platform := NewPlatformToolsets("cli")
	platform.SetOverride("cli", []string{"read", "mcp", "tool_search", "computer_use", "browser"})

	tf := NewToolsetFilter(ToolsetFilterConfig{
		Platform: platform,
		ToolNames: func() []string {
			return []string{"read", "mcp", "tool_search", "computer_use", "browser"}
		},
		PlatformName: func() string { return "cli" },
		LeanMode:     true,
	})

	mcc := &agentcore.ModelCallContext{
		Request: &agentcore.ProviderRequest{
			Tools: []agentcore.ToolDefinition{
				{Name: "read"},
				{Name: "mcp"},
				{Name: "tool_search"},
				{Name: "computer_use"},
				{Name: "browser"},
			},
		},
	}
	if err := tf.BeforeModelCall(context.Background(), &agentcore.AgentRunContext{}, mcc); err != nil {
		t.Fatalf("BeforeModelCall: %v", err)
	}
	kept := map[string]bool{}
	for _, def := range mcc.Request.Tools {
		kept[def.Name] = true
	}
	if !kept["read"] || !kept["tool_search"] || !kept["browser"] {
		t.Errorf("lean mode should keep core + discovery tools, kept=%v", kept)
	}
	if kept["mcp"] || kept["computer_use"] {
		t.Errorf("lean mode should hide long-tail schemas, kept=%v", kept)
	}
}

func TestLeanDirectToolsKeepsCodingLoop(t *testing.T) {
	for _, name := range []string{"read", "edit", "bash", "browser", "sessions_spawn", "sessions_wait", "tool_search", "tool_call"} {
		if !LeanDirectTools[name] {
			t.Errorf("expected %s in LeanDirectTools", name)
		}
	}
	for _, name := range []string{"web_fetch", "open_url", "mcp", "computer_use", "skill"} {
		if LeanDirectTools[name] {
			t.Errorf("did not expect %s in LeanDirectTools", name)
		}
	}
}

func TestCyberOnlyToolsHiddenOutsideCyberMode(t *testing.T) {
	platform := NewPlatformToolsets("cli")
	platform.SetOverride("cli", []string{"full", "cyber"})
	tf := NewToolsetFilter(ToolsetFilterConfig{
		Platform: platform,
		ToolNames: func() []string {
			return []string{"read", "scan", "finding_upsert", "finding_retest", "cyber_storage", "oast", "web_access_matrix", "web_workflow", "web_test_execute"}
		},
		PlatformName: func() string { return "cli" },
	})
	mcc := &agentcore.ModelCallContext{
		Request: &agentcore.ProviderRequest{
			Tools: []agentcore.ToolDefinition{
				{Name: "read"},
				{Name: "scan"},
				{Name: "finding_upsert"},
				{Name: "finding_retest"},
				{Name: "cyber_storage"},
				{Name: "oast"},
				{Name: "web_access_matrix"},
				{Name: "web_workflow"},
				{Name: "web_test_execute"},
			},
		},
	}
	if err := tf.BeforeModelCall(context.Background(), &agentcore.AgentRunContext{}, mcc); err != nil {
		t.Fatalf("BeforeModelCall: %v", err)
	}
	kept := map[string]bool{}
	for _, def := range mcc.Request.Tools {
		kept[def.Name] = true
	}
	if kept["scan"] || kept["finding_upsert"] || kept["finding_retest"] || kept["cyber_storage"] || kept["oast"] {
		t.Fatal("cyber-only tools should be hidden outside cyber mode")
	}
	if !kept["read"] {
		t.Fatal("read should remain visible")
	}
	for _, name := range []string{"web_access_matrix", "web_workflow", "web_test_execute"} {
		if kept[name] {
			t.Fatal("validation tool visible outside cyber mode", name)
		}
	}

	tf.cyberMode = true
	mcc.Request.Tools = []agentcore.ToolDefinition{
		{Name: "read"},
		{Name: "scan"},
		{Name: "finding_upsert"},
		{Name: "finding_retest"},
		{Name: "cyber_storage"},
		{Name: "oast"},
		{Name: "web_access_matrix"},
		{Name: "web_workflow"},
		{Name: "web_test_execute"},
	}
	if err := tf.BeforeModelCall(context.Background(), &agentcore.AgentRunContext{}, mcc); err != nil {
		t.Fatalf("BeforeModelCall cyber: %v", err)
	}
	kept = map[string]bool{}
	for _, def := range mcc.Request.Tools {
		kept[def.Name] = true
	}
	if !kept["scan"] || !kept["finding_upsert"] || !kept["finding_retest"] || !kept["cyber_storage"] || !kept["oast"] {
		t.Fatal("cyber-only tools should be visible in cyber mode")
	}
	for _, name := range []string{"web_access_matrix", "web_workflow", "web_test_execute"} {
		if !kept[name] {
			t.Fatal("validation tool missing in cyber mode", name)
		}
	}
}

func TestLeanCyberToolsVisibleOnlyWhenEnabled(t *testing.T) {
	tf := NewToolsetFilter(ToolsetFilterConfig{LeanMode: true})
	if tf.leanVisible("scan") {
		t.Fatal("scan should stay hidden in lean code/general")
	}
	tf.leanCyber = true
	if !tf.leanVisible("scan") || !tf.leanVisible("web_fetch") || !tf.leanVisible("open_url") || !tf.leanVisible("oast") {
		t.Fatal("lean cyber should keep scan/web schemas")
	}
}

func TestToolsetFilterMCPDiscoveryPolicy(t *testing.T) {
	const remote = "mcp__inventory__lookup"
	platform := NewPlatformToolsets("cli")
	platform.SetOverride("cli", []string{"mcp", "discovery"})
	availability := NewToolAvailability()
	blockedName, blockedContext, plan := "", "", false
	filter := NewToolsetFilter(ToolsetFilterConfig{
		Platform: platform, Availability: availability, LeanMode: true,
		NameFilter:      func(name string) bool { return name != blockedName },
		ContextFilter:   func(_ context.Context, name string) bool { return name != blockedContext },
		PlanModeChecker: func() bool { return plan },
	})
	names := []string{"mcp", remote, "tool_search", "tool_describe", "tool_call"}
	check := func(want bool) {
		t.Helper()
		preview := filter.DiscoveryPreview(context.Background(), names)
		found := false
		for _, name := range preview.Kept {
			found = found || name == remote
		}
		if found != want {
			t.Fatalf("remote allowed=%v want=%v: %+v", found, want, preview)
		}
	}
	check(true)
	for _, name := range []string{"mcp", remote} {
		blockedName = name
		check(false)
		blockedName = ""
		blockedContext = name
		check(false)
		blockedContext = ""
	}
	plan = true
	check(false)
	plan = false
	availability.RegisterCheck(remote, func() (string, bool) { return "not configured", false })
	check(false)
	availability.RegisterCheck(remote, func() (string, bool) { return "", true })
	check(true)
	availability.RegisterCheck("mcp", func() (string, bool) { return "disabled", false })
	filter.InvalidateAvailability()
	check(false)
	availability.RegisterCheck("mcp", func() (string, bool) { return "", true })
	platform.SetOverride("cli", []string{"discovery"})
	filter.InvalidateTools()
	check(false)
}

func TestToolsetFilter_NilNameFilterKeepsAll(t *testing.T) {
	platform := NewPlatformToolsets("cli")
	platform.SetOverride("cli", []string{"read", "bash", "web_fetch"})

	tf := NewToolsetFilter(ToolsetFilterConfig{
		Platform: platform,
		ToolNames: func() []string {
			return []string{"read", "bash", "web_fetch"}
		},
		PlatformName: func() string { return "cli" },
	})

	mcc := &agentcore.ModelCallContext{
		Request: &agentcore.ProviderRequest{
			Tools: []agentcore.ToolDefinition{
				{Name: "read"},
				{Name: "bash"},
				{Name: "web_fetch"},
			},
		},
	}
	if err := tf.BeforeModelCall(context.Background(), &agentcore.AgentRunContext{}, mcc); err != nil {
		t.Fatalf("BeforeModelCall: %v", err)
	}
	if len(mcc.Request.Tools) != 3 {
		t.Errorf("expected all 3 tools kept with nil NameFilter, got %d", len(mcc.Request.Tools))
	}
}
