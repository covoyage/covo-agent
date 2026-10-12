package commands

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/agent/approval"
)

func TestApprovalPatternKeyIsPerCall(t *testing.T) {
	nmap, _ := json.Marshal(map[string]string{"recipe": "nmap", "target": "10.0.0.8"})
	hashcat, _ := json.Marshal(map[string]string{"recipe": "hashcat", "target": "ntlm.txt"})

	if got := approvalPatternKey("scan", nmap); !strings.HasPrefix(got, "tool:scan:") {
		t.Errorf("nmap key = %q", got)
	}
	if got := approvalPatternKey("scan", hashcat); !strings.HasPrefix(got, "tool:scan:") {
		t.Errorf("hashcat key = %q", got)
	}
	if approvalPatternKey("scan", nmap) == approvalPatternKey("scan", hashcat) {
		t.Fatal("approving one recipe must not match another")
	}
	write, _ := json.Marshal(map[string]string{"path": "a.go"})
	if got := approvalPatternKey("write_file", write); got != "tool:write_file" {
		t.Errorf("non-scan key = %q", got)
	}
	if got := approvalPatternKey("scan", []byte(`{}`)); !strings.HasPrefix(got, "tool:scan:") {
		t.Errorf("recipe-less key = %q", got)
	}
	other, _ := json.Marshal(map[string]string{"recipe": "nmap", "target": "10.0.0.9"})
	if approvalPatternKey("scan", nmap) == approvalPatternKey("scan", other) {
		t.Fatal("approval was reused across different target arguments")
	}
	module := []byte(`{"recipe":"metasploit","target":"auxiliary/scanner/smb/smb_version","options":{"RHOSTS":"10.0.0.8","PASSWORD":"private-value"}}`)
	otherModule := []byte(`{"recipe":"metasploit","target":"auxiliary/scanner/smb/smb_version","options":{"RHOSTS":"10.0.0.9","PASSWORD":"private-value"}}`)
	if approvalPatternKey("scan", module) == approvalPatternKey("scan", otherModule) || strings.Contains(approvalPatternKey("scan", module), "private-value") {
		t.Fatal("approval reused a different module target or exposed credentials")
	}
}

func TestActiveCyberApprovalModes(t *testing.T) {
	for _, name := range []string{"scan", "scan_profile", "request_intake", "web_param_probe", "exchange_replay", "exchange_compare", "csrf_retest", "web_workflow", "web_test_execute", "remote_node_exec"} {
		if !dangerousTools[name] {
			t.Errorf("%s bypasses approval", name)
		}
	}
	for _, name := range []string{"web_workflow", "web_test_execute"} {
		first, second := []byte(`{"id":"ex-1","concurrency":2}`), []byte(`{"id":"ex-2","concurrency":4}`)
		if approvalPatternKey(name, first) == approvalPatternKey(name, second) || approvalPatternKey(name, first) == "tool:"+name {
			t.Fatal("approval reused across validation plans", name)
		}
		if readOnlyCyberToolCall(name, first) {
			t.Fatal("active validation classified read-only", name)
		}
	}
	for _, call := range []struct {
		name, args string
		readOnly   bool
	}{
		{"scan", `{"recipe":"list"}`, true},
		{"scan_profile", `{}`, true},
		{"scan_profile", `{"allowed_hosts":["example.test"]}`, false},
		{"request_intake", `{"action":"status"}`, true},
		{"request_intake", `{"action":"enable","hosts":["example.test"]}`, false},
	} {
		if got := readOnlyCyberToolCall(call.name, []byte(call.args)); got != call.readOnly {
			t.Errorf("%s(%s) read-only = %t, want %t", call.name, call.args, got, call.readOnly)
		}
	}
}

func TestExecutionAndMutationToolsRequireApproval(t *testing.T) {
	gate := NewPermissionGate(nil)
	for _, name := range []string{"sandbox", "mcp", "remote_exec", "apply_patch", "append_file"} {
		if gate.Checker()(context.Background(), name, []byte(`{"command":"echo test","action":"connect"}`)) {
			t.Errorf("%s bypassed approval", name)
		}
	}
	if !gate.Checker()(context.Background(), "mcp", []byte(`{"action":"list"}`)) {
		t.Fatal("read-only MCP list requires approval")
	}
}

func TestCancelledApprovalCallbackCannotApproveNextRequest(t *testing.T) {
	gate := NewPermissionGate(nil)
	callbacks := make(chan func(ApprovalChoice), 2)
	gate.showApprovalOverlay = func(_ string, choose func(ApprovalChoice), _ func()) { callbacks <- choose }
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan bool, 1)
	go func() { first <- gate.Checker()(ctx, "write_file", []byte(`{"path":"first"}`)) }()
	old := <-callbacks
	cancel()
	if <-first {
		t.Fatal("cancelled request approved")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	second := make(chan bool, 1)
	go func() { second <- gate.Checker()(ctx2, "write_file", []byte(`{"path":"second"}`)) }()
	current := <-callbacks
	old(ChoiceOnce)
	gate.mu.Lock()
	queued := len(gate.ch)
	gate.mu.Unlock()
	if queued != 0 {
		t.Fatal("stale callback reached new request")
	}
	current(ChoiceOnce)
	current(ChoiceOnce)
	if !<-second {
		t.Fatal("current request not approved")
	}
}

func TestPolicyAskOverridesAutomaticApproval(t *testing.T) {
	gate := NewPermissionGate(nil)
	gate.YoloMode = true
	ctx := approval.WithRequiredApproval(context.Background())
	if gate.Checker()(ctx, "read_file", []byte(`{"path":"deps.lock"}`)) {
		t.Fatal("policy ask bypassed by read-only or YOLO classification")
	}
}

func TestCyberApprovalSummaryOmitsCredentials(t *testing.T) {
	for _, check := range []struct {
		name, args, wanted, forbidden string
	}{
		{"scan_profile", `{"allowed_hosts":["example.test"]}`, "example.test", "password"},
		{"request_intake", `{"action":"enable","hosts":["example.test"]}`, "example.test", "password"},
		{"exchange_replay", `{"id":"ex-9","url":"https://example.test/a?token=secret-value","headers":{"Authorization":"Bearer secret-value"}}`, "ex-9", "secret-value"},
		{"remote_node_exec", `{"id":"edge","command":"echo secret-value"}`, "edge", "secret-value"},
	} {
		summary := buildSummary(check.name, []byte(check.args))
		if !strings.Contains(summary, check.wanted) || strings.Contains(summary, check.forbidden) {
			t.Errorf("%s approval summary=%q", check.name, summary)
		}
	}
}

func TestApprovalPatternKeySanitizesRecipe(t *testing.T) {
	hostile, _ := json.Marshal(map[string]string{"recipe": "../../etc/passwd; rm -rf /"})
	got := approvalPatternKey("scan", hostile)
	if strings.ContainsAny(got, "/; .") {
		t.Errorf("recipe text leaked into key structure: %q", got)
	}
	if !strings.HasPrefix(got, "tool:scan:") {
		t.Errorf("key = %q", got)
	}
	long, _ := json.Marshal(map[string]string{"recipe": strings.Repeat("a", 200)})
	if len(approvalPatternKey("scan", long)) > len("tool:scan:")+24 {
		t.Errorf("key not bounded: %d bytes", len(approvalPatternKey("scan", long)))
	}
}

func TestScanRecipeNameIsCaseInsensitive(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"recipe": " LIST "})
	if got := scanRecipeName(args); got != "list" {
		t.Errorf("scanRecipeName = %q, want list", got)
	}
	if got := scanRecipeName([]byte(`{`)); got != "" {
		t.Errorf("malformed args = %q, want empty", got)
	}
}

func TestBuildSummaryNamesTheRecipe(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"recipe": "metasploit", "target": "auxiliary/scanner/smb/smb_version"})
	summary := buildSummary("scan", args)
	if !strings.Contains(summary, "metasploit") {
		t.Errorf("summary hides the recipe being approved: %q", summary)
	}
	if !strings.Contains(summary, "smb_version") {
		t.Errorf("summary hides the target: %q", summary)
	}
	secondary, _ := json.Marshal(map[string]any{"recipe": "metasploit", "target": "auxiliary/scanner/smb/smb_version", "options": map[string]string{"RHOSTS": "allowed.test", "PASSWORD": "secret-pass"}})
	summary = buildSummary("scan", secondary)
	if !strings.Contains(summary, "allowed.test") || strings.Contains(summary, "secret-pass") {
		t.Errorf("summary hid a secondary host or exposed a password: %q", summary)
	}

	longTarget := strings.Repeat("u", 200)
	args, _ = json.Marshal(map[string]string{"recipe": "nmap", "target": longTarget})
	if len(buildSummary("scan", args)) > 120 {
		t.Errorf("summary not bounded: %d bytes", len(buildSummary("scan", args)))
	}
}

func TestCSRFRetestRequiresVisibleApproval(t *testing.T) {
	if !dangerousTools["csrf_retest"] {
		t.Fatal("csrf_retest bypassed approval")
	}
	summary := buildSummary("csrf_retest", []byte(`{"page_id":"ex-1","submit_id":"ex-2"}`))
	if !strings.Contains(summary, "ex-1") || !strings.Contains(summary, "ex-2") || !strings.Contains(summary, "POST") {
		t.Fatalf("approval prompt does not identify the submission: %q", summary)
	}
}

func TestActiveReplayToolsRequireApproval(t *testing.T) {
	for _, name := range []string{"exchange_replay", "exchange_compare", "csrf_retest"} {
		if !dangerousTools[name] {
			t.Errorf("%s bypassed approval", name)
		}
	}
}
