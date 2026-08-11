package slashcmd

import (
	"fmt"
	"strings"

	"github.com/covoyage/covo-agent/internal/i18n"
	agenttheme "github.com/covoyage/covo-agent/internal/theme"
)

// handleLanguage handles /language, /lang
func handleLanguage(sctx *SlashContext, parts []string) bool {
	if len(parts) < 2 {
		lang := i18n.DetectSystemLanguage()
		if lang == "" {
			lang = i18n.DefaultLanguage
		}
		name := i18n.DisplayName(lang)
		sctx.UI.App.PrintSystem(i18n.T("system.language_current", "lang", string(lang), "name", name))
		sctx.UI.App.PrintSystem(i18n.T("system.language_usage"))
		return true
	}
	code := parts[1]
	if code == "list" {
		var list []string
		for _, l := range i18n.SupportedLanguages() {
			list = append(list, string(l)+" ("+i18n.DisplayName(l)+")")
		}
		sctx.UI.App.PrintSystem(strings.Join(list, ", "))
		return true
	}
	if !i18n.IsSupported(code) {
		sctx.UI.App.PrintSystem(i18n.T("system.language_unknown", "code", code))
		return true
	}
	lang, _ := i18n.ParseLanguage(code)
	i18n.SetLanguage(lang)
	name := i18n.DisplayName(lang)
	sctx.UI.App.PrintSystem(i18n.T("system.language_switched", "name", name))
	return true
}

// handleTheme handles /theme
func handleTheme(sctx *SlashContext, parts []string) bool {
	if len(parts) < 2 || parts[1] == "list" {
		themes := agenttheme.All()
		var list []string
		for _, p := range themes {
			tag := "dark"
			if !p.Dark {
				tag = "light"
			}
			list = append(list, p.Name+" ("+tag+")")
		}
		sctx.UI.App.PrintSystem("Available themes: " + strings.Join(list, ", "))
		return true
	}
	name := parts[1]
	p := agenttheme.Get(name)
	if p == nil {
		sctx.UI.App.PrintSystem(fmt.Sprintf("Unknown theme: %s. Run /theme list to see available themes.", name))
		return true
	}
	if err := sctx.Services.WriteSkinTheme(sctx.Services.HomeDir, name); err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("write skin: %w", err))
		return true
	}
	sctx.UI.ApplyNamedTheme(name)
	tag := "dark"
	if !p.Dark {
		tag = "light"
	}
	sctx.UI.App.PrintSystem(fmt.Sprintf("Theme set to: %s (%s)", name, tag))
	return true
}

// handleTemplate handles /template
func handleTemplate(sctx *SlashContext, parts []string) bool {
	if len(parts) < 2 {
		sctx.UI.App.PrintSystem("Usage: /template <name> [args...] — available: " + sctx.Services.TemplateList(sctx.Services.HomeDir))
		return true
	}
	name := parts[1]
	content, err := sctx.Services.ReadTemplate(sctx.Services.HomeDir, name)
	if err != nil {
		sctx.UI.App.PrintSystem(fmt.Sprintf("Template %q not found. Create it with: covo-agent template edit %s", name, name))
		return true
	}
	if len(parts) > 2 {
		content = sctx.Services.ExpandTemplateArgs(content, parts[2:])
	}
	if ed := sctx.UI.App.Editor(); ed != nil {
		ed.SetValue(content)
	}
	return true
}
