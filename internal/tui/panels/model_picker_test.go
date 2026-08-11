package panels

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covonaut/tui/core"
)

type fakeModelPickerBackend struct {
	providers []ProviderOption
	scoped    []string

	providerDisplay map[string]string
	defaultModels   map[string]string
	apiKeyEnvs      map[string]string
	configured      map[string]bool

	providerModels map[string][]ProviderModel
	providerErrs   map[string]error

	customModels []ProviderModel
	customErr    error

	savedKeys map[string]string

	customByType map[string]CustomProvider
	upserts      []CustomProvider
	deletes      []string

	mu sync.Mutex
}

func (f *fakeModelPickerBackend) ListProviders() []ProviderOption {
	out := make([]ProviderOption, len(f.providers))
	copy(out, f.providers)
	return out
}

func (f *fakeModelPickerBackend) ProviderDisplayName(providerType string) string {
	if v, ok := f.providerDisplay[providerType]; ok {
		return v
	}
	return providerType
}

func (f *fakeModelPickerBackend) NormalizeProvider(providerType string) string {
	return providerType
}

func (f *fakeModelPickerBackend) DefaultModel(providerType string) string {
	if v, ok := f.defaultModels[providerType]; ok {
		return v
	}
	return ""
}

func (f *fakeModelPickerBackend) ProviderAPIKeyEnv(providerType string) string {
	if v, ok := f.apiKeyEnvs[providerType]; ok {
		return v
	}
	return "API_KEY"
}

func (f *fakeModelPickerBackend) HasProviderConfigured(providerType string) bool {
	return f.configured[providerType]
}

func (f *fakeModelPickerBackend) FetchProviderModels(providerType string) ([]ProviderModel, error) {
	if err := f.providerErrs[providerType]; err != nil {
		return nil, err
	}
	models := f.providerModels[providerType]
	out := make([]ProviderModel, len(models))
	copy(out, models)
	return out, nil
}

func (f *fakeModelPickerBackend) FetchCustomProviderModels(provider CustomProvider) ([]ProviderModel, error) {
	if f.customErr != nil {
		return nil, f.customErr
	}
	out := make([]ProviderModel, len(f.customModels))
	copy(out, f.customModels)
	return out, nil
}

func (f *fakeModelPickerBackend) SaveAPIKey(env, value string) error {
	if f.savedKeys == nil {
		f.savedKeys = map[string]string{}
	}
	f.savedKeys[env] = value
	return nil
}

func (f *fakeModelPickerBackend) CustomProviderTypeName(name string) string {
	return "custom_" + name
}

func (f *fakeModelPickerBackend) GetCustomProvider(providerType string) (CustomProvider, bool) {
	cp, ok := f.customByType[providerType]
	return cp, ok
}

func (f *fakeModelPickerBackend) DeleteCustomProvider(providerType string) error {
	f.mu.Lock()
	f.deletes = append(f.deletes, providerType)
	f.mu.Unlock()
	return nil
}

func (f *fakeModelPickerBackend) UpsertCustomProvider(provider CustomProvider) (string, error) {
	f.mu.Lock()
	f.upserts = append(f.upserts, provider)
	f.mu.Unlock()
	return "custom_" + provider.Name, nil
}

func (f *fakeModelPickerBackend) ScopedModels() []string {
	out := make([]string, len(f.scoped))
	copy(out, f.scoped)
	return out
}

func newTestBackend() *fakeModelPickerBackend {
	return &fakeModelPickerBackend{
		providers: []ProviderOption{
			{Type: "openrouter", DisplayName: "OpenRouter", Configured: true},
			{Type: "needkey", DisplayName: "Need Key", Configured: false},
			{Type: "custom", DisplayName: "Custom", Configured: true},
		},
		providerDisplay: map[string]string{
			"openrouter": "OpenRouter",
			"needkey":    "Need Key",
		},
		defaultModels: map[string]string{
			"openrouter": "openai/gpt-5.6",
			"needkey":    "needkey/model-default",
		},
		apiKeyEnvs: map[string]string{
			"needkey": "NEEDKEY_API_KEY",
		},
		configured: map[string]bool{
			"openrouter": true,
			"needkey":    false,
		},
		providerErrs: map[string]error{},
		providerModels: map[string][]ProviderModel{
			"openrouter": {
				{ID: "openai/gpt-5.6", Name: "GPT-5.6"},
				{ID: "anthropic/claude-3.5-sonnet", Name: "Claude Sonnet"},
			},
		},
		customByType: map[string]CustomProvider{},
		savedKeys:    map[string]string{},
		customModels: []ProviderModel{{ID: "custom-model-a", Name: "Custom A", Context: 128000}},
	}
}

func TestModelPickerEscOnProviderCancels(t *testing.T) {
	backend := newTestBackend()
	picker := NewModelPicker("openrouter", "openai/gpt-5.6", backend)
	cancelled := make(chan struct{}, 1)
	picker.SetOnCancel(func() {
		cancelled <- struct{}{}
	})

	picker.Update(core.KeyMsg{Data: "\x1b"})

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("expected Esc on provider step to cancel picker")
	}
}

func TestModelPickerProviderSelectionRequiresAPIKey(t *testing.T) {
	backend := newTestBackend()
	picker := NewModelPicker("openrouter", "openai/gpt-5.6", backend)

	picker.selected = 1
	picker.clearIgnoredEnter()
	cmd := picker.confirm()
	if cmd != nil {
		t.Fatal("expected no async cmd when entering API key step")
	}
	if picker.step != modelPickerAPIKey {
		t.Fatalf("expected API key step, got %v", picker.step)
	}
	if picker.apiKeyEnv != "NEEDKEY_API_KEY" {
		t.Fatalf("expected NEEDKEY_API_KEY, got %q", picker.apiKeyEnv)
	}
}

func TestModelPickerAsyncModelResultTransitionAndFilter(t *testing.T) {
	backend := newTestBackend()
	picker := NewModelPicker("openrouter", "", backend)

	picker.selected = 0
	picker.clearIgnoredEnter()
	cmd := picker.confirm()
	if cmd == nil {
		t.Fatal("expected async fetch command")
	}
	if picker.step != modelPickerLoading {
		t.Fatalf("expected loading step, got %v", picker.step)
	}

	msg := cmd()
	picker.Update(msg)
	if picker.step != modelPickerModels {
		t.Fatalf("expected models step, got %v", picker.step)
	}
	if len(picker.filtered) < 2 {
		t.Fatalf("expected filtered models, got %v", picker.filtered)
	}

	picker.typeRune('c')
	picker.typeRune('l')
	if len(picker.filtered) == 0 {
		t.Fatal("expected non-empty filtered model list")
	}
}

func TestModelPickerCustomProviderUpsertFlow(t *testing.T) {
	backend := newTestBackend()
	picker := NewModelPicker("openrouter", "openai/gpt-5.6", backend)

	applied := make(chan [2]string, 1)
	picker.SetOnApply(func(provider, model string) {
		applied <- [2]string{provider, model}
	})

	picker.selected = 2
	picker.clearIgnoredEnter()
	if cmd := picker.confirm(); cmd != nil {
		t.Fatal("custom provider selection should not start async command")
	}
	if picker.step != modelPickerCustomName {
		t.Fatalf("expected custom name step, got %v", picker.step)
	}

	picker.customName = "myprovider"
	picker.confirm()
	if picker.step != modelPickerCustomProtocol {
		t.Fatalf("expected custom protocol step, got %v", picker.step)
	}
	picker.confirm()
	picker.customBaseURL = "https://example.invalid/v1"
	picker.confirm()
	picker.customAPIKey = "secret"

	cmd := picker.confirm()
	if cmd == nil {
		t.Fatal("expected async custom model fetch command")
	}
	picker.Update(cmd())
	if picker.step != modelPickerCustomModels {
		t.Fatalf("expected custom models step, got %v", picker.step)
	}

	picker.modelSelected = 0
	picker.confirm()

	select {
	case got := <-applied:
		if got[0] != "custom_myprovider" {
			t.Fatalf("expected custom provider type, got %q", got[0])
		}
		if got[1] != "custom-model-a" {
			t.Fatalf("expected selected model id, got %q", got[1])
		}
	case <-time.After(time.Second):
		t.Fatal("expected apply callback")
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(backend.upserts))
	}
	if backend.upserts[0].Name != "myprovider" {
		t.Fatalf("expected upserted provider name myprovider, got %q", backend.upserts[0].Name)
	}
	if backend.savedKeys["COVO_MYPROVIDER_API_KEY"] != "secret" {
		t.Fatalf("expected saved API key for custom env, got %v", backend.savedKeys)
	}
}

func TestModelPickerBackendErrorFallsBackToManualInput(t *testing.T) {
	backend := newTestBackend()
	backend.providerErrs["openrouter"] = errors.New("network unavailable")

	picker := NewModelPicker("openrouter", "openai/gpt-5.6", backend)
	picker.selected = 0
	picker.clearIgnoredEnter()
	cmd := picker.confirm()
	if cmd == nil {
		t.Fatal("expected async fetch command")
	}
	picker.Update(cmd())

	if picker.step != modelPickerManual {
		t.Fatalf("expected manual input step, got %v", picker.step)
	}
	if picker.manualInput != "openai/gpt-5.6" {
		t.Fatalf("expected manual default model fallback, got %q", picker.manualInput)
	}
}
