package cli

import (
	"strings"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/provider/anthropic"
	"github.com/covoyage/covonaut/provider/chatcompat"
	"github.com/covoyage/covonaut/provider/gemini"
)

// BuildAuxiliaryProvider builds a provider for an auxiliary task from a
// provider type, optional base URL, and optional API key. This uses the
// full provider registry so all registered providers are supported.
// It is designed to be passed as agent.AuxiliaryProviderBuilder.
func BuildAuxiliaryProvider(providerType, baseURL, apiKey string) (agentcore.Provider, error) {
	providerType = strings.ToLower(strings.TrimSpace(providerType))
	if providerType == "" {
		providerType = "openai"
	}

	// If explicit credentials are provided, build directly without env lookup.
	if apiKey != "" || baseURL != "" {
		return buildAuxProviderDirect(providerType, baseURL, apiKey)
	}

	// Fall back to the standard provider registry (reads env vars for keys).
	reg, ok := GetProviderRegistration(providerType)
	if !ok {
		// Unknown provider — try OpenAI-compatible as fallback.
		return buildAuxProviderDirect("openai", "", "")
	}
	return reg.Factory()
}

// buildAuxProviderDirect builds a provider from explicit credentials,
// inferring the protocol from the provider type.
func buildAuxProviderDirect(providerType, baseURL, apiKey string) (agentcore.Provider, error) {
	switch providerType {
	case "anthropic":
		return anthropic.New(anthropic.Config{APIKey: apiKey, BaseURL: baseURL, ExtraHeaders: map[string]string{"User-Agent": UserAgent()}}), nil
	case "gemini":
		return gemini.New(gemini.Config{APIKey: apiKey, BaseURL: baseURL, ExtraHeaders: map[string]string{"User-Agent": UserAgent()}}), nil
	default:
		// All OpenAI-compatible providers use chatcompat.
		return chatcompat.New(chatcompat.Config{APIKey: apiKey, BaseURL: baseURL, ExtraHeaders: map[string]string{"User-Agent": UserAgent()}}), nil
	}
}

// ResolveAuxiliaryProviderBuilder returns a provider builder function suitable
// for passing to agent.CovoAgentConfig.AuxiliaryProviderBuilder. It uses the
// full provider registry so all registered providers work.
func ResolveAuxiliaryProviderBuilder() func(providerType, baseURL, apiKey string) (agentcore.Provider, error) {
	return BuildAuxiliaryProvider
}
