package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/provider/chatcompat"
)

// AuxiliaryModelConfig mirrors cli.AuxiliaryModelConfig so the agent package
// does not need to import cli. Each field optionally overrides the main
// provider/model for a specific class of auxiliary LLM call.
type AuxiliaryModelConfig struct {
	Model    string `yaml:"model,omitempty"`
	Provider string `yaml:"provider,omitempty"`
	BaseURL  string `yaml:"base_url,omitempty"`
	APIKey   string `yaml:"api_key,omitempty"`
}

// AuxiliaryConfig holds per-task auxiliary model overrides. When a field is
// nil, the corresponding auxiliary call falls back to the agent's main
// provider and model.
type AuxiliaryConfig struct {
	Compression *AuxiliaryModelConfig `yaml:"compression,omitempty"`
	Vision      *AuxiliaryModelConfig `yaml:"vision,omitempty"`
	WebExtract  *AuxiliaryModelConfig `yaml:"web_extract,omitempty"`
	Title       *AuxiliaryModelConfig `yaml:"title,omitempty"`
	Review      *AuxiliaryModelConfig `yaml:"review,omitempty"`
}

// AuxiliaryTask identifies a category of auxiliary LLM work.
type AuxiliaryTask string

const (
	TaskCompression AuxiliaryTask = "compression"
	TaskVision      AuxiliaryTask = "vision"
	TaskWebExtract  AuxiliaryTask = "web_extract"
	TaskTitle       AuxiliaryTask = "title"
	TaskReview      AuxiliaryTask = "review"
)

// AuxiliaryProviderBuilder is a function that builds a provider from a
// provider type, optional base URL, and optional API key. Callers in the
// cli package set this to route through the full provider registry without
// creating an import cycle.
type AuxiliaryProviderBuilder func(providerType, baseURL, apiKey string) (agentcore.Provider, error)

// resolvedModel holds a ready-to-use provider+model pair.
type resolvedModel struct {
	provider agentcore.Provider
	model    string
}

// AuxiliaryClient routes auxiliary LLM calls (title generation, background
// review, context compression, goal judging, etc.) to independently
// configured providers/models when available, falling back to the agent's
// main provider otherwise. This lets users configure a cheap/fast model for
// routine auxiliary work while keeping the main agent on a powerful model.
type AuxiliaryClient struct {
	mu           sync.RWMutex
	mainProvider agentcore.Provider
	mainModel    string
	logger       *slog.Logger

	// providerBuilder builds a new provider from an auxiliary config that
	// specifies its own provider/base_url/api_key. When nil, only model-only
	// overrides are supported (reusing the main provider with a different
	// model name).
	providerBuilder AuxiliaryProviderBuilder

	// Per-task resolved models. nil = fallback to main.
	resolved map[AuxiliaryTask]*resolvedModel
}

// NewAuxiliaryClient creates an AuxiliaryClient. If auxCfg is nil or a
// task's sub-config is absent, that task uses the main provider/model.
func NewAuxiliaryClient(mainProvider agentcore.Provider, mainModel string, auxCfg *AuxiliaryConfig, builder AuxiliaryProviderBuilder, logger *slog.Logger) *AuxiliaryClient {
	ac := &AuxiliaryClient{
		mainProvider:    mainProvider,
		mainModel:       mainModel,
		logger:          logger,
		providerBuilder: builder,
		resolved:        make(map[AuxiliaryTask]*resolvedModel),
	}

	if auxCfg != nil {
		ac.resolveTask(TaskCompression, auxCfg.Compression)
		ac.resolveTask(TaskVision, auxCfg.Vision)
		ac.resolveTask(TaskWebExtract, auxCfg.WebExtract)
		ac.resolveTask(TaskTitle, auxCfg.Title)
		ac.resolveTask(TaskReview, auxCfg.Review)
	}

	return ac
}

// resolveTask attempts to build a dedicated provider for an auxiliary task.
// On failure it logs a warning and the task will fall back to the main provider.
func (ac *AuxiliaryClient) resolveTask(task AuxiliaryTask, cfg *AuxiliaryModelConfig) {
	if cfg == nil || (cfg.Provider == "" && cfg.Model == "" && cfg.BaseURL == "" && cfg.APIKey == "") {
		return // no override configured
	}

	// If only a model is specified (no provider/base_url/api_key), we can
	// reuse the main provider with a different model — no new provider build
	// needed. This is the most common case: "use gpt-5.6 for titles".
	if cfg.Provider == "" && cfg.BaseURL == "" && cfg.APIKey == "" && cfg.Model != "" {
		ac.resolved[task] = &resolvedModel{
			provider: ac.mainProvider,
			model:    cfg.Model,
		}
		if ac.logger != nil {
			ac.logger.Debug("auxiliary task using main provider with model override",
				"task", task, "model", cfg.Model)
		}
		return
	}

	// A full provider override is configured. Build a separate provider.
	providerType := cfg.Provider
	if providerType == "" {
		providerType = "openai"
	}

	// Expand env vars in case api_key/base_url are like "$OPENAI_API_KEY".
	apiKey := os.ExpandEnv(cfg.APIKey)
	baseURL := os.ExpandEnv(cfg.BaseURL)

	var provider agentcore.Provider
	if ac.providerBuilder != nil {
		p, err := ac.providerBuilder(providerType, baseURL, apiKey)
		if err != nil {
			if ac.logger != nil {
				ac.logger.Warn("auxiliary provider build failed, falling back to main",
					"task", task, "provider", providerType, "error", err)
			}
			return
		}
		provider = p
	} else {
		// No builder available — try to build a minimal OpenAI-compatible provider.
		p, err := buildMinimalProvider(apiKey, baseURL)
		if err != nil {
			if ac.logger != nil {
				ac.logger.Warn("auxiliary provider build failed, falling back to main",
					"task", task, "provider", providerType, "error", err)
			}
			return
		}
		provider = p
	}

	model := cfg.Model
	if model == "" {
		model = ac.mainModel
	}

	ac.resolved[task] = &resolvedModel{
		provider: provider,
		model:    model,
	}
	if ac.logger != nil {
		ac.logger.Info("auxiliary task configured with dedicated provider",
			"task", task, "provider", providerType, "model", model)
	}
}

// getResolved returns the resolved model for a task, or the main fallback.
func (ac *AuxiliaryClient) getResolved(task AuxiliaryTask) (*resolvedModel, bool) {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	if rm, ok := ac.resolved[task]; ok && rm != nil {
		return rm, true
	}
	if ac.mainProvider != nil {
		return &resolvedModel{provider: ac.mainProvider, model: ac.mainModel}, true
	}
	return nil, false
}

// Provider returns the provider to use for the given task.
func (ac *AuxiliaryClient) Provider(task AuxiliaryTask) agentcore.Provider {
	rm, ok := ac.getResolved(task)
	if !ok {
		return nil
	}
	return rm.provider
}

// Model returns the model name to use for the given task.
func (ac *AuxiliaryClient) Model(task AuxiliaryTask) string {
	rm, ok := ac.getResolved(task)
	if !ok {
		return ac.mainModel
	}
	return rm.model
}

// Complete dispatches a simple system+user LLM call to the appropriate
// auxiliary provider. It is the primary entry point for auxiliary tasks
// like title generation, background review, and goal judging.
func (ac *AuxiliaryClient) Complete(ctx context.Context, task AuxiliaryTask, systemPrompt, userPrompt string, maxTokens int64, temperature float64) (string, error) {
	rm, ok := ac.getResolved(task)
	if !ok {
		return "", fmt.Errorf("no provider available for auxiliary task %q", task)
	}

	req := &agentcore.ProviderRequest{
		Model: rm.model,
		Messages: []agentcore.Message{
			{Role: agentcore.RoleSystem, Content: systemPrompt},
			{Role: agentcore.RoleUser, Content: userPrompt},
		},
		MaxTokens:   maxTokens,
		Temperature: temperature,
	}

	resp, err := rm.provider.Complete(ctx, req)
	if err != nil {
		return "", fmt.Errorf("auxiliary %s call: %w", task, err)
	}
	return resp.Content, nil
}

// CompleteWithMessages dispatches an LLM call with arbitrary messages (not
// just system+user) to the appropriate auxiliary provider.
func (ac *AuxiliaryClient) CompleteWithMessages(ctx context.Context, task AuxiliaryTask, messages []agentcore.Message, maxTokens int64, temperature float64) (string, error) {
	rm, ok := ac.getResolved(task)
	if !ok {
		return "", fmt.Errorf("no provider available for auxiliary task %q", task)
	}

	req := &agentcore.ProviderRequest{
		Model:       rm.model,
		Messages:    messages,
		MaxTokens:   maxTokens,
		Temperature: temperature,
	}

	resp, err := rm.provider.Complete(ctx, req)
	if err != nil {
		return "", fmt.Errorf("auxiliary %s call: %w", task, err)
	}
	return resp.Content, nil
}

// HasProvider returns true if the client has any provider (main or auxiliary)
// available for the given task.
func (ac *AuxiliaryClient) HasProvider(task AuxiliaryTask) bool {
	_, ok := ac.getResolved(task)
	return ok
}

// SetMainProvider updates the main provider and model. Used when the agent is
// rebuilt with a different provider/model (e.g. via /model or /provider
// slash commands). Previously resolved auxiliary providers are preserved.
// Model-only overrides (which share the main provider) are cleared so they
// re-resolve through the fallback path with the new main provider.
func (ac *AuxiliaryClient) SetMainProvider(provider agentcore.Provider, model string) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	oldProvider := ac.mainProvider
	ac.mainProvider = provider
	ac.mainModel = model

	// Clear model-only overrides (those whose provider pointer matches the
	// old main provider) so they re-resolve via getResolved fallback with the
	// new main provider. Full provider overrides have their own separate
	// provider and are preserved.
	for task, rm := range ac.resolved {
		if rm != nil && rm.provider == oldProvider {
			delete(ac.resolved, task)
		}
	}
}

// buildMinimalProvider builds a basic OpenAI-compatible chat provider from
// an API key and/or base URL. This is used as a fallback when no
// AuxiliaryProviderBuilder is configured (e.g. in tests).
func buildMinimalProvider(apiKey, baseURL string) (agentcore.Provider, error) {
	if apiKey == "" && baseURL == "" {
		return nil, fmt.Errorf("cannot build auxiliary provider without api_key or base_url")
	}
	return chatcompat.New(chatcompat.Config{APIKey: apiKey, BaseURL: baseURL}), nil
}
