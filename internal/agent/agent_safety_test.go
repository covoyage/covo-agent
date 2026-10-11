package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/covoyage/covo-agent/internal/agent/approval"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
	"github.com/covoyage/covonaut/agentcore"
)

func TestNonInteractiveCyberRemoteExecutionRequiresPolicy(t *testing.T) {
	system := approval.NewSystem(approval.Config{Mode: "manual", StorageDir: t.TempDir()})
	system.SetNonInteractive(true)
	agent := &CovoAgent{mode: ModeCyber, approvalSystem: system}
	if err := agent.permissionGateBeforeHook()(context.Background(), &agentcore.HookContext{ToolName: "remote_node_exec"}); err == nil {
		t.Fatal("non-interactive child executed an unapproved remote command")
	}
}

func TestCommandApprovalDenialIsFinal(t *testing.T) {
	ctx := context.Background()
	command := `python3 -c 'print("hello")'`
	system := approval.NewSystem(approval.Config{Mode: "manual", StorageDir: t.TempDir(), NonInteractive: true})
	agent := &CovoAgent{mode: ModeGeneral, approvalSystem: system}
	args, _ := json.Marshal(map[string]string{"command": command})
	hook := agent.permissionGateBeforeHook()
	if err := hook(ctx, &agentcore.HookContext{ToolName: "bash", Arguments: args}); err == nil {
		t.Fatal("non-interactive deny became allowed")
	}
	agent.permissionChecker = func(context.Context, string, []byte) bool { t.Fatal("final denial reached requester"); return true }
	if err := hook(ctx, &agentcore.HookContext{ToolName: "bash", Arguments: args}); err == nil {
		t.Fatal("requester overrode denial")
	}
}

func TestAutomaticApprovalCannotSkipExplicitChecker(t *testing.T) {
	system := approval.NewSystem(approval.Config{Mode: "manual", StorageDir: t.TempDir()})
	calls := 0
	agent := &CovoAgent{mode: ModeGeneral, approvalSystem: system, permissionChecker: func(context.Context, string, []byte) bool { calls++; return false }}
	if err := agent.permissionGateBeforeHook()(context.Background(), &agentcore.HookContext{ToolName: "bash", Arguments: json.RawMessage(`{"command":"ls -la"}`)}); err == nil {
		t.Fatal("safe command bypassed explicit denial")
	}
	if calls != 1 {
		t.Fatalf("checker calls=%d", calls)
	}
}

func TestMutationSafetyCoversEveryPatchTarget(t *testing.T) {
	agent := &CovoAgent{mode: ModeGeneral, homeDir: t.TempDir(), workDir: t.TempDir(), workspaceOnly: true}
	inside := filepath.Join(agent.workDir, "allowed.txt")
	outside := filepath.Join(t.TempDir(), "blocked.txt")
	for _, patch := range []string{
		"*** Begin Patch\n*** Add File: " + inside + "\n+safe\n*** Add File: " + outside + "\n+unsafe\n*** End Patch",
		"*** Begin Patch\n*** Move File: " + inside + " -> " + outside + "\n*** End Patch",
	} {
		args, _ := json.Marshal(map[string]string{"patch": patch})
		if err := agent.fileSafetyBeforeHook()(context.Background(), &agentcore.HookContext{ToolName: "apply_patch", Arguments: args}); err == nil {
			t.Fatal("patch target escaped workspace")
		}
	}
	for _, name := range []string{"write", "append_file", "delete_file"} {
		args, _ := json.Marshal(map[string]string{"path": outside, "file_path": outside})
		if err := agent.fileSafetyBeforeHook()(context.Background(), &agentcore.HookContext{ToolName: name, Arguments: args}); err == nil {
			t.Fatalf("%s escaped workspace", name)
		}
	}
	args, _ := json.Marshal(map[string]string{"patch": "*** Begin Patch\n*** Add File: allowed.txt\n+safe\n*** End Patch"})
	if err := agent.fileSafetyBeforeHook()(context.Background(), &agentcore.HookContext{ToolName: "apply_patch", Arguments: args}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteAndMoveUseRegisteredSchemaSafety(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	agent := &CovoAgent{workDir: workspace, workspaceOnly: true}
	for _, test := range []struct {
		name string
		args map[string]string
	}{
		{"delete", map[string]string{"path": outside}},
		{"move", map[string]string{"source": "inside.txt", "dest": outside}},
		{"move", map[string]string{"source": outside, "dest": "inside.txt"}},
	} {
		arguments, _ := json.Marshal(test.args)
		if err := agent.fileSafetyBeforeHook()(context.Background(), &agentcore.HookContext{ToolName: test.name, Arguments: arguments}); err == nil {
			t.Fatalf("%s schema bypassed workspace safety: %s", test.name, arguments)
		}
	}
}

func TestCyberChildRoleCannotBecomeParent(t *testing.T) {
	if role, err := resolveCyberChildRole(ModeCyber, ""); err != nil || role != "leaf" {
		t.Fatalf("empty child role=%q err=%v", role, err)
	}
	if _, err := resolveCyberChildRole(ModeCyber, "parent"); err == nil {
		t.Fatal("parent cyber role accepted for child")
	}
	if role, err := resolveCyberChildRole(ModeCyber, "verifier"); err != nil || role != "verifier" {
		t.Fatalf("verifier role=%q err=%v", role, err)
	}
}

func TestAutomaticVerifierCannotEscapeIntakeEndpoint(t *testing.T) {
	scope := toolssubagent.IntakeScope{Endpoint: "GET https://example.test/orders/1", ExchangeID: "ex-1"}
	for _, call := range []struct {
		name string
		args string
		want bool
	}{
		{"exchange_get", `{"id":"ex-1"}`, true},
		{"exchange_get", `{"id":"ex-2"}`, false},
		{"exchange_search", `{"host":"other.test"}`, false},
		{"exchange_replay", `{"id":"ex-1","url":"https://example.test/orders/1?id=2"}`, true},
		{"exchange_replay", `{"id":"ex-1","url":"https://example.test/admin"}`, false},
		{"exchange_replay", `{"id":"ex-1","method":"POST"}`, false},
		{"exchange_compare", `{"id":"ex-2"}`, false},
		{"scan", `{"recipe":"nmap","target":"example.test"}`, false},
		{"sessions_spawn", `{"task":"scan"}`, false},
		{"tool_call", `{"name":"scan"}`, false},
		{"coverage_upsert", `{"target":"outside.test","surface":"/"}`, false},
		{"coverage_upsert", `{"surface":"/orders/1","outcome":"clean"}`, false},
	} {
		context := toolssubagent.WithIntakeScope(context.Background(), scope)
		agent := &CovoAgent{mode: ModeCyber}
		err := agent.permissionGateBeforeHook()(context, &agentcore.HookContext{ToolName: call.name, Arguments: json.RawMessage(call.args)})
		if (err == nil) != call.want {
			t.Errorf("%s(%s) allowed=%v want=%v: %v", call.name, call.args, err == nil, call.want, err)
		}
	}
}

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "https allowed", url: "https://example.com", wantErr: false},
		{name: "https github", url: "https://github.com/covoyage/covo-agent", wantErr: false},
		{name: "https with path", url: "https://github.com/covoyage/covo-agent/blob/main/README.md", wantErr: false},
		{name: "file blocked", url: "file:///etc/passwd", wantErr: true},
		{name: "ftp blocked", url: "ftp://evil.com/malware", wantErr: true},
		{name: "localhost blocked", url: "http://localhost:8080", wantErr: true},
		{name: "127.0.0.1 blocked", url: "http://127.0.0.1/api", wantErr: true},
		{name: "0.0.0.0 blocked", url: "http://0.0.0.0:3000", wantErr: true},
		{name: "10.x blocked", url: "http://10.0.0.1/internal", wantErr: true},
		{name: "172.16.x blocked", url: "http://172.16.0.1/", wantErr: true},
		{name: "192.168.x blocked", url: "http://192.168.1.1/admin", wantErr: true},
		{name: "169.254.x blocked", url: "http://169.254.169.254/latest/meta-data/", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateURL(%q) error=%v wantErr=%v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "safety-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	subDir := filepath.Join(tmpDir, "workspace")
	os.MkdirAll(subDir, 0755)

	tests := []struct {
		name    string
		base    string
		rel     string
		wantErr bool
	}{
		{name: "safe within base", base: subDir, rel: "file.txt", wantErr: false},
		{name: "safe subdirectory", base: subDir, rel: "src/main.go", wantErr: false},
		{name: "traversal blocked", base: subDir, rel: "../etc/passwd", wantErr: true},
		{name: "absolute blocked", base: subDir, rel: "/etc/passwd", wantErr: true},
		{name: "double dot traversal", base: subDir, rel: "../../root/.bashrc", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidatePath(tt.base, tt.rel)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePath(%q, %q) error=%v wantErr=%v", tt.base, tt.rel, err, tt.wantErr)
			}
		})
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"169.254.1.1", true},
		{"100.100.100.200", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"93.184.216.34", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			got := IsPrivateIP(tt.ip)
			if got != tt.want {
				t.Errorf("IsPrivateIP(%q)=%v want=%v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestIsSensitivePath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/etc/passwd", true},
		{"/etc/shadow", true},
		{"/root/.ssh/id_rsa", true},
		{"/home/user/.ssh/id_rsa", true},
		{"/Users/me/.aws/credentials", true},
		{"/proc/cpuinfo", true},
		{"/var/log/app.log", false},
		{"/home/user/projects/main.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := IsSensitivePath(tt.path)
			if got != tt.want {
				t.Errorf("IsSensitivePath(%q)=%v want=%v", tt.path, got, tt.want)
			}
		})
	}
}

func TestSanitizeURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "strip credentials",
			raw:  "https://user:pass@example.com/secret",
			want: "https://example.com/secret",
		},
		{
			name: "clean URL unchanged",
			raw:  "https://example.com/path",
			want: "https://example.com/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeURL(tt.raw)
			if got != tt.want {
				t.Errorf("SanitizeURL(%q)=%q want=%q", tt.raw, got, tt.want)
			}
		})
	}
}
