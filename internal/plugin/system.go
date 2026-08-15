package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/covoyage/covo-agent/internal/logutil"
	"gopkg.in/yaml.v3"
)

type System struct {
	cfg      SystemConfig
	Registry *Registry
	logger   *slog.Logger
}

func NewSystem(ctx context.Context, cfg SystemConfig) (*System, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))
	}

	sys := &System{
		cfg:      cfg,
		Registry: NewRegistry(),
		logger:   logger,
	}

	if err := sys.loadPluginConfig(); err != nil {
		logger.Warn("plugin: failed to load plugin config", "error", err)
	}

	return sys, nil
}

func (s *System) Shutdown() {
	s.logger.Info("plugin: system shutdown")
}

// CLICommands returns all CLI commands from enabled plugins.
func (s *System) CLICommands() []CLICommand {
	var cmds []CLICommand
	for _, e := range s.Registry.List() {
		if !e.Enabled {
			continue
		}
		if cp, ok := e.Provider.(CLICommandProvider); ok {
			cmds = append(cmds, cp.CLICommands()...)
		}
	}
	return cmds
}

// MemoryProviders returns all memory provider registrations from enabled plugins.
func (s *System) MemoryProviders() []MemoryProviderRegistration {
	var providers []MemoryProviderRegistration
	for _, e := range s.Registry.List() {
		if !e.Enabled {
			continue
		}
		if mp, ok := e.Provider.(MemoryProviderPlugin); ok {
			providers = append(providers, mp.MemoryProviders()...)
		}
	}
	return providers
}

// LifecycleHooks returns all lifecycle hooks from enabled plugins.
func (s *System) LifecycleHooks() []LifecycleHook {
	var hooks []LifecycleHook
	for _, e := range s.Registry.List() {
		if !e.Enabled {
			continue
		}
		if hp, ok := e.Provider.(HookProvider); ok {
			hooks = append(hooks, hp.LifecycleHooks()...)
		}
	}
	return hooks
}

func (s *System) pluginConfigPath() string {
	return filepath.Join(s.cfg.HomeDir, "plugins.yaml")
}

func (s *System) loadPluginConfig() error {
	path := s.pluginConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read plugin config: %w", err)
	}

	var cfg PluginConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse plugin config: %w", err)
	}

	for _, entry := range cfg.Plugins {
		e := &Entry{
			ID:       entry.ID,
			Name:     entry.Name,
			Category: Category(entry.Category),
			Enabled:  entry.Enabled,
		}
		if err := s.Registry.Register(e); err != nil {
			s.logger.Warn("plugin: register failed", "id", entry.ID, "error", err)
		}
	}

	return nil
}

func (s *System) SavePluginConfig() error {
	entries := s.Registry.List()

	var cfg PluginConfig
	for _, e := range entries {
		cfg.Plugins = append(cfg.Plugins, PluginEntryConfig{
			ID:       e.ID,
			Name:     e.Name,
			Category: string(e.Category),
			Enabled:  e.Enabled,
		})
	}

	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshal plugin config: %w", err)
	}

	path := s.pluginConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create plugin config dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write plugin config: %w", err)
	}

	return nil
}

func (s *System) RegisterBuiltin(providers []any) error {
	for _, p := range providers {
		var id, name string
		var cat Category

		if named, ok := p.(interface{ GetID() string }); ok {
			id = named.GetID()
		}
		if named, ok := p.(interface{ GetName() string }); ok {
			name = named.GetName()
		}
		if named, ok := p.(interface{ GetCategory() Category }); ok {
			cat = named.GetCategory()
		}

		if id == "" {
			continue
		}

		entry := &Entry{
			ID:       id,
			Name:     name,
			Category: cat,
			Enabled:  s.isEnabledByDefault(id, cat),
			Provider: p,
		}

		if existing := s.Registry.Get(entry.ID); existing != nil {
			existing.Provider = p
			continue
		}

		if err := s.Registry.Register(entry); err != nil {
			s.logger.Warn("plugin: register builtin failed", "id", entry.ID, "error", err)
		}
	}
	return nil
}

func (s *System) isEnabledByDefault(id string, category Category) bool {
	if category == CategoryPlatform {
		p := id
		envKey := strings.ToUpper(p) + "_BOT_TOKEN"
		if botToken := strings.TrimSpace(os.Getenv(envKey)); botToken != "" {
			return true
		}
		envKey2 := strings.ToUpper(p) + "_TOKEN"
		if token := strings.TrimSpace(os.Getenv(envKey2)); token != "" {
			return true
		}
		if strings.EqualFold(p, "whatsapp") {
			if token := strings.TrimSpace(os.Getenv("WHATSAPP_ACCESS_TOKEN")); token != "" {
				return true
			}
		}
		if strings.EqualFold(p, "line") {
			if token := strings.TrimSpace(os.Getenv("LINE_CHANNEL_ACCESS_TOKEN")); token != "" {
				return true
			}
		}
		if strings.EqualFold(p, "twitch") {
			if token := strings.TrimSpace(os.Getenv("TWITCH_OAUTH_TOKEN")); token != "" {
				return true
			}
		}
		if strings.EqualFold(p, "zalo") {
			if token := strings.TrimSpace(os.Getenv("ZALO_ACCESS_TOKEN")); token != "" {
				return true
			}
		}
		if strings.EqualFold(p, "nextcloud-talk") {
			if token := strings.TrimSpace(os.Getenv("NEXTCLOUD_TALK_BASE_URL")); token != "" {
				return true
			}
		}
		if strings.EqualFold(p, "synology-chat") {
			if token := strings.TrimSpace(os.Getenv("SYNOLOGY_CHAT_WEBHOOK_URL")); token != "" {
				return true
			}
		}
		if strings.EqualFold(p, "webhook") || strings.EqualFold(p, "api_server") || strings.EqualFold(p, "cron") {
			return true
		}
	}
	return false
}

type PluginConfig struct {
	Plugins []PluginEntryConfig `yaml:"plugins"`
}

type PluginEntryConfig struct {
	ID       string `yaml:"id"`
	Name     string `yaml:"name"`
	Category string `yaml:"category"`
	Enabled  bool   `yaml:"enabled"`
}
