package slashcmd

import (
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/i18n"
)

// handleMode handles /mode
func handleMode(sctx *SlashContext, parts []string) bool {
	if sctx.Runtime.Busy.Load() {
		sctx.UI.App.PrintSystem(i18n.T("system.busy"))
		return true
	}
	if len(parts) < 2 {
		// List all available modes
		sctx.UI.App.PrintSystem(fmt.Sprintf("Current mode: %s", sctx.Runtime.ActiveMode()))
		sctx.UI.App.PrintSystem("Available modes: general, code")
		for _, cm := range agent.ListCustomModes() {
			desc := cm.Description
			if desc == "" {
				desc = "(no description)"
			}
			sctx.UI.App.PrintSystem(fmt.Sprintf("  %s — %s", cm.Name, desc))
		}
		sctx.UI.App.PrintSystem("Usage: /mode <mode-name>")
		return true
	}
	newMode, ok := agent.ParseMode(parts[1])
	if !ok {
		available := strings.Join(agent.AllModeNames(), ", ")
		sctx.UI.App.PrintError(fmt.Errorf("unknown mode %q: available modes: %s", parts[1], available))
		return true
	}
	sctx.Runtime.SwitchToMode(newMode)
	sctx.UI.App.PrintSystem(i18n.T("system.switched_mode", "mode", string(newMode)))
	return true
}

// handleModel handles /model
func handleModel(sctx *SlashContext, parts []string) bool {
	if sctx.Runtime.Busy.Load() {
		sctx.UI.App.PrintSystem(i18n.T("system.busy"))
		return true
	}
	if len(parts) < 2 {
		sctx.UI.OpenModelPicker()
		return true
	}
	newModel := parts[1]
	sctx.Runtime.SwitchModel(newModel)
	sctx.UI.App.UpdateStatusBar(sctx.Runtime.ProviderType, newModel, string(sctx.Runtime.ActiveMode()))
	sctx.UI.App.PrintSystem(i18n.T("system.switched_model", "model", newModel))
	return true
}

// handleProvider handles /provider
func handleProvider(sctx *SlashContext, parts []string) bool {
	if sctx.Runtime.Busy.Load() {
		sctx.UI.App.PrintSystem(i18n.T("system.busy"))
		return true
	}
	if len(parts) < 2 {
		sctx.UI.App.PrintSystem(fmt.Sprintf("current provider: %s. Usage: /provider <provider-name>", sctx.Runtime.ProviderType))
		sctx.UI.App.PrintSystem("Available: openai, anthropic, gemini, xiaomi, openrouter, custom")
		sctx.UI.App.PrintSystem("For interactive provider/model setup, run: covo-agent model")
		return true
	}
	newProvider := parts[1]
	if err := sctx.Runtime.SwitchProvider(newProvider); err != nil {
		sctx.UI.App.PrintError(err)
		return true
	}
	sctx.UI.App.UpdateStatusBar(newProvider, sctx.Runtime.Model, string(sctx.Runtime.ActiveMode()))
	sctx.UI.App.PrintSystem(i18n.T("system.switched_provider", "provider", newProvider))
	return true
}

// handleReasoning handles /reasoning
func handleReasoning(sctx *SlashContext, parts []string) bool {
	if len(parts) < 2 {
		sctx.UI.App.PrintSystem(fmt.Sprintf("Usage: /reasoning none|low|medium|high|max (current: %s)", sctx.Runtime.State.ReasoningEffort()))
		return true
	}
	effort := strings.ToLower(parts[1])
	validEfforts := map[string]bool{"none": true, "low": true, "medium": true, "high": true, "max": true}
	if !validEfforts[effort] {
		sctx.UI.App.PrintSystem("Invalid effort level. Use: none, low, medium, high, max")
		return true
	}
	sctx.Runtime.State.SetReasoningEffort(effort)
	if ag := sctx.Runtime.Agents.Core(); ag != nil {
		ag.SetThinkingConfig(&agentcore.ThinkingConfig{
			Effort: agentcore.ThinkingEffort(effort),
		})
	}
	sctx.UI.App.PrintSystem(fmt.Sprintf("Reasoning effort set to: %s", effort))
	return true
}

// handlePersonality handles /personality
func handlePersonality(sctx *SlashContext, parts []string) bool {
	if len(parts) < 2 {
		sctx.UI.App.PrintSystem("Usage: /personality <name> | /personality list")
		return true
	}
	sub := strings.ToLower(parts[1])
	if sub == "list" {
		sctx.UI.App.PrintSystem("── Personalities ──")
		for name, p := range sctx.Services.Personalities {
			sctx.UI.App.PrintSystem(fmt.Sprintf("  %-15s — %s", name, p.Description))
		}
		return true
	}
	if sub == "none" {
		sctx.Runtime.State.SetPersonality("")
		sctx.UI.App.PrintSystem("Personality cleared — using default agent behavior")
		return true
	}
	if p, ok := sctx.Services.Personalities[sub]; ok {
		sctx.Runtime.State.SetPersonality(sub)
		sctx.UI.App.PrintSystem(fmt.Sprintf("Personality set to: %s — %s", p.Name, p.Description))
		return true
	}
	sctx.UI.App.PrintSystem(fmt.Sprintf("Unknown personality: %q. Use /personality list to see options.", sub))
	return true
}

// handleBusy handles /busy
func handleBusy(sctx *SlashContext, parts []string) bool {
	if len(parts) < 2 {
		sctx.UI.App.PrintSystem(fmt.Sprintf("Usage: /busy block|queue|interrupt (current: %s)", sctx.Runtime.State.BusyInputMode()))
		return true
	}
	mode := strings.ToLower(parts[1])
	switch mode {
	case "block", "queue", "interrupt":
		sctx.Runtime.State.SetBusyInputMode(mode)
		sctx.UI.App.PrintSystem(fmt.Sprintf("Busy-Enter mode set to: %s", mode))
	default:
		sctx.UI.App.PrintSystem("Invalid mode. Use: block, queue, interrupt")
	}
	return true
}

// handleProfile handles /profile
func handleProfile(sctx *SlashContext, parts []string) bool {
	if len(parts) < 2 {
		sctx.UI.App.PrintSystem(fmt.Sprintf("Usage: /profile minimal|coding|messaging|full (current: %s)", sctx.Runtime.State.ActiveProfile()))
		return true
	}
	name := strings.ToLower(parts[1])
	switch name {
	case "minimal", "coding", "messaging", "full":
		sctx.Runtime.State.SetActiveProfile(name)
		sctx.UI.App.PrintSystem(fmt.Sprintf("Tool profile set to: %s", name))
		if sctx.Runtime.Busy.Load() {
			sctx.UI.App.PrintSystem("(profile will take effect on next agent restart)")
		}
	default:
		sctx.UI.App.PrintSystem("Invalid profile. Use: minimal, coding, messaging, full")
	}
	return true
}

// handleFast handles /fast
func handleFast(sctx *SlashContext, parts []string) bool {
	nowFast := sctx.Runtime.State.ToggleFastMode()
	if ag := sctx.Runtime.Agents.Core(); ag != nil {
		ag.SetFastMode(nowFast)
	}
	if nowFast {
		sctx.UI.App.PrintSystem(i18n.T("system.fast_on"))
	} else {
		sctx.UI.App.PrintSystem(i18n.T("system.fast_off"))
	}
	return true
}

// handleFooter handles /footer
func handleFooter(sctx *SlashContext, parts []string) bool {
	nowFooter := sctx.Runtime.State.ToggleFooterEnabled()
	sctx.Services.NotifyGatewayFooter(sctx.Services.HomeDir, nowFooter)
	if nowFooter {
		sctx.UI.App.PrintSystem(i18n.T("system.footer_on"))
	} else {
		sctx.UI.App.PrintSystem(i18n.T("system.footer_off"))
	}
	return true
}
