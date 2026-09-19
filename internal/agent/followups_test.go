package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestParseFollowUpLines(t *testing.T) {
	got := ParseFollowUpLines("1. Add tests for auth\n- Check error paths\n* Review the diff\nAdd tests for auth\n")
	if len(got) != 3 {
		t.Fatalf("got %v", got)
	}
	if got[0] != "Add tests for auth" || got[1] != "Check error paths" || got[2] != "Review the diff" {
		t.Fatalf("got %v", got)
	}
}

func TestParseFollowUpLinesDropsHeadingsAndCopiedText(t *testing.T) {
	raw := strings.Join([]string{
		"## 项目分析: OpenAI Codex CLI",
		"### 项目概述",
		"**Codex CLI** 是 OpenAI 开发的本地编码代理，可以在你的计算机上直接运行。它支持通过 ChatGPT 订阅或 API Key 使用。",
		"111 个 Cargo crate**, 4,464 个 Rust 文件, 753 个 TS 文件",
		"总代码量约 **",
		"对比 covo 和 Codex 的权限模型",
		"列出还缺的测试再补上",
		"hi",
	}, "\n")
	got := ParseFollowUpLines(raw)
	if len(got) != 2 || got[0] != "对比 covo 和 Codex 的权限模型" || got[1] != "列出还缺的测试再补上" {
		t.Fatalf("got %v", got)
	}
}

func TestGenerateFollowUpsDropsStatsDump(t *testing.T) {
	main := &mockAuxProvider{content: "111 个 Cargo crate**, 4,464 个 Rust 文件\n总代码量约 **\n"}
	ac := NewAuxiliaryClient(main, "gpt-4", nil, nil, nil)
	msgs := []agentcore.Message{
		{Role: agentcore.RoleUser, Content: "分析这个项目"},
		{Role: agentcore.RoleAssistant, Content: "111 个 Cargo crate, 4,464 个 Rust 文件"},
	}
	if got := GenerateFollowUps(context.Background(), ac, msgs); got != nil {
		t.Fatalf("expected fallback, got %v", got)
	}
}

func TestFilterCopiedFollowUps(t *testing.T) {
	assistant := "Codex CLI 是 OpenAI 开发的本地编码代理，可以在你的计算机上直接运行。"
	got := filterCopiedFollowUps([]string{
		"Codex CLI 是 OpenAI 开发的本地编码代理",
		"补一份权限模型对照表",
	}, "分析这个项目", assistant)
	if len(got) != 1 || got[0] != "补一份权限模型对照表" {
		t.Fatalf("got %v", got)
	}
}

func TestGenerateFollowUpsUsesAuxiliaryClient(t *testing.T) {
	main := &mockAuxProvider{content: "Run the unit tests\nOpen a pull request\n"}
	ac := NewAuxiliaryClient(main, "gpt-4", nil, nil, nil)
	msgs := []agentcore.Message{
		{Role: agentcore.RoleUser, Content: "fix login"},
		{Role: agentcore.RoleAssistant, Content: "updated auth.go"},
	}
	got := GenerateFollowUps(context.Background(), ac, msgs)
	if len(got) != 2 || got[0] != "Run the unit tests" || got[1] != "Open a pull request" {
		t.Fatalf("got %v", got)
	}
	if main.callsCount() != 1 {
		t.Fatalf("expected one aux call, got %d", main.callsCount())
	}
}

func TestGenerateFollowUpsNilClient(t *testing.T) {
	if got := GenerateFollowUps(context.Background(), nil, nil); got != nil {
		t.Fatalf("got %v", got)
	}
}

func TestLastExchangeSkipsEmpty(t *testing.T) {
	user, assistant := lastExchange([]agentcore.Message{
		{Role: agentcore.RoleUser, Content: "hello"},
		{Role: agentcore.RoleAssistant, Content: "   "},
		{Role: agentcore.RoleAssistant, Content: "done"},
	})
	if user != "hello" || assistant != "done" {
		t.Fatalf("user=%q assistant=%q", user, assistant)
	}
	if !strings.Contains(formatFollowUpInput(user, assistant), "hello") {
		t.Fatal("expected user snippet")
	}
}
