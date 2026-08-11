package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/i18n"
	agentui "github.com/covoyage/covo-agent/internal/tui"
	agentpanels "github.com/covoyage/covo-agent/internal/tui/panels"
	"github.com/covoyage/covonaut/tui/chat"
)

type cliModelPickerBackend struct {
	cfg *cli.Config
}

func newCLIModelPickerBackend(cfg *cli.Config) *cliModelPickerBackend {
	return &cliModelPickerBackend{cfg: cfg}
}

func (b *cliModelPickerBackend) ListProviders() []agentpanels.ProviderOption {
	types := cli.RegisteredProviderTypes()
	sort.Slice(types, func(i, j int) bool {
		iCustom := types[i] == "custom" || strings.HasPrefix(types[i], "custom_")
		jCustom := types[j] == "custom" || strings.HasPrefix(types[j], "custom_")
		if iCustom || jCustom {
			return iCustom && !jCustom
		}
		icfg := cli.HasProviderConfiguredFor(cli.ProviderName(types[i]))
		jcfg := cli.HasProviderConfiguredFor(cli.ProviderName(types[j]))
		if icfg != jcfg {
			return icfg
		}
		oi, hasI := providerOrder[types[i]]
		oj, hasJ := providerOrder[types[j]]
		if hasI != hasJ {
			return hasI
		}
		if hasI && hasJ {
			return oi < oj
		}
		return types[i] < types[j]
	})

	out := make([]agentpanels.ProviderOption, 0, len(types))
	for _, t := range types {
		out = append(out, agentpanels.ProviderOption{
			Type:        t,
			DisplayName: cli.ProviderDisplayName(t),
			Configured:  cli.HasProviderConfiguredFor(cli.ProviderName(t)),
		})
	}
	return out
}

func (b *cliModelPickerBackend) ProviderDisplayName(providerType string) string {
	return cli.ProviderDisplayName(providerType)
}

func (b *cliModelPickerBackend) NormalizeProvider(providerType string) string {
	return cli.ProviderName(providerType)
}

func (b *cliModelPickerBackend) DefaultModel(providerType string) string {
	return cli.DefaultModel(providerType)
}

func (b *cliModelPickerBackend) ProviderAPIKeyEnv(providerType string) string {
	return cli.ProviderAPIKeyEnv(providerType)
}

func (b *cliModelPickerBackend) HasProviderConfigured(providerType string) bool {
	return cli.HasProviderConfiguredFor(providerType)
}

func (b *cliModelPickerBackend) FetchProviderModels(providerType string) ([]agentpanels.ProviderModel, error) {
	models, err := cli.FetchProviderModels(providerType)
	if err != nil {
		return nil, err
	}
	return toPanelProviderModels(models), nil
}

func (b *cliModelPickerBackend) FetchCustomProviderModels(provider agentpanels.CustomProvider) ([]agentpanels.ProviderModel, error) {
	apiKey := os.Getenv(provider.APIKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("API key not set")
	}

	var (
		models []cli.ProviderModel
		err    error
	)
	switch provider.Protocol {
	case "anthropic":
		models, err = cli.FetchCustomAnthropicModels(provider.BaseURL, apiKey)
	case "gemini":
		models, err = cli.FetchCustomGeminiModels(provider.BaseURL, apiKey)
	default:
		models, err = cli.FetchCustomOpenAIModels(provider.BaseURL, apiKey)
	}
	if err != nil {
		return nil, err
	}
	return toPanelProviderModels(models), nil
}

func (b *cliModelPickerBackend) SaveAPIKey(env, value string) error {
	os.Setenv(env, value)
	return cli.SaveEnvValue(env, value)
}

func (b *cliModelPickerBackend) CustomProviderTypeName(name string) string {
	return cli.ProviderTypeName(name)
}

func (b *cliModelPickerBackend) GetCustomProvider(providerType string) (agentpanels.CustomProvider, bool) {
	if b.cfg == nil {
		return agentpanels.CustomProvider{}, false
	}
	for i := range b.cfg.CustomProviders {
		cp := b.cfg.CustomProviders[i]
		if cp.TypeName() != providerType {
			continue
		}
		return agentpanels.CustomProvider{
			Name:      cp.Name,
			Protocol:  cp.Protocol,
			BaseURL:   cp.BaseURL,
			APIKeyEnv: cp.APIKeyEnv,
			Models:    toPanelCustomModels(cp.Models),
		}, true
	}
	return agentpanels.CustomProvider{}, false
}

func (b *cliModelPickerBackend) DeleteCustomProvider(providerType string) error {
	if b.cfg == nil {
		return nil
	}
	remaining := make([]cli.CustomProvider, 0, len(b.cfg.CustomProviders))
	for _, cp := range b.cfg.CustomProviders {
		if cp.TypeName() != providerType {
			remaining = append(remaining, cp)
		}
	}
	b.cfg.CustomProviders = remaining
	if err := cli.SaveConfig(b.cfg); err != nil {
		return err
	}
	cli.UnregisterProvider(providerType)
	return nil
}

func (b *cliModelPickerBackend) UpsertCustomProvider(provider agentpanels.CustomProvider) (string, error) {
	if b.cfg == nil {
		return "", nil
	}
	cp := cli.CustomProvider{
		Name:      provider.Name,
		Protocol:  provider.Protocol,
		BaseURL:   provider.BaseURL,
		APIKeyEnv: provider.APIKeyEnv,
		Models:    toCLICustomModels(provider.Models),
	}
	if b.cfg.CustomProviders == nil {
		b.cfg.CustomProviders = make([]cli.CustomProvider, 0)
	}

	updated := false
	oldTypeName := ""
	for i := range b.cfg.CustomProviders {
		if b.cfg.CustomProviders[i].APIKeyEnv == cp.APIKeyEnv {
			oldTypeName = b.cfg.CustomProviders[i].TypeName()
			b.cfg.CustomProviders[i] = cp
			updated = true
			break
		}
	}
	if !updated {
		b.cfg.CustomProviders = append(b.cfg.CustomProviders, cp)
	}
	if oldTypeName != "" && oldTypeName != cp.TypeName() {
		cli.UnregisterProvider(oldTypeName)
	}
	if err := cli.SaveConfig(b.cfg); err != nil {
		return "", err
	}
	cli.RegisterCustomProviders(b.cfg)
	return cp.TypeName(), nil
}

func (b *cliModelPickerBackend) ScopedModels() []string {
	if b.cfg == nil || len(b.cfg.ScopedModels) == 0 {
		return nil
	}
	out := make([]string, len(b.cfg.ScopedModels))
	copy(out, b.cfg.ScopedModels)
	return out
}

func toPanelProviderModels(models []cli.ProviderModel) []agentpanels.ProviderModel {
	if len(models) == 0 {
		return nil
	}
	out := make([]agentpanels.ProviderModel, 0, len(models))
	for _, m := range models {
		out = append(out, agentpanels.ProviderModel{
			ID:          m.ID,
			Name:        m.Name,
			Description: m.Description,
			Context:     m.Context,
		})
	}
	return out
}

func toPanelCustomModels(models []cli.CustomModel) []agentpanels.CustomModel {
	if len(models) == 0 {
		return nil
	}
	out := make([]agentpanels.CustomModel, 0, len(models))
	for _, m := range models {
		out = append(out, agentpanels.CustomModel{
			Name:    m.Name,
			ID:      m.ID,
			Context: m.Context,
		})
	}
	return out
}

func toCLICustomModels(models []agentpanels.CustomModel) []cli.CustomModel {
	if len(models) == 0 {
		return nil
	}
	out := make([]cli.CustomModel, 0, len(models))
	for _, m := range models {
		out = append(out, cli.CustomModel{
			Name:    m.Name,
			ID:      m.ID,
			Context: m.Context,
		})
	}
	return out
}

func showTUIModelPicker(app *chat.ChatApp, currentProvider, currentModel string, cfg *cli.Config, apply func(provider, model string) error) {
	picker := agentpanels.NewModelPicker(currentProvider, currentModel, newCLIModelPickerBackend(cfg))
	ov := agentui.NewLocalOverlay(picker, 100, 100)
	closeOverlay := func() {
		app.Host().RemoveOverlay(ov)
		app.Host().Focus(app.Editor())
	}
	picker.SetOnCancel(closeOverlay)
	picker.SetOnApply(func(provider, model string) {
		if err := apply(provider, model); err != nil {
			app.PrintError(err)
			return
		}
		closeOverlay()
		app.PrintSystem(i18n.T("system.provider_switched_model", "provider", provider, "model", model))
	})
	app.Host().PushOverlay(ov)
}
