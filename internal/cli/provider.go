package cli

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/provider/anthropic"
	"github.com/covoyage/covonaut/provider/bedrock"
	"github.com/covoyage/covonaut/provider/chatcompat"
	"github.com/covoyage/covonaut/provider/gemini"
	"github.com/covoyage/covonaut/provider/mistral"
	"github.com/covoyage/covonaut/provider/vertex"
)

const (
	xiaomiBaseURL       = "https://token-plan-cn.xiaomimimo.com/v1"
	openrouterBaseURL   = "https://openrouter.ai/api/v1"
	openaiBaseURL       = "https://api.openai.com/v1"
	anthropicBaseURL    = "https://api.anthropic.com"
	geminiBaseURL       = "https://generativelanguage.googleapis.com/v1beta"
	deepseekBaseURL     = "https://api.deepseek.com/v1"
	xaiBaseURL          = "https://api.x.ai/v1"
	qwenBaseURL         = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	zaiBaseURL          = "https://api.z.ai/api/paas/v4"
	stepfunBaseURL      = "https://api.stepfun.com/v1"
	nvidiaBaseURL       = "https://integrate.api.nvidia.com/v1"
	huggingfaceBaseURL  = "https://api-inference.huggingface.co/v1"
	alibabaBaseURL      = "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"
	kilocodeBaseURL     = "https://api.kilo.ai/api/gateway/v1"
	kimicodingBaseURL   = "https://api.moonshot.ai/v1"
	kimicodingCNBaseURL = "https://api.moonshot.cn/v1"
	perplexityBaseURL   = "https://api.perplexity.ai"
	nousBaseURL         = "https://inference.nousresearch.com/v1"
	minimaxBaseURL      = "https://api.minimax.io/anthropic"
	minimaxCNBaseURL    = "https://api.minimaxi.com/anthropic"
)

func BuildProvider(providerType string) (agentcore.Provider, error) {
	reg, ok := GetProviderRegistration(providerType)
	if !ok {
		// fallback to openai
		reg, ok = GetProviderRegistration("openai")
		if !ok {
			return nil, fmt.Errorf("no default provider registered")
		}
	}
	return reg.Factory()
}

func resolveAPIKey(primaryEnv string, fallbackEnvs ...string) string {
	if v := os.Getenv(primaryEnv); v != "" {
		return v
	}
	for _, env := range fallbackEnvs {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	if v := os.Getenv("API_KEY"); v != "" {
		return v
	}
	return ""
}

func HasProviderConfigured() bool {
	providerEnvVars := []string{
		"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY",
		"GOOGLE_API_KEY", "XIAOMI_API_KEY", "OPENROUTER_API_KEY",
		"CUSTOM_API_KEY", "API_KEY", "OPENAI_BASE_URL", "CUSTOM_BASE_URL",
	}
	for _, v := range providerEnvVars {
		if os.Getenv(v) != "" {
			return true
		}
	}
	return false
}

func HasProviderConfiguredFor(providerType string) bool {
	env := ProviderAPIKeyEnv(providerType)
	if os.Getenv(env) != "" {
		return true
	}
	if os.Getenv("API_KEY") != "" {
		return true
	}
	if providerType == "custom" && os.Getenv("CUSTOM_BASE_URL") == "" {
		return false
	}
	return false
}

func getProviderRegOrFallback(providerType string) *ProviderRegistration {
	reg, ok := GetProviderRegistration(providerType)
	if !ok {
		reg, _ = GetProviderRegistration("openai")
	}
	return reg
}

func DefaultModel(providerType string) string {
	reg := getProviderRegOrFallback(providerType)
	if reg == nil {
		return "gpt-5.6"
	}
	return reg.DefaultModel
}

// DefaultGhostModel returns the fastest available model for inline ghost
// completions based on the active provider.
func DefaultGhostModel(providerType string) string {
	switch providerType {
	case "openai":
		return "gpt-4o-mini"
	case "anthropic":
		return "claude-3-5-haiku-20241022"
	case "gemini":
		return "gemini-2.5-flash"
	case "deepseek":
		return "deepseek-chat"
	case "openrouter":
		return "google/gemini-2.5-flash"
	default:
		// For custom/OpenAI-compatible providers, fall back to the
		// provider's default model — likely the best option available.
		return DefaultModel(providerType)
	}
}

func ProviderName(providerType string) string {
	reg := getProviderRegOrFallback(providerType)
	if reg == nil {
		return "openai"
	}
	return reg.Name
}

func ValidateProvider(providerType string) error {
	if _, ok := GetProviderRegistration(providerType); ok {
		return nil
	}
	return fmt.Errorf("unsupported provider %q (supported: %v)", providerType, RegisteredProviderTypes())
}

func ProviderDisplayName(providerType string) string {
	reg := getProviderRegOrFallback(providerType)
	if reg == nil {
		return providerType
	}
	return reg.DisplayName
}

func ProviderAPIKeyEnv(providerType string) string {
	reg := getProviderRegOrFallback(providerType)
	if reg == nil || len(reg.APIKeyEnvs) == 0 {
		return "OPENAI_API_KEY"
	}
	return reg.APIKeyEnvs[0]
}

func ProviderNeedsBaseURL(providerType string) bool {
	reg := getProviderRegOrFallback(providerType)
	return reg != nil && reg.NeedsBaseURL
}

func ProviderBaseURLEnv(providerType string) string {
	reg := getProviderRegOrFallback(providerType)
	if reg == nil {
		return ""
	}
	return reg.BaseURLEnv
}

func ProviderDefaultBaseURL(providerType string) string {
	reg := getProviderRegOrFallback(providerType)
	if reg == nil {
		return ""
	}
	return reg.DefaultBaseURL
}

type OpenRouterModel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Pricing     struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
	} `json:"pricing"`
	ContextLength int `json:"context_length"`
}

type ProviderModel struct {
	ID          string
	Name        string
	Description string
	Context     int // context window size in tokens, 0 if unknown
}

type openRouterModelsResponse struct {
	Data []OpenRouterModel `json:"data"`
}

type openAIModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

type anthropicModelsResponse struct {
	Data []struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"data"`
}

type geminiModelsResponse struct {
	Models []struct {
		Name        string   `json:"name"`
		DisplayName string   `json:"displayName"`
		Description string   `json:"description"`
		Methods     []string `json:"supportedGenerationMethods"`
	} `json:"models"`
}

var (
	openRouterModelsCache []OpenRouterModel
	openRouterCacheTime   time.Time
)

func FetchOpenRouterModels() ([]OpenRouterModel, error) {
	if len(openRouterModelsCache) > 0 && time.Since(openRouterCacheTime) < 5*time.Minute {
		return openRouterModelsCache, nil
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", openrouterBaseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch models: %w", err)
	}
	defer resp.Body.Close()

	var result openRouterModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}

	sort.Slice(result.Data, func(i, j int) bool {
		return result.Data[i].ID < result.Data[j].ID
	})

	openRouterModelsCache = result.Data
	openRouterCacheTime = time.Now()
	return result.Data, nil
}

func OpenRouterModelIDs() ([]string, error) {
	models, err := FetchOpenRouterModels()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

func FetchProviderModels(providerType string) ([]ProviderModel, error) {
	reg := getProviderRegOrFallback(providerType)
	if reg == nil || reg.FetchModels == nil {
		return nil, fmt.Errorf("provider %q does not expose a model list API", providerType)
	}
	return reg.FetchModels()
}

func fetchOpenAICompatibleModels(providerType string) ([]ProviderModel, error) {
	providerType = ProviderName(providerType)
	apiKey := providerAPIKey(providerType)
	if apiKey == "" {
		return nil, fmt.Errorf("%s is not set", ProviderAPIKeyEnv(providerType))
	}

	baseURL := openaiBaseURL
	switch providerType {
	case "openrouter":
		baseURL = envOrDefault("OPENROUTER_BASE_URL", openrouterBaseURL)
	case "xiaomi":
		baseURL = envOrDefault("XIAOMI_BASE_URL", xiaomiBaseURL)
	case "custom":
		baseURL = os.Getenv("CUSTOM_BASE_URL")
	case "openai":
		baseURL = envOrDefault("OPENAI_BASE_URL", openaiBaseURL)
	}
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("%s is required", ProviderBaseURLEnv(providerType))
	}

	req, err := http.NewRequest("GET", strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	var response openAIModelsResponse
	if err := doJSON(req, &response); err != nil {
		return nil, err
	}

	models := make([]ProviderModel, 0, len(response.Data))
	for _, m := range response.Data {
		if m.ID != "" {
			models = append(models, ProviderModel{ID: m.ID})
		}
	}
	sortProviderModels(models)
	return models, nil
}

func fetchAnthropicModels() ([]ProviderModel, error) {
	apiKey := providerAPIKey("anthropic")
	if apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}
	baseURL := envOrDefault("ANTHROPIC_BASE_URL", anthropicBaseURL)
	req, err := http.NewRequest("GET", strings.TrimRight(baseURL, "/")+"/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	var response anthropicModelsResponse
	if err := doJSON(req, &response); err != nil {
		return nil, err
	}

	models := make([]ProviderModel, 0, len(response.Data))
	for _, m := range response.Data {
		if m.ID != "" {
			models = append(models, ProviderModel{ID: m.ID, Name: m.DisplayName})
		}
	}
	sortProviderModels(models)
	return models, nil
}

func fetchGeminiModels() ([]ProviderModel, error) {
	apiKey := providerAPIKey("gemini")
	if apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY or GOOGLE_API_KEY is not set")
	}
	baseURL := envOrDefault("GEMINI_BASE_URL", geminiBaseURL)
	req, err := http.NewRequest("GET", strings.TrimRight(baseURL, "/")+"/models?key="+urlQueryEscape(apiKey), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	var response geminiModelsResponse
	if err := doJSON(req, &response); err != nil {
		return nil, err
	}

	models := make([]ProviderModel, 0, len(response.Models))
	for _, m := range response.Models {
		if !supportsGeminiGenerateContent(m.Methods) {
			continue
		}
		id := strings.TrimPrefix(m.Name, "models/")
		if id != "" {
			models = append(models, ProviderModel{ID: id, Name: m.DisplayName, Description: m.Description})
		}
	}
	sortProviderModels(models)
	return models, nil
}

func doJSON(req *http.Request, target any) error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fetch models: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode models: %w", err)
	}
	return nil
}

func providerAPIKey(providerType string) string {
	switch ProviderName(providerType) {
	case "gemini":
		return resolveAPIKey("GEMINI_API_KEY", "GOOGLE_API_KEY")
	default:
		return resolveAPIKey(ProviderAPIKeyEnv(providerType))
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func sortProviderModels(models []ProviderModel) {
	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})
}

func supportsGeminiGenerateContent(methods []string) bool {
	for _, method := range methods {
		if method == "generateContent" {
			return true
		}
	}
	return false
}

func urlQueryEscape(value string) string {
	replacer := strings.NewReplacer(
		" ", "%20",
		"+", "%2B",
		"/", "%2F",
		"=", "%3D",
		"&", "%26",
		"?", "%3F",
		"#", "%23",
	)
	return replacer.Replace(value)
}

func BuildFallbackProvider(primaryType string, fallbackTypes []string, logger *slog.Logger) (agentcore.Provider, error) {
	primary, err := BuildProvider(primaryType)
	if err != nil {
		return nil, fmt.Errorf("build primary provider %s: %w", primaryType, err)
	}

	if len(fallbackTypes) == 0 {
		return primary, nil
	}

	providers := []agentcore.Provider{primary}
	names := []string{primaryType}

	for _, ft := range fallbackTypes {
		fp, err := BuildProvider(ft)
		if err != nil {
			logger.Warn("skip fallback provider", "provider", ft, "error", err)
			continue
		}
		providers = append(providers, fp)
		names = append(names, ft)
	}

	if len(providers) == 1 {
		return primary, nil
	}

	return NewFallbackProvider(providers, names, logger), nil
}

// --- Pluggable Provider Registry ---

// ProviderRegistration describes a provider type and how to build it.
type ProviderRegistration struct {
	// Type is the primary identifier (e.g. "openai").
	Type string
	// Aliases are alternative names (e.g. "mimo" for "xiaomi").
	Aliases []string
	// Factory creates a provider instance. Reads must be from env vars at call time.
	Factory func() (agentcore.Provider, error)
	// DefaultModel is the default model for this provider.
	DefaultModel string
	// Name is the canonical name (e.g. "openai").
	Name string
	// DisplayName is the human-readable name (e.g. "OpenAI").
	DisplayName string
	// APIKeyEnvs is the list of env var names to check for the API key, in priority order.
	APIKeyEnvs []string
	// NeedsBaseURL is true if a base URL must be explicitly configured.
	NeedsBaseURL bool
	// BaseURLEnv is the env var name for the base URL override.
	BaseURLEnv string
	// DefaultBaseURL is the default base URL if none is configured.
	DefaultBaseURL string
	// FetchModels fetches available models from the provider API. Nil if not supported.
	FetchModels func() ([]ProviderModel, error)
}

var (
	providerReg   = map[string]*ProviderRegistration{}
	providerRegMu sync.RWMutex
)

// RegisterProvider registers a provider type. Panics on duplicate.
func RegisterProvider(reg *ProviderRegistration) {
	providerRegMu.Lock()
	defer providerRegMu.Unlock()
	keys := append([]string{reg.Type}, reg.Aliases...)
	for _, k := range keys {
		if _, exists := providerReg[k]; exists {
			panic(fmt.Sprintf("provider %q already registered", k))
		}
	}
	for _, k := range keys {
		providerReg[k] = reg
	}
}

// GetProviderRegistration returns the registration for a provider type.
func GetProviderRegistration(providerType string) (*ProviderRegistration, bool) {
	providerRegMu.RLock()
	defer providerRegMu.RUnlock()
	reg, ok := providerReg[providerType]
	return reg, ok
}

// RegisteredProviderTypes returns all primary provider type names.
func RegisteredProviderTypes() []string {
	providerRegMu.RLock()
	defer providerRegMu.RUnlock()
	seen := map[string]bool{}
	var types []string
	for _, reg := range providerReg {
		if !seen[reg.Type] {
			seen[reg.Type] = true
			types = append(types, reg.Type)
		}
	}
	return types
}

// UnregisterProvider removes a provider type and all its aliases from the registry.
func UnregisterProvider(providerType string) {
	providerRegMu.Lock()
	defer providerRegMu.Unlock()
	reg, ok := providerReg[providerType]
	if !ok {
		return
	}
	keys := append([]string{reg.Type}, reg.Aliases...)
	for _, k := range keys {
		delete(providerReg, k)
	}
}

// --- Custom Provider Registration ---

var safeTypeRe = regexp.MustCompile(`[^a-z0-9]+`)

// ProviderTypeName generates a safe internal type name from a display name.
// This is used to create stable registry keys for custom providers.
func ProviderTypeName(displayName string) string {
	key := strings.ToLower(displayName)
	key = safeTypeRe.ReplaceAllString(key, "_")
	key = strings.Trim(key, "_")
	if key == "" {
		key = "custom"
	}
	return "custom_" + key
}

// RegisterCustomProviders registers all user-defined custom providers from config.
// Must be called after LoadConfig and LoadDotEnv.
// Safe to call multiple times — previously registered custom providers are unregistered first.
func RegisterCustomProviders(cfg *Config) {
	// Unregister existing custom providers to avoid duplicates on re-call.
	for _, cp := range cfg.CustomProviders {
		UnregisterProvider(ProviderTypeName(cp.Name))
	}

	seen := map[string]bool{}
	for _, cp := range cfg.CustomProviders {
		cp := cp // capture for closure
		typeName := ProviderTypeName(cp.Name)

		// Skip duplicates (e.g. two providers with names that sanitize to the same key).
		if seen[typeName] {
			continue
		}
		seen[typeName] = true

		apiKeyEnv := cp.APIKeyEnv
		baseURL := cp.BaseURL

		reg := &ProviderRegistration{
			Type:        typeName,
			Name:        typeName,
			DisplayName: cp.Name,
			APIKeyEnvs:  []string{apiKeyEnv},
			BaseURLEnv:  "",
		}

		// Default model: first manual model, or empty
		if len(cp.Models) > 0 {
			reg.DefaultModel = cp.Models[0].ID
		}

		// Build factory based on protocol
		switch cp.Protocol {
		case "anthropic":
			reg.Factory = func() (agentcore.Provider, error) {
				key := resolveAPIKey(apiKeyEnv)
				if key == "" {
					return nil, fmt.Errorf("%s is required for %s provider", apiKeyEnv, cp.Name)
				}
				return anthropic.New(anthropic.Config{APIKey: key, BaseURL: baseURL, ExtraHeaders: map[string]string{"User-Agent": UserAgent()}}), nil
			}
			reg.FetchModels = func() ([]ProviderModel, error) {
				if len(cp.Models) > 0 {
					return customModelsToProviderModels(cp.Models), nil
				}
				return fetchCustomProviderModels(baseURL, keyForFetch(apiKeyEnv), "anthropic")
			}
		case "gemini":
			reg.Factory = func() (agentcore.Provider, error) {
				key := resolveAPIKey(apiKeyEnv)
				if key == "" {
					return nil, fmt.Errorf("%s is required for %s provider", apiKeyEnv, cp.Name)
				}
				return gemini.New(gemini.Config{APIKey: key, BaseURL: baseURL, ExtraHeaders: map[string]string{"User-Agent": UserAgent()}}), nil
			}
			reg.FetchModels = func() ([]ProviderModel, error) {
				if len(cp.Models) > 0 {
					return customModelsToProviderModels(cp.Models), nil
				}
				return fetchCustomProviderModels(baseURL, keyForFetch(apiKeyEnv), "gemini")
			}
		default: // "openai/chat", "openai/responses"
			reg.Factory = func() (agentcore.Provider, error) {
				key := resolveAPIKey(apiKeyEnv)
				if key == "" {
					return nil, fmt.Errorf("%s is required for %s provider", apiKeyEnv, cp.Name)
				}
				cfg := chatcompat.Config{APIKey: key, BaseURL: baseURL, ExtraHeaders: map[string]string{"User-Agent": UserAgent()}}
				return chatcompat.New(cfg), nil
			}
			reg.FetchModels = func() ([]ProviderModel, error) {
				if len(cp.Models) > 0 {
					return customModelsToProviderModels(cp.Models), nil
				}
				return FetchCustomOpenAIModels(baseURL, keyForFetch(apiKeyEnv))
			}
		}

		RegisterProvider(reg)
	}
}

// keyForFetch resolves an API key from its env var for model fetching.
func keyForFetch(envName string) string {
	return resolveAPIKey(envName)
}

// FetchCustomOpenAIModels fetches models from an OpenAI-compatible /v1/models endpoint.
func FetchCustomOpenAIModels(baseURL, apiKey string) ([]ProviderModel, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("base URL is required to fetch models")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("API key is required to fetch models")
	}

	req, err := http.NewRequest("GET", strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	var response openAIModelsResponse
	if err := doJSON(req, &response); err != nil {
		return nil, err
	}

	models := make([]ProviderModel, 0, len(response.Data))
	for _, m := range response.Data {
		if m.ID != "" {
			models = append(models, ProviderModel{ID: m.ID})
		}
	}
	sortProviderModels(models)
	return models, nil
}

// fetchCustomProviderModels fetches models from a provider-specific endpoint.
func fetchCustomProviderModels(baseURL, apiKey, protocol string) ([]ProviderModel, error) {
	switch protocol {
	case "anthropic":
		return FetchCustomAnthropicModels(baseURL, apiKey)
	case "gemini":
		return FetchCustomGeminiModels(baseURL, apiKey)
	default:
		return FetchCustomOpenAIModels(baseURL, apiKey)
	}
}

// FetchCustomAnthropicModels fetches models from an Anthropic-compatible endpoint.
func FetchCustomAnthropicModels(baseURL, apiKey string) ([]ProviderModel, error) {
	req, err := http.NewRequest("GET", strings.TrimRight(baseURL, "/")+"/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	var response anthropicModelsResponse
	if err := doJSON(req, &response); err != nil {
		return nil, err
	}

	models := make([]ProviderModel, 0, len(response.Data))
	for _, m := range response.Data {
		if m.ID != "" {
			models = append(models, ProviderModel{ID: m.ID, Name: m.DisplayName})
		}
	}
	sortProviderModels(models)
	return models, nil
}

// FetchCustomGeminiModels fetches models from a Gemini-compatible endpoint.
func FetchCustomGeminiModels(baseURL, apiKey string) ([]ProviderModel, error) {
	req, err := http.NewRequest("GET", strings.TrimRight(baseURL, "/")+"/models?key="+urlQueryEscape(apiKey), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	var response geminiModelsResponse
	if err := doJSON(req, &response); err != nil {
		return nil, err
	}

	models := make([]ProviderModel, 0, len(response.Models))
	for _, m := range response.Models {
		if !supportsGeminiGenerateContent(m.Methods) {
			continue
		}
		id := strings.TrimPrefix(m.Name, "models/")
		if id != "" {
			models = append(models, ProviderModel{ID: id, Name: m.DisplayName, Description: m.Description})
		}
	}
	sortProviderModels(models)
	return models, nil
}

func customModelsToProviderModels(cms []CustomModel) []ProviderModel {
	models := make([]ProviderModel, 0, len(cms))
	for _, cm := range cms {
		models = append(models, ProviderModel{ID: cm.ID, Name: cm.Name, Context: cm.Context})
	}
	return models
}

// registerOpenAICompatible registers a provider backed by the OpenAI-compatible
// chat completions API. If Factory or FetchModels are pre-set on pr, they are preserved.
func registerOpenAICompatible(pr *ProviderRegistration) {
	if pr.Factory == nil {
		pr.Factory = func() (agentcore.Provider, error) {
			key := ""
			for _, env := range pr.APIKeyEnvs {
				if k := resolveAPIKey(env); k != "" {
					key = k
					break
				}
			}
			if key == "" && len(pr.APIKeyEnvs) > 0 {
				return nil, fmt.Errorf("no API key found for provider %q (tried %v)", pr.Name, pr.APIKeyEnvs)
			}
			baseURL := os.Getenv(pr.BaseURLEnv)
			if baseURL == "" {
				baseURL = pr.DefaultBaseURL
			}
			return chatcompat.New(chatcompat.Config{APIKey: key, BaseURL: baseURL, ExtraHeaders: map[string]string{"User-Agent": UserAgent()}}), nil
		}
	}
	if pr.FetchModels == nil {
		name := pr.Name
		pr.FetchModels = func() ([]ProviderModel, error) {
			return fetchOpenAICompatibleModels(name)
		}
	}
	RegisterProvider(pr)
}

// init registers the built-in providers.
func init() {
	RegisterProvider(&ProviderRegistration{
		Type:           "openai",
		Name:           "openai",
		DisplayName:    "OpenAI",
		DefaultModel:   "gpt-5.6",
		APIKeyEnvs:     []string{"OPENAI_API_KEY"},
		BaseURLEnv:     "OPENAI_BASE_URL",
		DefaultBaseURL: openaiBaseURL,
		Factory: func() (agentcore.Provider, error) {
			apiKey := resolveAPIKey("OPENAI_API_KEY")
			if apiKey == "" {
				return nil, fmt.Errorf("OPENAI_API_KEY or API_KEY is required for openai provider")
			}
			return chatcompat.New(chatcompat.Config{
				APIKey:       apiKey,
				BaseURL:      os.Getenv("OPENAI_BASE_URL"),
				ExtraHeaders: map[string]string{"User-Agent": UserAgent()},
			}), nil
		},
		FetchModels: func() ([]ProviderModel, error) {
			return fetchOpenAICompatibleModels("openai")
		},
	})
	RegisterProvider(&ProviderRegistration{
		Type:           "anthropic",
		Name:           "anthropic",
		DisplayName:    "Anthropic",
		DefaultModel:   "claude-sonnet-4-20250514",
		APIKeyEnvs:     []string{"ANTHROPIC_API_KEY"},
		BaseURLEnv:     "ANTHROPIC_BASE_URL",
		DefaultBaseURL: anthropicBaseURL,
		Factory: func() (agentcore.Provider, error) {
			apiKey := resolveAPIKey("ANTHROPIC_API_KEY")
			if apiKey == "" {
				return nil, fmt.Errorf("ANTHROPIC_API_KEY or API_KEY is required for anthropic provider")
			}
			return anthropic.New(anthropic.Config{
				APIKey:       apiKey,
				BaseURL:      os.Getenv("ANTHROPIC_BASE_URL"),
				ExtraHeaders: map[string]string{"User-Agent": UserAgent()},
			}), nil
		},
		FetchModels: func() ([]ProviderModel, error) {
			return fetchAnthropicModels()
		},
	})
	RegisterProvider(&ProviderRegistration{
		Type:           "gemini",
		Name:           "gemini",
		DisplayName:    "Google Gemini",
		DefaultModel:   "gemini-2.5-flash",
		APIKeyEnvs:     []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
		BaseURLEnv:     "GEMINI_BASE_URL",
		DefaultBaseURL: geminiBaseURL,
		Factory: func() (agentcore.Provider, error) {
			apiKey := resolveAPIKey("GEMINI_API_KEY", "GOOGLE_API_KEY")
			if apiKey == "" {
				return nil, fmt.Errorf("GEMINI_API_KEY, GOOGLE_API_KEY, or API_KEY is required for gemini provider")
			}
			return gemini.New(gemini.Config{
				APIKey:       apiKey,
				BaseURL:      os.Getenv("GEMINI_BASE_URL"),
				ExtraHeaders: map[string]string{"User-Agent": UserAgent()},
			}), nil
		},
		FetchModels: func() ([]ProviderModel, error) {
			return fetchGeminiModels()
		},
	})
	RegisterProvider(&ProviderRegistration{
		Type:           "xiaomi",
		Aliases:        []string{"mimo", "xiaomi-mimo"},
		Name:           "xiaomi",
		DisplayName:    "Xiaomi MiMo",
		DefaultModel:   "mimo-v2.5-pro",
		APIKeyEnvs:     []string{"XIAOMI_API_KEY"},
		BaseURLEnv:     "XIAOMI_BASE_URL",
		DefaultBaseURL: xiaomiBaseURL,
		Factory: func() (agentcore.Provider, error) {
			apiKey := resolveAPIKey("XIAOMI_API_KEY")
			if apiKey == "" {
				return nil, fmt.Errorf("XIAOMI_API_KEY or API_KEY is required for xiaomi provider")
			}
			baseURL := os.Getenv("XIAOMI_BASE_URL")
			if baseURL == "" {
				baseURL = xiaomiBaseURL
			}
			return chatcompat.New(chatcompat.Config{APIKey: apiKey, BaseURL: baseURL, ExtraHeaders: map[string]string{"User-Agent": UserAgent()}}), nil
		},
	})
	RegisterProvider(&ProviderRegistration{
		Type:           "openrouter",
		Name:           "openrouter",
		DisplayName:    "OpenRouter",
		DefaultModel:   "openai/gpt-5.6",
		APIKeyEnvs:     []string{"OPENROUTER_API_KEY"},
		BaseURLEnv:     "OPENROUTER_BASE_URL",
		DefaultBaseURL: openrouterBaseURL,
		Factory: func() (agentcore.Provider, error) {
			apiKey := resolveAPIKey("OPENROUTER_API_KEY")
			if apiKey == "" {
				return nil, fmt.Errorf("OPENROUTER_API_KEY or API_KEY is required for openrouter provider")
			}
			baseURL := os.Getenv("OPENROUTER_BASE_URL")
			if baseURL == "" {
				baseURL = openrouterBaseURL
			}
			return chatcompat.New(chatcompat.Config{APIKey: apiKey, BaseURL: baseURL, ExtraHeaders: map[string]string{"User-Agent": UserAgent()}}), nil
		},
		FetchModels: func() ([]ProviderModel, error) {
			models, err := FetchOpenRouterModels()
			if err != nil {
				return nil, err
			}
			result := make([]ProviderModel, 0, len(models))
			for _, m := range models {
				result = append(result, ProviderModel{ID: m.ID, Name: m.Name, Description: m.Description, Context: m.ContextLength})
			}
			return result, nil
		},
	})
	// ── OpenAI-compatible providers ──
	registerOpenAICompatible(&ProviderRegistration{Type: "deepseek", Name: "deepseek", DisplayName: "DeepSeek", DefaultModel: "deepseek-chat", APIKeyEnvs: []string{"DEEPSEEK_API_KEY"}, BaseURLEnv: "DEEPSEEK_BASE_URL", DefaultBaseURL: deepseekBaseURL})
	registerOpenAICompatible(&ProviderRegistration{Type: "xai", Name: "xai", DisplayName: "xAI Grok", DefaultModel: "grok-beta", APIKeyEnvs: []string{"XAI_API_KEY"}, BaseURLEnv: "XAI_BASE_URL", DefaultBaseURL: xaiBaseURL})
	registerOpenAICompatible(&ProviderRegistration{Type: "qwen-oauth", Aliases: []string{"alibaba-cloud", "tongyi", "qwen"}, Name: "qwen-oauth", DisplayName: "Qwen (通义千问)", DefaultModel: "qwen-plus", APIKeyEnvs: []string{"DASHSCOPE_API_KEY", "QWEN_API_KEY"}, BaseURLEnv: "QWEN_BASE_URL", DefaultBaseURL: qwenBaseURL})
	registerOpenAICompatible(&ProviderRegistration{Type: "zai", Aliases: []string{"zhipu", "glm"}, Name: "zai", DisplayName: "Z.AI (智谱 GLM)", DefaultModel: "glm-4-flash", APIKeyEnvs: []string{"GLM_API_KEY", "ZAI_API_KEY"}, BaseURLEnv: "ZAI_BASE_URL", DefaultBaseURL: zaiBaseURL})
	registerOpenAICompatible(&ProviderRegistration{Type: "stepfun", Name: "stepfun", DisplayName: "StepFun (阶跃星辰)", DefaultModel: "step-2-16k", APIKeyEnvs: []string{"STEPFUN_API_KEY"}, BaseURLEnv: "STEPFUN_BASE_URL", DefaultBaseURL: stepfunBaseURL})
	registerOpenAICompatible(&ProviderRegistration{Type: "nvidia", Name: "nvidia", DisplayName: "NVIDIA NIM", DefaultModel: "nvidia/nemotron-4-340b-instruct", APIKeyEnvs: []string{"NVIDIA_API_KEY"}, BaseURLEnv: "NVIDIA_BASE_URL", DefaultBaseURL: nvidiaBaseURL})
	registerOpenAICompatible(&ProviderRegistration{Type: "huggingface", Name: "huggingface", DisplayName: "HuggingFace", DefaultModel: "meta-llama/Llama-3.3-70B-Instruct", APIKeyEnvs: []string{"HF_TOKEN", "HUGGINGFACE_HUB_TOKEN"}, BaseURLEnv: "HUGGINGFACE_BASE_URL", DefaultBaseURL: huggingfaceBaseURL})
	registerOpenAICompatible(&ProviderRegistration{Type: "alibaba", Name: "alibaba", DisplayName: "Alibaba Cloud (Intl)", DefaultModel: "qwen-plus", APIKeyEnvs: []string{"DASHSCOPE_API_KEY"}, BaseURLEnv: "ALIBABA_BASE_URL", DefaultBaseURL: alibabaBaseURL})
	registerOpenAICompatible(&ProviderRegistration{Type: "kimi-coding", Aliases: []string{"moonshot", "kimi"}, Name: "kimi-coding", DisplayName: "Kimi (Moonshot)", DefaultModel: "moonshot-v1-auto", APIKeyEnvs: []string{"MOONSHOT_API_KEY", "KIMI_API_KEY"}, BaseURLEnv: "KIMI_BASE_URL", DefaultBaseURL: kimicodingBaseURL})
	registerOpenAICompatible(&ProviderRegistration{Type: "kimi-cn", Name: "kimi-cn", DisplayName: "Kimi CN (月之暗面)", DefaultModel: "moonshot-v1-auto", APIKeyEnvs: []string{"MOONSHOT_CN_API_KEY", "KIMI_CN_API_KEY"}, BaseURLEnv: "KIMI_CN_BASE_URL", DefaultBaseURL: kimicodingCNBaseURL})
	registerOpenAICompatible(&ProviderRegistration{Type: "nous", Name: "nous", DisplayName: "Nous Research", DefaultModel: "NousResearch/Hermes-3-Llama-3.1-405B", APIKeyEnvs: []string{"NOUS_API_KEY"}, BaseURLEnv: "NOUS_BASE_URL", DefaultBaseURL: nousBaseURL})
	registerOpenAICompatible(&ProviderRegistration{Type: "opencode-zen", Name: "opencode-zen", DisplayName: "OpenCode Zen", DefaultModel: "claude-sonnet-4-20250514", APIKeyEnvs: []string{"OPENCODE_ZEN_API_KEY"}, BaseURLEnv: "OPENCODE_BASE_URL", DefaultBaseURL: "https://opencode.ai/zen/v1"})
	RegisterProvider(&ProviderRegistration{Type: "mistral", Name: "mistral", DisplayName: "Mistral AI", DefaultModel: "mistral-large-latest", APIKeyEnvs: []string{"MISTRAL_API_KEY"}, BaseURLEnv: "MISTRAL_BASE_URL", DefaultBaseURL: "https://api.mistral.ai/v1",
		Factory: func() (agentcore.Provider, error) {
			apiKey := resolveAPIKey("MISTRAL_API_KEY")
			if apiKey == "" {
				return nil, fmt.Errorf("MISTRAL_API_KEY is required")
			}
			return mistral.New(mistral.Config{APIKey: apiKey, BaseURL: os.Getenv("MISTRAL_BASE_URL")}), nil
		},
		FetchModels: func() ([]ProviderModel, error) { return fetchOpenAICompatibleModels("mistral") },
	})
	registerOpenAICompatible(&ProviderRegistration{Type: "azure-foundry", Name: "azure-foundry", DisplayName: "Azure AI Foundry", DefaultModel: "gpt-5.6", APIKeyEnvs: []string{"AZURE_FOUNDRY_API_KEY", "AZURE_OPENAI_API_KEY"}, BaseURLEnv: "AZURE_FOUNDRY_BASE_URL"})
	registerOpenAICompatible(&ProviderRegistration{Type: "tencent-tokenhub", Name: "tencent-tokenhub", DisplayName: "Tencent TokenHub", DefaultModel: "hunyuan-turbo", APIKeyEnvs: []string{"TOKENHUB_API_KEY"}, BaseURLEnv: "TOKENHUB_BASE_URL", DefaultBaseURL: "https://tokenhub.tencentmaas.com/v1"})
	registerOpenAICompatible(&ProviderRegistration{Type: "perplexity", Name: "perplexity", DisplayName: "Perplexity", DefaultModel: "sonar-pro", APIKeyEnvs: []string{"PERPLEXITY_API_KEY"}, BaseURLEnv: "PERPLEXITY_BASE_URL", DefaultBaseURL: perplexityBaseURL})

	// ── Anthropic protocol providers ──
	RegisterProvider(&ProviderRegistration{Type: "minimax", Name: "minimax", DisplayName: "MiniMax", DefaultModel: "MiniMax-M1", APIKeyEnvs: []string{"MINIMAX_API_KEY"}, BaseURLEnv: "MINIMAX_BASE_URL", DefaultBaseURL: minimaxBaseURL,
		Factory: func() (agentcore.Provider, error) {
			apiKey := resolveAPIKey("MINIMAX_API_KEY")
			if apiKey == "" {
				return nil, fmt.Errorf("MINIMAX_API_KEY is required")
			}
			return anthropic.New(anthropic.Config{APIKey: apiKey, BaseURL: os.Getenv("MINIMAX_BASE_URL"), ExtraHeaders: map[string]string{"User-Agent": UserAgent()}}), nil
		},
	})
	RegisterProvider(&ProviderRegistration{Type: "minimax-cn", Name: "minimax-cn", DisplayName: "MiniMax CN", DefaultModel: "MiniMax-M1", APIKeyEnvs: []string{"MINIMAX_CN_API_KEY"}, BaseURLEnv: "MINIMAX_CN_BASE_URL", DefaultBaseURL: minimaxCNBaseURL,
		Factory: func() (agentcore.Provider, error) {
			apiKey := resolveAPIKey("MINIMAX_CN_API_KEY")
			if apiKey == "" {
				return nil, fmt.Errorf("MINIMAX_CN_API_KEY is required")
			}
			return anthropic.New(anthropic.Config{APIKey: apiKey, BaseURL: os.Getenv("MINIMAX_CN_BASE_URL"), ExtraHeaders: map[string]string{"User-Agent": UserAgent()}}), nil
		},
	})

	// ── OAuth / Special Auth Providers ──
	RegisterProvider(&ProviderRegistration{
		Type: "bedrock", Name: "bedrock", DisplayName: "AWS Bedrock",
		DefaultModel: "anthropic.claude-sonnet-4-20250514-v1:0",
		BaseURLEnv:   "BEDROCK_BASE_URL", NeedsBaseURL: false,
		Factory: func() (agentcore.Provider, error) {
			region := os.Getenv("AWS_DEFAULT_REGION")
			if region == "" {
				region = os.Getenv("AWS_REGION")
			}
			if region == "" {
				region = "us-east-1"
			}
			return bedrock.New(bedrock.Config{
				AccessKey:    resolveAPIKey("AWS_ACCESS_KEY_ID"),
				SecretKey:    resolveAPIKey("AWS_SECRET_ACCESS_KEY"),
				SessionToken: resolveAPIKey("AWS_SESSION_TOKEN"),
				Region:       region,
			})
		},
	})
	RegisterProvider(&ProviderRegistration{
		Type: "vertex", Name: "vertex", DisplayName: "Google Vertex AI",
		DefaultModel: "gemini-2.5-flash",
		Factory: func() (agentcore.Provider, error) {
			return vertex.NewGemini(vertex.Config{
				Project: os.Getenv("VERTEX_PROJECT"),
				Region:  os.Getenv("VERTEX_REGION"),
			})
		},
	})

	RegisterProvider(&ProviderRegistration{Type: "custom",
		Name:         "custom",
		DisplayName:  "Custom",
		DefaultModel: "gpt-5.6",
		APIKeyEnvs:   []string{"CUSTOM_API_KEY"},
		NeedsBaseURL: true,
		BaseURLEnv:   "CUSTOM_BASE_URL",
		Factory: func() (agentcore.Provider, error) {
			apiKey := resolveAPIKey("CUSTOM_API_KEY")
			if apiKey == "" {
				return nil, fmt.Errorf("CUSTOM_API_KEY or API_KEY is required for custom provider")
			}
			baseURL := os.Getenv("CUSTOM_BASE_URL")
			if baseURL == "" {
				return nil, fmt.Errorf("CUSTOM_BASE_URL is required for custom provider")
			}
			return chatcompat.New(chatcompat.Config{APIKey: apiKey, BaseURL: baseURL, ExtraHeaders: map[string]string{"User-Agent": UserAgent()}}), nil
		},
	})
}
