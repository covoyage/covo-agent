package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	acplib "github.com/covoyage/covonaut/acp"
	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/cli"
)

type covoAgentInstance struct {
	agent *agent.CovoAgent
}

func (a *covoAgentInstance) Run(ctx context.Context, input string) (string, error) {
	return a.agent.Run(ctx, input)
}

func (a *covoAgentInstance) Core() *agentcore.Agent {
	return a.agent.Core()
}

func (a *covoAgentInstance) Model() string {
	return a.agent.Config().Model
}

func (a *covoAgentInstance) Mode() string {
	return string(a.agent.Mode())
}

func (a *covoAgentInstance) Rebuild(mode, model string) error {
	agentMode, ok := agent.ParseMode(mode)
	if !ok {
		agentMode = agent.ModeGeneral
	}
	cfg := a.agent.Config()
	return a.agent.Rebuild(agentMode, cfg.Provider, a.agent.ProviderName(), model, a.agent.WorkDir(), a.agent.HomeDir())
}

// SetPermissionRequester implements acplib.PermissionAware: it bridges the
// agent's dangerous-tool permission gate to the ACP client. The ACP requester
// receives (toolCallID, name, rawInput); covo-agent's checker only has
// (ctx, name, args), so toolCallID is passed empty.
func (a *covoAgentInstance) SetPermissionRequester(fn func(toolCallID, name string, rawInput any) bool) {
	a.agent.SetPermissionChecker(func(ctx context.Context, toolName string, args []byte) bool {
		return fn("", toolName, json.RawMessage(args))
	})
}

// SetFileSystem implements acplib.FileSystemAware: it routes the agent's file
// read/write tools through the editor's filesystem (seeing unsaved buffers).
func (a *covoAgentInstance) SetFileSystem(fs acplib.ACPFileSystem) {
	a.agent.SetFileBackend(fs)
}

type covoAgentFactory struct {
	homeDir string
	logger  *slog.Logger
}

func newCovoAgentFactory(homeDir string, logger *slog.Logger) *covoAgentFactory {
	return &covoAgentFactory{homeDir: homeDir, logger: logger}
}

func (f *covoAgentFactory) CreateAgent(ctx context.Context, sessionID, cwd, model, mode string) (acplib.AgentInstance, error) {
	if err := cli.LoadDotEnv(); err != nil {
		f.logger.Warn("load .env", "err", err)
	}
	cfg, err := cli.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	providerType := cli.ResolveProvider(cfg)
	if model == "" {
		model = cli.ResolveModel(cfg)
	}
	if mode == "" {
		mode = cli.ResolveMode(cfg)
	}

	llm, err := cli.BuildProvider(providerType)
	if err != nil {
		return nil, fmt.Errorf("create LLM provider: %w", err)
	}

	agentMode, ok := agent.ParseMode(mode)
	if !ok {
		agentMode = agent.ModeGeneral
	}

	ca, err := agent.NewCovoAgent(agent.CovoAgentConfig{
		Mode:                    agentMode,
		Provider:                llm,
		ProviderName:            providerType,
		Model:                   model,
		WorkingDir:              cwd,
		HomeDir:                 f.homeDir,
		Logger:                  f.logger,
		Auxiliary:               convertAuxiliary(cfg),
		AuxiliaryProviderBuilder: cli.ResolveAuxiliaryProviderBuilder(),
	})
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}

	return &covoAgentInstance{agent: ca}, nil
}

func (f *covoAgentFactory) DefaultModel() string {
	cfg, err := cli.LoadConfig()
	if err != nil {
		return "openrouter/owl-alpha"
	}
	return cli.ResolveModel(cfg)
}

func (f *covoAgentFactory) DefaultMode() string {
	cfg, err := cli.LoadConfig()
	if err != nil {
		return "general"
	}
	return cli.ResolveMode(cfg)
}

func (f *covoAgentFactory) AvailableModes() []acplib.SessionMode {
	return []acplib.SessionMode{
		{ID: "general", Name: "General", Description: "General-purpose assistant"},
		{ID: "code", Name: "Code", Description: "Coding-focused assistant"},
	}
}

type covoAuthProvider struct{}

func (p *covoAuthProvider) AuthMethods() []any {
	methods := []any{}

	if cli.HasProviderConfigured() {
		cfg, _ := cli.LoadConfig()
		if cfg != nil {
			provider := cli.ResolveProvider(cfg)
			methods = append(methods, acplib.AuthMethodAgent{
				ID:          provider,
				Name:        fmt.Sprintf("%s runtime credentials", provider),
				Description: fmt.Sprintf("Authenticate using configured %s credentials.", provider),
			})
		}
	}

	methods = append(methods, acplib.TerminalAuthMethod{
		ID:          "covo-setup",
		Name:        "Configure covo-agent provider",
		Description: "Run covo-agent interactive setup in a terminal.",
		Type:        "terminal",
		Args:        []string{"--setup"},
	})

	return methods
}

type fileSessionStore struct {
	path string
}

func newFileSessionStore(homeDir string) *fileSessionStore {
	return &fileSessionStore{path: filepath.Join(homeDir, "acp_sessions.json")}
}

func (s *fileSessionStore) LoadSessionMeta(sessionID string) (acplib.SessionMeta, error) {
	all, err := s.loadAll()
	if err != nil {
		return acplib.SessionMeta{}, fmt.Errorf("session %s not found", sessionID)
	}
	for _, m := range all {
		if m.SessionID == sessionID {
			return m, nil
		}
	}
	return acplib.SessionMeta{}, fmt.Errorf("session %s not found", sessionID)
}

func (s *fileSessionStore) SaveSessionMeta(meta acplib.SessionMeta) error {
	meta.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	all, err := s.loadAll()
	if err != nil {
		slog.Warn("load sessions for save", "err", err)
	}

	found := false
	for i, m := range all {
		if m.SessionID == meta.SessionID {
			all[i] = meta
			found = true
			break
		}
	}
	if !found {
		all = append(all, meta)
	}

	return s.saveAll(all)
}

func (s *fileSessionStore) ListSessions(cwd string) []acplib.SessionMeta {
	all, err := s.loadAll()
	if err != nil {
		slog.Warn("load sessions for list", "err", err)
		return nil
	}
	if cwd == "" {
		return all
	}
	var filtered []acplib.SessionMeta
	for _, m := range all {
		if m.CWD == cwd {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

func (s *fileSessionStore) loadAll() ([]acplib.SessionMeta, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	var metas []acplib.SessionMeta
	if err := json.Unmarshal(data, &metas); err != nil {
		return nil, err
	}
	return metas, nil
}

func (s *fileSessionStore) saveAll(metas []acplib.SessionMeta) error {
	data, err := json.MarshalIndent(metas, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

// convertAuxiliary converts cli.AuxiliaryConfig to agent.AuxiliaryConfig.
func convertAuxiliary(cfg *cli.Config) *agent.AuxiliaryConfig {
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
