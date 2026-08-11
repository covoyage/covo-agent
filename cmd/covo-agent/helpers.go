package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/evolution"
	"github.com/covoyage/covo-agent/internal/i18n"
	agenttools "github.com/covoyage/covo-agent/internal/tools"
)

func mcpAgentConfig(cfg *cli.Config) map[string]agenttools.MCPConfig {
	if cfg == nil || cfg.MCPServers == nil {
		return nil
	}
	m := make(map[string]agenttools.MCPConfig, len(cfg.MCPServers))
	for name, srv := range cfg.MCPServers {
		m[name] = agenttools.MCPConfig{
			Command: srv.Command,
			Args:    srv.Args,
			Env:     srv.Env,
			Timeout: srv.Timeout,
		}
	}
	return m
}

func curatorConfig(cfg *cli.Config) evolution.CuratorConfig {
	if cfg.Curator != nil {
		return evolution.CuratorConfig{
			Enabled:          cfg.Curator.Enabled,
			IntervalHours:    cfg.Curator.IntervalHours,
			StaleAfterDays:   cfg.Curator.StaleAfterDays,
			ArchiveAfterDays: cfg.Curator.ArchiveAfterDays,
		}
	}
	return evolution.CuratorConfig{
		Enabled:          true,
		IntervalHours:    168,
		StaleAfterDays:   30,
		ArchiveAfterDays: 90,
	}
}

// auxiliaryConfigFromCLI converts cli.AuxiliaryConfig to agent.AuxiliaryConfig.
// Returns nil when no auxiliary config is set, so the agent falls back to the
// main provider/model for all auxiliary tasks.
func auxiliaryConfigFromCLI(cfg *cli.Config) *agent.AuxiliaryConfig {
	if cfg == nil || cfg.Auxiliary == nil {
		return nil
	}
	return &agent.AuxiliaryConfig{
		Compression: convertAuxModel(cfg.Auxiliary.Compression),
		Vision:      convertAuxModel(cfg.Auxiliary.Vision),
		WebExtract:  convertAuxModel(cfg.Auxiliary.WebExtract),
		Title:       convertAuxModel(cfg.Auxiliary.Title),
		Review:      convertAuxModel(cfg.Auxiliary.Review),
	}
}

func convertAuxModel(m *cli.AuxiliaryModelConfig) *agent.AuxiliaryModelConfig {
	if m == nil {
		return nil
	}
	return &agent.AuxiliaryModelConfig{
		Model:    m.Model,
		Provider: m.Provider,
		BaseURL:  m.BaseURL,
		APIKey:   m.APIKey,
	}
}

func parseFallbackProviders() []string {
	raw := os.Getenv("FALLBACK_PROVIDER")
	if raw == "" {
		raw = os.Getenv("FALLBACK_PROVIDERS")
	}
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func defaultMouseMode() string {
	if mode := os.Getenv("COVO_MOUSE_MODE"); mode != "" {
		return mode
	}
	return "sgr"
}

func defaultKeyboardMode() string {
	if mode := os.Getenv("COVO_KITTY_KEYBOARD_MODE"); mode != "" {
		return mode
	}
	if runtime.GOOS == "darwin" {
		return "on"
	}
	return "auto"
}

func defaultKeyboardFlags() int64 {
	// flag 1 = disambiguate escape codes. This alone suffices for modifier-
	// rich keys (Cmd+C, Cmd+A, …) since combos without a legacy encoding —
	// and the Super modifier has none — are reported as CSI u regardless.
	// Avoid flag 8 (report ALL keys as CSI u): it forces printable characters
	// (including IME-committed CJK text) into CSI u encoding, which breaks
	// macOS Chinese input methods that expect raw UTF-8 text delivery.
	return 1
}

func chatMessageFromAgentMessage(msg agentcore.Message) chat.ChatMessage {
	role := chat.RoleSystem
	switch msg.Role {
	case agentcore.RoleUser:
		role = chat.RoleUser
	case agentcore.RoleAssistant:
		role = chat.RoleAssistant
	case agentcore.RoleTool:
		role = chat.RoleTool
	case agentcore.RoleSystem:
		role = chat.RoleSystem
	}
	text := msg.Content
	if text == "" && len(msg.ToolCalls) > 0 {
		text = i18n.T("statusline.tool_calls", "count", fmt.Sprintf("%d", len(msg.ToolCalls)))
	}
	return chat.ChatMessage{
		Role: role,
		Text: text,
	}
}

// restoreChatHistory clears the app's chat history and re-appends messages
// from a session snapshot, skipping internal system messages (agent identity,
// rules, memory guidance, etc.) that are not meant for display.
func restoreChatHistory(app *chat.ChatApp, msgs []agentcore.Message) {
	app.History().Clear()
	for _, msg := range msgs {
		if msg.Role == agentcore.RoleSystem {
			continue
		}
		app.History().Append(chatMessageFromAgentMessage(msg))
	}
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
