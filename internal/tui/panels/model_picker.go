package panels

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
)

type modelPickerStep int

const (
	modelPickerProvider modelPickerStep = iota
	modelPickerProviderSearch
	modelPickerLoading
	modelPickerModels
	modelPickerManual
	modelPickerAPIKey
	modelPickerCustomName
	modelPickerCustomProtocol
	modelPickerCustomBaseURL
	modelPickerCustomAPIKey
	modelPickerCustomLoading
	modelPickerCustomModels
	modelPickerCustomContext
)

const providerPageSize = 15
const customModelOption = "Enter custom model name"
const modelPickerPageSize = 20

type ProviderModel struct {
	ID          string
	Name        string
	Description string
	Context     int
}

type CustomModel struct {
	Name    string
	ID      string
	Context int
}

type CustomProvider struct {
	Name      string
	Protocol  string
	BaseURL   string
	APIKeyEnv string
	Models    []CustomModel
}

type ProviderOption struct {
	Type        string
	DisplayName string
	Configured  bool
}

type ModelPickerBackend interface {
	ListProviders() []ProviderOption
	ProviderDisplayName(providerType string) string
	NormalizeProvider(providerType string) string
	DefaultModel(providerType string) string
	ProviderAPIKeyEnv(providerType string) string
	HasProviderConfigured(providerType string) bool
	FetchProviderModels(providerType string) ([]ProviderModel, error)
	FetchCustomProviderModels(provider CustomProvider) ([]ProviderModel, error)
	SaveAPIKey(env, value string) error
	CustomProviderTypeName(name string) string
	GetCustomProvider(providerType string) (CustomProvider, bool)
	DeleteCustomProvider(providerType string) error
	UpsertCustomProvider(provider CustomProvider) (string, error)
	ScopedModels() []string
}

type providerModelsLoadedMsg struct {
	core.MsgBase
	provider string
	models   []ProviderModel
	err      error
}

type customModelsLoadedMsg struct {
	core.MsgBase
	models []ProviderModel
	err    error
}

type cursorTickMsg struct {
	core.MsgBase
}

func cursorTickCmd() core.Cmd {
	return func() core.Msg {
		time.Sleep(500 * time.Millisecond)
		return cursorTickMsg{}
	}
}

type ModelPicker struct {
	mu sync.RWMutex

	backend ModelPickerBackend

	step      modelPickerStep
	providers []ProviderOption

	selected      int
	modelSelected int
	modelOffset   int

	provider string
	models   []ProviderModel
	filtered []string

	search          string
	providerSearch  string
	providerOffset  int
	manualInput     string
	defaultModel    string
	currentProvider string
	currentModel    string
	fetchErr        error

	ignoreEnterOnce bool
	onApply         func(provider, model string)
	onCancel        func()
	km              *terminal.KeybindingsManager

	customName             string
	customProtocol         string
	customProtocolSelected int
	customProtocols        []string
	customBaseURL          string
	customAPIKey           string
	customModels           []ProviderModel
	customFetchedModels    []CustomModel
	customContextInput     string
	customBaseURLErr       string
	selectedModelID        string

	apiKeyEnv   string
	apiKeyInput string

	cursorVisible    bool
	cursorTickActive bool
	caret            int
}

var customProtocolOptions = []string{
	"openai/chat",
	"openai/responses",
	"anthropic",
	"gemini",
}

func NewModelPicker(currentProvider, currentModel string, backend ModelPickerBackend) *ModelPicker {
	providers := nilProviderOptions(backend.ListProviders())
	selected := 0
	for i, p := range providers {
		if p.Type == currentProvider {
			selected = i
			break
		}
	}
	return &ModelPicker{
		backend:         backend,
		step:            modelPickerProvider,
		providers:       providers,
		selected:        selected,
		currentProvider: currentProvider,
		currentModel:    currentModel,
		defaultModel:    currentModel,
		ignoreEnterOnce: true,
		customProtocols: customProtocolOptions,
		km: terminal.NewKeybindingsManager(map[string]terminal.KeybindingDef{
			"picker.up":      {DefaultKeys: []string{"up", "ctrl+p"}},
			"picker.down":    {DefaultKeys: []string{"down", "ctrl+n"}},
			"picker.confirm": {DefaultKeys: []string{"enter"}},
			"picker.back":    {DefaultKeys: []string{"esc"}},
			"picker.delete":  {DefaultKeys: []string{"backspace", "ctrl+h"}},
		}),
	}
}

func nilProviderOptions(options []ProviderOption) []ProviderOption {
	if len(options) == 0 {
		return []ProviderOption{}
	}
	out := make([]ProviderOption, 0, len(options))
	for _, o := range options {
		out = append(out, o)
	}
	return out
}

func (p *ModelPicker) SetOnApply(fn func(provider, model string)) { p.onApply = fn }
func (p *ModelPicker) SetOnCancel(fn func())                      { p.onCancel = fn }
func (p *ModelPicker) Invalidate()                                {}

func (p *ModelPicker) Update(msg core.Msg) core.Cmd {
	var cmd core.Cmd

	switch m := msg.(type) {
	case cursorTickMsg:
		p.mu.Lock()
		if isTextInputStep(p.step) {
			p.cursorVisible = !p.cursorVisible
			p.mu.Unlock()
			return cursorTickCmd()
		}
		p.cursorTickActive = false
		p.mu.Unlock()
		return nil
	case core.KeyMsg:
		cmd = p.processKeys(m.Data)
	case core.PasteMsg:
		p.handlePaste(m.Text)
	case providerModelsLoadedMsg:
		p.mu.Lock()
		if m.provider != p.provider {
			p.mu.Unlock()
			return nil
		}
		p.fetchErr = m.err
		if m.err != nil || len(m.models) == 0 {
			p.step = modelPickerManual
			p.manualInput = p.defaultModel
			p.resetCaretToEndLocked()
			p.mu.Unlock()
			p.cursorTickActive = true
			return cursorTickCmd()
		}
		p.step = modelPickerModels
		p.models = copyProviderModels(m.models)
		p.search = ""
		p.modelSelected = 0
		p.modelOffset = 0
		p.applyModelFilterLocked()
		p.resetCaretToEndLocked()
		p.mu.Unlock()
		p.cursorTickActive = true
		return cursorTickCmd()
	case customModelsLoadedMsg:
		p.mu.Lock()
		p.fetchErr = m.err
		p.customFetchedModels = nil
		for _, pm := range m.models {
			p.customFetchedModels = append(p.customFetchedModels, CustomModel{
				ID:      pm.ID,
				Name:    pm.Name,
				Context: pm.Context,
			})
		}
		if m.err != nil || len(m.models) == 0 {
			p.step = modelPickerManual
			p.manualInput = p.defaultModel
			p.resetCaretToEndLocked()
			p.mu.Unlock()
			p.cursorTickActive = true
			return cursorTickCmd()
		}
		p.step = modelPickerCustomModels
		p.customModels = copyProviderModels(m.models)
		p.models = copyProviderModels(m.models)
		p.search = ""
		p.modelSelected = 0
		p.modelOffset = 0
		p.applyModelFilterLocked()
		p.resetCaretToEndLocked()
		p.mu.Unlock()
		p.cursorTickActive = true
		return cursorTickCmd()
	}

	p.mu.RLock()
	step := p.step
	p.mu.RUnlock()
	if isTextInputStep(step) && !p.cursorTickActive {
		p.cursorTickActive = true
		cmd = core.Batch(cmd, cursorTickCmd())
	}
	return cmd
}

func (p *ModelPicker) processKeys(data string) core.Cmd {
	var cmd core.Cmd
	for _, key := range terminal.ParseKeys(data) {
		if key.IsRelease() {
			continue
		}
		if next := p.processKey(key); next != nil {
			cmd = next
		}
	}
	return cmd
}

func isTextInputStep(step modelPickerStep) bool {
	return step == modelPickerModels || step == modelPickerManual ||
		step == modelPickerCustomName || step == modelPickerCustomBaseURL ||
		step == modelPickerCustomAPIKey || step == modelPickerAPIKey ||
		step == modelPickerCustomContext || step == modelPickerCustomModels
}

func (p *ModelPicker) processKey(key terminal.Key) core.Cmd {
	p.mu.RLock()
	step := p.step
	p.mu.RUnlock()

	var cmd core.Cmd
	switch key.Name {
	case "up":
		p.clearIgnoredEnter()
		p.move(-1)
	case "down":
		p.clearIgnoredEnter()
		p.move(1)
	case "left", "right":
		if isCaretInputStep(step) {
			p.clearIgnoredEnter()
			if key.Name == "left" {
				p.moveCaret(-1)
			} else {
				p.moveCaret(1)
			}
		}
	case "home", "end":
		if isCaretInputStep(step) {
			p.clearIgnoredEnter()
			p.setCaretBoundary(key.Name == "home")
		}
	case "enter":
		if p.consumeIgnoredEnter() {
			cmd = nil
		} else {
			cmd = p.confirm()
		}
	case "escape":
		p.clearIgnoredEnter()
		p.back()
	case "backspace":
		p.clearIgnoredEnter()
		p.backspace()
	default:
		p.clearIgnoredEnter()
		if isCaretInputStep(step) && key.Mods&terminal.ModCtrl != 0 {
			switch key.Name {
			case "b":
				p.moveCaret(-1)
			case "f":
				p.moveCaret(1)
			case "a":
				p.setCaretBoundary(true)
			case "e":
				p.setCaretBoundary(false)
			}
		}
		isTextInput := step == modelPickerModels || step == modelPickerManual ||
			step == modelPickerCustomName || step == modelPickerCustomBaseURL ||
			step == modelPickerCustomAPIKey || step == modelPickerAPIKey ||
			step == modelPickerCustomContext
		if isTextInput && key.IsPrintable() {
			p.typeRune(key.Rune)
		}
		if step == modelPickerProvider {
			if key.Name == "d" || key.Name == "D" {
				cmd = p.handleDeleteProvider()
			}
			if key.Name == "e" || key.Name == "E" {
				cmd = p.handleEditProvider()
			}
			if key.Rune == '/' {
				p.enterProviderSearch()
			}
			if key.Name == "pgup" {
				p.pageProvider(-1)
			}
			if key.Name == "pgdn" {
				p.pageProvider(1)
			}
		}

		if step == modelPickerProviderSearch {
			if key.IsPrintable() {
				p.typeRune(key.Rune)
			}
		}
	}

	p.mu.RLock()
	afterStep := p.step
	p.mu.RUnlock()
	if isTextInputStep(afterStep) && !p.cursorTickActive {
		p.cursorTickActive = true
		cmd = core.Batch(cmd, cursorTickCmd())
	}
	return cmd
}

func (p *ModelPicker) move(delta int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.providers)
	if p.step == modelPickerModels || p.step == modelPickerCustomModels {
		n = len(p.filtered)
	}
	if p.step == modelPickerCustomProtocol {
		n = len(p.customProtocols)
	}
	if n == 0 || p.step == modelPickerLoading || p.step == modelPickerCustomLoading ||
		p.step == modelPickerManual || p.step == modelPickerCustomName ||
		p.step == modelPickerCustomBaseURL || p.step == modelPickerCustomAPIKey ||
		p.step == modelPickerAPIKey || p.step == modelPickerCustomContext {
		return
	}
	if p.step == modelPickerProvider || p.step == modelPickerCustomProtocol {
		if p.step == modelPickerProvider {
			filtered := p.filteredProviderList()
			n = len(filtered)
			if n == 0 {
				return
			}
			p.selected = wrapIndex(p.selected+delta, n)
			if p.selected < p.providerOffset {
				p.providerOffset = p.selected
			}
			if p.selected >= p.providerOffset+providerPageSize {
				p.providerOffset = p.selected - providerPageSize + 1
			}
		} else {
			p.selected = wrapIndex(p.selected+delta, n)
		}
		return
	}
	p.modelSelected = wrapIndex(p.modelSelected+delta, n)
	p.modelOffset = adjustPickerOffset(p.modelSelected, p.modelOffset, modelPickerPageSize, n)
}

func (p *ModelPicker) filteredProviderList() []ProviderOption {
	if p.providerSearch == "" {
		return copyProviderOptions(p.providers)
	}
	s := strings.ToLower(p.providerSearch)
	var out []ProviderOption
	for _, prov := range p.providers {
		dn := strings.ToLower(prov.DisplayName)
		if strings.Contains(dn, s) || strings.Contains(strings.ToLower(prov.Type), s) {
			out = append(out, prov)
		}
	}
	return out
}

func copyProviderOptions(options []ProviderOption) []ProviderOption {
	if len(options) == 0 {
		return nil
	}
	out := make([]ProviderOption, len(options))
	copy(out, options)
	return out
}

func (p *ModelPicker) enterProviderSearch() {
	p.mu.Lock()
	p.step = modelPickerProviderSearch
	p.providerSearch = ""
	p.caret = 0
	p.mu.Unlock()
}

func (p *ModelPicker) pageProvider(dir int) {
	filtered := p.filteredProviderList()
	n := len(filtered)
	if n == 0 {
		return
	}
	p.providerOffset += dir * providerPageSize
	if p.providerOffset < 0 {
		p.providerOffset = 0
	}
	if p.providerOffset >= n {
		p.providerOffset = (n - 1) / providerPageSize * providerPageSize
	}
	if p.providerOffset < 0 {
		p.providerOffset = 0
	}
	if p.selected < p.providerOffset || p.selected >= p.providerOffset+providerPageSize {
		p.selected = p.providerOffset
	}
}

func (p *ModelPicker) confirm() core.Cmd {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.step {
	case modelPickerProviderSearch:
		p.step = modelPickerProvider
		p.selected = 0
		p.providerOffset = 0
		return nil
	case modelPickerProvider:
		filtered := p.filteredProviderList()
		if p.selected >= len(filtered) {
			return nil
		}
		selectedProvider := filtered[p.selected]
		if selectedProvider.Type == "custom" {
			p.step = modelPickerCustomName
			p.customName = ""
			p.customProtocol = ""
			p.customProtocolSelected = 0
			p.customBaseURL = ""
			p.customAPIKey = ""
			p.caret = 0
			return nil
		}
		provider := p.backend.NormalizeProvider(selectedProvider.Type)
		if !p.backend.HasProviderConfigured(provider) {
			p.provider = provider
			p.apiKeyEnv = p.backend.ProviderAPIKeyEnv(provider)
			p.apiKeyInput = ""
			p.step = modelPickerAPIKey
			p.caret = 0
			return nil
		}
		p.provider = provider
		p.defaultModel = p.currentModel
		if provider != p.currentProvider || p.defaultModel == "" {
			p.defaultModel = p.backend.DefaultModel(provider)
		}
		p.step = modelPickerLoading
		return func() core.Msg {
			models, err := p.backend.FetchProviderModels(provider)
			return providerModelsLoadedMsg{provider: provider, models: models, err: err}
		}
	case modelPickerAPIKey:
		apiKey := strings.TrimSpace(p.apiKeyInput)
		if apiKey != "" {
			_ = p.backend.SaveAPIKey(p.apiKeyEnv, apiKey)
		}
		p.defaultModel = p.currentModel
		if p.provider != p.currentProvider || p.defaultModel == "" {
			p.defaultModel = p.backend.DefaultModel(p.provider)
		}
		p.step = modelPickerLoading
		provider := p.provider
		return func() core.Msg {
			models, err := p.backend.FetchProviderModels(provider)
			return providerModelsLoadedMsg{provider: provider, models: models, err: err}
		}
	case modelPickerCustomName:
		name := strings.TrimSpace(p.customName)
		if name == "" {
			return nil
		}
		p.customName = name
		p.customBaseURLErr = ""
		p.step = modelPickerCustomProtocol
		p.customProtocolSelected = 0
		p.selected = 0
		return nil
	case modelPickerCustomProtocol:
		p.customProtocol = p.customProtocols[p.customProtocolSelected]
		p.step = modelPickerCustomBaseURL
		p.customBaseURL = ""
		p.caret = 0
		return nil
	case modelPickerCustomBaseURL:
		baseURL := strings.TrimSpace(p.customBaseURL)
		if baseURL == "" {
			p.customBaseURLErr = "Base URL is required. Please enter a valid endpoint."
			return nil
		}
		p.customBaseURL = baseURL
		p.customBaseURLErr = ""
		p.step = modelPickerCustomAPIKey
		p.customAPIKey = ""
		p.caret = 0
		return nil
	case modelPickerCustomAPIKey:
		apiKey := strings.TrimSpace(p.customAPIKey)
		name := p.customName
		protocol := p.customProtocol
		baseURL := p.customBaseURL
		apiKeyEnv := p.apiKeyEnv
		if apiKeyEnv == "" {
			apiKeyEnv = apiKeyEnvForProvider(name)
		}
		if apiKey != "" {
			_ = p.backend.SaveAPIKey(apiKeyEnv, apiKey)
		}
		p.customName = name
		p.customProtocol = protocol
		p.customBaseURL = baseURL
		p.apiKeyEnv = apiKeyEnv
		cp := CustomProvider{
			Name:      name,
			Protocol:  protocol,
			BaseURL:   baseURL,
			APIKeyEnv: apiKeyEnv,
		}
		p.provider = p.backend.CustomProviderTypeName(name)
		p.defaultModel = ""
		p.step = modelPickerCustomLoading
		return func() core.Msg {
			models, err := p.backend.FetchCustomProviderModels(cp)
			return customModelsLoadedMsg{models: models, err: err}
		}
	case modelPickerCustomModels:
		if len(p.filtered) == 0 {
			return nil
		}
		choice := p.filtered[p.modelSelected]
		if choice == customModelOption {
			p.step = modelPickerManual
			p.manualInput = p.defaultModel
			p.resetCaretToEndLocked()
			return nil
		}
		p.selectedModelID = choice
		if p.needsContextPrompt(choice) {
			p.step = modelPickerCustomContext
			p.customContextInput = ""
			p.caret = 0
			return nil
		}
		return p.finalizeCustomProvider(choice)
	case modelPickerCustomContext:
		ctxValue := strings.TrimSpace(p.customContextInput)
		if ctxValue != "" {
			if n := parseSize(ctxValue); n > 0 {
				p.updateCustomModelContext(p.selectedModelID, n)
			}
		}
		return p.finalizeCustomProvider(p.selectedModelID)
	case modelPickerModels:
		if len(p.filtered) == 0 {
			return nil
		}
		choice := p.filtered[p.modelSelected]
		if choice == customModelOption {
			p.step = modelPickerManual
			p.manualInput = p.defaultModel
			p.resetCaretToEndLocked()
			return nil
		}
		if p.onApply != nil {
			go p.onApply(p.provider, choice)
		}
	case modelPickerManual:
		model := strings.TrimSpace(p.manualInput)
		if model == "" {
			model = p.defaultModel
		}
		if strings.HasPrefix(p.provider, "custom_") {
			p.selectedModelID = model
			if len(p.customFetchedModels) > 0 {
				return p.finalizeCustomProvider(model)
			}
			if p.needsContextPrompt(model) {
				p.step = modelPickerCustomContext
				p.customContextInput = ""
				p.caret = 0
				return nil
			}
			return p.finalizeCustomProvider(model)
		}
		if p.onApply != nil {
			go p.onApply(p.provider, model)
		}
	}
	return nil
}

func (p *ModelPicker) back() {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.step {
	case modelPickerProviderSearch:
		p.providerSearch = ""
		p.step = modelPickerProvider
		p.caret = 0
	case modelPickerProvider:
		if p.providerSearch != "" {
			p.providerSearch = ""
			p.selected = 0
			p.providerOffset = 0
		} else {
			if p.onCancel != nil {
				go p.onCancel()
			}
		}
	case modelPickerAPIKey:
		p.step = modelPickerProvider
		p.apiKeyInput = ""
		p.caret = 0
	case modelPickerCustomName:
		p.step = modelPickerProvider
		p.customName = ""
		p.caret = 0
	case modelPickerCustomProtocol:
		p.step = modelPickerCustomName
		p.customProtocol = ""
		p.resetCaretToEndLocked()
	case modelPickerCustomBaseURL:
		p.step = modelPickerCustomProtocol
		p.customBaseURL = ""
		p.caret = 0
	case modelPickerCustomAPIKey:
		p.step = modelPickerCustomBaseURL
		p.customAPIKey = ""
		p.resetCaretToEndLocked()
	case modelPickerCustomContext:
		if len(p.customFetchedModels) > 0 {
			p.step = modelPickerCustomModels
			p.resetCaretToEndLocked()
		} else {
			p.step = modelPickerManual
			p.manualInput = p.defaultModel
			p.resetCaretToEndLocked()
		}
		p.customContextInput = ""
	case modelPickerCustomLoading, modelPickerCustomModels, modelPickerLoading, modelPickerModels, modelPickerManual:
		p.step = modelPickerProvider
		p.fetchErr = nil
		p.search = ""
		p.manualInput = ""
		p.customName = ""
		p.customProtocol = ""
		p.customBaseURL = ""
		p.customAPIKey = ""
		p.customFetchedModels = nil
		p.customContextInput = ""
		p.customBaseURLErr = ""
		p.selectedModelID = ""
		p.apiKeyEnv = ""
		p.apiKeyInput = ""
		p.caret = 0
	}
}

func (p *ModelPicker) backspace() {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.step {
	case modelPickerProviderSearch:
		p.providerSearch = deleteRuneBeforeCaret(p.providerSearch, &p.caret)
	case modelPickerModels, modelPickerCustomModels:
		old := p.search
		p.search = deleteRuneBeforeCaret(p.search, &p.caret)
		if p.search != old {
			p.modelSelected = 0
			p.modelOffset = 0
			p.applyModelFilterLocked()
		}
	case modelPickerManual:
		p.manualInput = deleteRuneBeforeCaret(p.manualInput, &p.caret)
	case modelPickerCustomName:
		p.customName = deleteRuneBeforeCaret(p.customName, &p.caret)
	case modelPickerCustomBaseURL:
		p.customBaseURL = deleteRuneBeforeCaret(p.customBaseURL, &p.caret)
	case modelPickerCustomAPIKey:
		p.customAPIKey = deleteRuneBeforeCaret(p.customAPIKey, &p.caret)
	case modelPickerAPIKey:
		p.apiKeyInput = deleteRuneBeforeCaret(p.apiKeyInput, &p.caret)
	case modelPickerCustomContext:
		p.customContextInput = deleteRuneBeforeCaret(p.customContextInput, &p.caret)
	}
}

func (p *ModelPicker) typeRune(r rune) {
	if r == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.step {
	case modelPickerProviderSearch:
		p.providerSearch = insertRuneAtCaret(p.providerSearch, &p.caret, r)
	case modelPickerModels, modelPickerCustomModels:
		p.search = insertRuneAtCaret(p.search, &p.caret, r)
		p.modelSelected = 0
		p.modelOffset = 0
		p.applyModelFilterLocked()
	case modelPickerManual:
		p.manualInput = insertRuneAtCaret(p.manualInput, &p.caret, r)
	case modelPickerCustomName:
		p.customName = insertRuneAtCaret(p.customName, &p.caret, r)
	case modelPickerCustomBaseURL:
		p.customBaseURL = insertRuneAtCaret(p.customBaseURL, &p.caret, r)
	case modelPickerCustomAPIKey:
		p.customAPIKey = insertRuneAtCaret(p.customAPIKey, &p.caret, r)
	case modelPickerAPIKey:
		p.apiKeyInput = insertRuneAtCaret(p.apiKeyInput, &p.caret, r)
	case modelPickerCustomContext:
		p.customContextInput = insertRuneAtCaret(p.customContextInput, &p.caret, r)
	}
}

func (p *ModelPicker) consumeIgnoredEnter() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.ignoreEnterOnce {
		return false
	}
	p.ignoreEnterOnce = false
	return true
}

func (p *ModelPicker) clearIgnoredEnter() {
	p.mu.Lock()
	p.ignoreEnterOnce = false
	p.mu.Unlock()
}

func (p *ModelPicker) applyModelFilterLocked() {
	scoped := p.backend.ScopedModels()
	p.filtered = filterProviderModels(p.models, p.search, 0, scoped...)
	p.filtered = appendUnique(p.filtered, p.defaultModel, customModelOption)
}

func (p *ModelPicker) handleDeleteProvider() core.Cmd {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.selected < 0 || p.selected >= len(p.providers) {
		return nil
	}
	selectedProvider := p.providers[p.selected].Type
	if !strings.HasPrefix(selectedProvider, "custom_") {
		return nil
	}
	if err := p.backend.DeleteCustomProvider(selectedProvider); err != nil {
		return nil
	}
	if p.currentProvider == selectedProvider {
		p.currentProvider = "openai"
	}
	p.providers = nilProviderOptions(p.backend.ListProviders())
	if p.selected >= len(p.providers) {
		p.selected = len(p.providers) - 1
	}
	if p.selected < 0 {
		p.selected = 0
	}
	return nil
}

func (p *ModelPicker) handleEditProvider() core.Cmd {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.selected < 0 || p.selected >= len(p.providers) {
		return nil
	}
	selectedProvider := p.providers[p.selected].Type
	if !strings.HasPrefix(selectedProvider, "custom_") {
		return nil
	}
	target, ok := p.backend.GetCustomProvider(selectedProvider)
	if !ok {
		return nil
	}
	p.customName = target.Name
	p.customProtocol = target.Protocol
	p.customBaseURL = target.BaseURL
	p.customAPIKey = ""
	p.apiKeyEnv = target.APIKeyEnv
	p.step = modelPickerCustomName
	p.resetCaretToEndLocked()
	return nil
}

func (p *ModelPicker) handlePaste(text string) {
	if text == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.step {
	case modelPickerProviderSearch:
		p.providerSearch = insertStringAtCaret(p.providerSearch, &p.caret, text)
	case modelPickerModels, modelPickerCustomModels:
		p.search = insertStringAtCaret(p.search, &p.caret, text)
		p.modelSelected = 0
		p.modelOffset = 0
		p.applyModelFilterLocked()
	case modelPickerManual:
		p.manualInput = insertStringAtCaret(p.manualInput, &p.caret, text)
	case modelPickerCustomName:
		p.customName = insertStringAtCaret(p.customName, &p.caret, text)
	case modelPickerCustomBaseURL:
		p.customBaseURL = insertStringAtCaret(p.customBaseURL, &p.caret, text)
	case modelPickerCustomAPIKey:
		p.customAPIKey = insertStringAtCaret(p.customAPIKey, &p.caret, text)
	case modelPickerAPIKey:
		p.apiKeyInput = insertStringAtCaret(p.apiKeyInput, &p.caret, text)
	case modelPickerCustomContext:
		p.customContextInput = insertStringAtCaret(p.customContextInput, &p.caret, text)
	}
}

func (p *ModelPicker) needsContextPrompt(modelID string) bool {
	for _, cm := range p.customFetchedModels {
		if cm.ID == modelID {
			return cm.Context == 0
		}
	}
	return true
}

func (p *ModelPicker) updateCustomModelContext(modelID string, ctx int) {
	for i := range p.customFetchedModels {
		if p.customFetchedModels[i].ID == modelID {
			p.customFetchedModels[i].Context = ctx
			return
		}
	}
	p.customFetchedModels = append(p.customFetchedModels, CustomModel{
		ID:      modelID,
		Name:    modelID,
		Context: ctx,
	})
}

func (p *ModelPicker) finalizeCustomProvider(model string) core.Cmd {
	name := p.customName
	protocol := p.customProtocol
	baseURL := p.customBaseURL
	apiKeyEnv := p.apiKeyEnv

	found := false
	for _, cm := range p.customFetchedModels {
		if cm.ID == model {
			found = true
			break
		}
	}
	if !found && model != "" {
		p.customFetchedModels = append(p.customFetchedModels, CustomModel{
			ID:      model,
			Name:    model,
			Context: 0,
		})
	}

	cp := CustomProvider{
		Name:      name,
		Protocol:  protocol,
		BaseURL:   baseURL,
		APIKeyEnv: apiKeyEnv,
		Models:    copyCustomModels(p.customFetchedModels),
	}
	providerType, err := p.backend.UpsertCustomProvider(cp)
	if err != nil {
		p.fetchErr = err
		p.step = modelPickerManual
		p.manualInput = model
		return nil
	}
	if providerType != "" {
		p.provider = providerType
	}
	p.providers = nilProviderOptions(p.backend.ListProviders())

	if p.onApply != nil {
		go p.onApply(p.provider, model)
	}
	return nil
}

func (p *ModelPicker) Render(width int64) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	pal := theme.CurrentPalette()
	if width < 48 {
		width = 48
	}
	panelWidth := width - 8
	if panelWidth > 96 {
		panelWidth = 96
	}
	if panelWidth < 48 {
		panelWidth = width
	}
	innerWidth := panelWidth - 4
	var body []string
	body = append(body, pal.Accent.Render(i18n.T("model_picker.title")))
	switch p.step {
	case modelPickerProvider, modelPickerProviderSearch:
		filtered := p.filteredProviderList()
		total := len(filtered)

		if p.step == modelPickerProviderSearch {
			sbar := pal.Accent.Render("Search: ") +
				p.renderInput(p.providerSearch, pal.Accent) +
				pal.Accent.Render("  backspace edit  enter confirm  esc cancel")
			body = append(body, sbar)
		} else if p.providerSearch != "" {
			body = append(body, pal.Dim.Render(fmt.Sprintf("Filter: %s  / refine  esc clear  PgUp/PgDn", p.providerSearch)))
		} else {
			body = append(body, pal.Dim.Render("↑↓ move  Enter pick  / filter  PgUp/PgDn"))
		}
		body = append(body, pal.Dim.Render(fmt.Sprintf("Select provider — %d total (d=delete, e=edit for custom)", len(p.providers))))

		if total == 0 {
			body = append(body, "")
			body = append(body, pal.Dim.Render("  No matching providers."))
			break
		}

		start := p.providerOffset
		end := start + providerPageSize
		if end > total {
			end = total
		}
		if total > providerPageSize {
			m := min(p.providerOffset/providerPageSize+1, (total-1)/providerPageSize+1)
			mp := (total-1)/providerPageSize + 1
			body = append(body, pal.Dim.Render(fmt.Sprintf("  Page %d/%d", m, mp)))
		}
		body = append(body, "")

		for i := start; i < end; i++ {
			provider := filtered[i]
			radio := pal.Dim.Render("(○)")
			prefix := "  "
			display := pal.Dim.Render(provider.DisplayName)
			if provider.Configured {
				radio = pal.Success.Render("(●)")
				display = pal.Assistant.Render(provider.DisplayName)
			}
			if provider.Type == p.currentProvider {
				display += pal.Dim.Render(" ← current")
			}
			if i == p.selected {
				prefix = pal.SelectHighlight.Render("→ ")
				radio = pal.SelectHighlight.Render("(●)")
				display = pal.SelectHighlight.Render(provider.DisplayName)
			}
			body = append(body, prefix+radio+" "+display)
		}
	case modelPickerAPIKey:
		body = append(body, pal.Dim.Render(fmt.Sprintf("Provider: %s", p.provider)))
		body = append(body, "")
		body = append(body, fmt.Sprintf("Enter API key for %s (leave blank to keep current):", p.apiKeyEnv))
		body = append(body, p.renderField(maskAPIKey(p.apiKeyInput)))
	case modelPickerLoading, modelPickerCustomLoading:
		body = append(body, "")
		displayName := p.provider
		if dn := p.backend.ProviderDisplayName(p.provider); dn != "" {
			displayName = dn
		}
		body = append(body, fmt.Sprintf("Fetching %s models from API...", displayName))
	case modelPickerModels, modelPickerCustomModels:
		body = append(body, pal.Dim.Render(fmt.Sprintf("Provider: %s", p.provider)))
		body = append(body, pal.Dim.Render("Search: ")+p.renderInput(p.search, pal.Dim)+pal.Dim.Render(fmt.Sprintf(" (%d models)", len(p.filtered))))
		body = append(body, "")
		body = append(body, p.renderModelRows(innerWidth, pal)...)
	case modelPickerManual:
		body = append(body, pal.Dim.Render(fmt.Sprintf("Provider: %s", p.provider)))
		if p.fetchErr != nil {
			body = append(body, pal.Dim.Render("Model API unavailable: "+p.fetchErr.Error()))
		}
		body = append(body, "")
		body = append(body, "Enter model name, then Enter to confirm. Esc returns to providers.")
		body = append(body, p.renderField(p.manualInput))
	case modelPickerCustomName:
		body = append(body, pal.Dim.Render("Add Custom Provider"))
		body = append(body, "")
		body = append(body, "Enter provider name:")
		body = append(body, p.renderField(p.customName))
	case modelPickerCustomProtocol:
		body = append(body, pal.Dim.Render("Add Custom Provider - Protocol"))
		body = append(body, fmt.Sprintf("Name: %s", p.customName))
		body = append(body, "")
		body = append(body, "Select protocol type:")
		for i, proto := range p.customProtocols {
			label := proto
			if i == p.customProtocolSelected {
				body = append(body, "▸ "+pal.SelectHighlight.Render(label))
			} else {
				body = append(body, "  "+pal.Assistant.Render(label))
			}
		}
	case modelPickerCustomBaseURL:
		body = append(body, pal.Dim.Render("Add Custom Provider - Base URL"))
		body = append(body, fmt.Sprintf("Name: %s", p.customName))
		body = append(body, fmt.Sprintf("Protocol: %s", p.customProtocol))
		body = append(body, "")
		if p.customBaseURLErr != "" {
			body = append(body, pal.Error.Render(p.customBaseURLErr))
		}
		body = append(body, "Enter base URL:")
		body = append(body, p.renderField(p.customBaseURL))
	case modelPickerCustomAPIKey:
		body = append(body, pal.Dim.Render("Add Custom Provider - API Key"))
		body = append(body, fmt.Sprintf("Name: %s", p.customName))
		body = append(body, fmt.Sprintf("Protocol: %s", p.customProtocol))
		body = append(body, fmt.Sprintf("Base URL: %s", p.customBaseURL))
		body = append(body, "")
		label := "Enter API key (leave blank to keep current):"
		if p.apiKeyEnv != "" {
			label = fmt.Sprintf("Enter API key (will be saved as %s):", p.apiKeyEnv)
		}
		body = append(body, label)
		body = append(body, p.renderField(maskAPIKey(p.customAPIKey)))
	case modelPickerCustomContext:
		body = append(body, pal.Dim.Render("Add Custom Provider - Context Window"))
		body = append(body, fmt.Sprintf("Model: %s", p.selectedModelID))
		body = append(body, "")
		body = append(body, fmt.Sprintf("Enter context window size (in tokens) for %s:", p.selectedModelID))
		body = append(body, p.renderField(p.customContextInput))
	}
	panel := renderPickerPanel(body, panelWidth)
	return centerPanelLines(panel, width)
}

func (p *ModelPicker) renderModelRows(width int64, pal *theme.Palette) []string {
	if len(p.filtered) == 0 {
		return []string{pal.Dim.Render("  No matching models")}
	}
	start := p.modelOffset
	end := start + modelPickerPageSize
	if end > len(p.filtered) {
		end = len(p.filtered)
	}
	rows := []string{pal.Dim.Render(fmt.Sprintf("Showing %d-%d of %d (type to search, Enter confirm)", start+1, end, len(p.filtered)))}
	for i := start; i < end; i++ {
		label := p.filtered[i]
		if label == p.currentModel {
			label += "  ← current"
		}
		label = core.TruncateToWidth(label, width-4, "…")
		if i == p.modelSelected {
			rows = append(rows, "▸ "+pal.SelectHighlight.Render(label))
		} else {
			rows = append(rows, "  "+pal.Assistant.Render(label))
		}
	}
	return rows
}

func renderPickerPanel(body []string, width int64) []string {
	if width < 4 {
		width = 4
	}
	border := "+" + strings.Repeat("-", int(width-2)) + "+"
	lines := []string{border}
	for _, line := range body {
		lines = append(lines, pickerPanelLine(line, width))
	}
	lines = append(lines, border)
	return lines
}

func pickerPanelLine(content string, width int64) string {
	inner := width - 4
	content = core.TruncateToWidth(content, inner, "…")
	return "| " + core.PadToWidth(content, inner) + " |"
}

func centerPanelLines(panel []string, width int64) []string {
	leftPad := int64(0)
	if width > 0 && len(panel) > 0 {
		panelWidth := core.VisibleWidth(panel[0])
		if width > panelWidth {
			leftPad = (width - panelWidth) / 2
		}
	}
	prefix := strings.Repeat(" ", int(leftPad))
	lines := make([]string, 0, len(panel)+2)
	lines = append(lines, core.PadToWidth("", width))
	for _, line := range panel {
		lines = append(lines, core.PadToWidth(prefix+line, width))
	}
	lines = append(lines, core.PadToWidth("", width))
	return lines
}

func maskAPIKey(key string) string {
	if len(key) <= 4 {
		return strings.Repeat("*", len(key))
	}
	return strings.Repeat("*", len(key)-4) + key[len(key)-4:]
}

// renderInput returns text with the caret drawn as a background block over the
// character at the caret position, so the cursor never consumes an extra
// character cell. Only when the caret sits past the last character is a single
// trailing cell (▌ when visible, a space when hidden) appended, keeping the
// field width stable across cursor blinks. Non-cursor segments are rendered
// with base.
func (p *ModelPicker) renderInput(text string, base theme.Style) string {
	runes := []rune(text)
	pos := clampCaret(p.caret, len(runes))
	if pos >= len(runes) {
		marker := " "
		if p.cursorVisible {
			marker = "▌"
		}
		return base.Render(text) + marker
	}
	out := base.Render(string(runes[:pos]))
	ch := string(runes[pos])
	if p.cursorVisible {
		out += cursorBlockStyle(theme.CurrentPalette()).Render(ch)
	} else {
		out += base.Render(ch)
	}
	if pos+1 < len(runes) {
		out += base.Render(string(runes[pos+1:]))
	}
	return out
}

// renderField renders a highlighted input field line ("> "+text) with the caret
// drawn over the character under it (see renderInput).
func (p *ModelPicker) renderField(text string) string {
	pal := theme.CurrentPalette()
	return pal.SelectHighlight.Render("> ") + p.renderInput(text, pal.SelectHighlight)
}

// cursorBlockStyle returns a style that renders a character as a filled block:
// the character stays visible with the theme accent as its background.
func cursorBlockStyle(pal *theme.Palette) theme.Style {
	s := theme.NewStyle().Bold()
	if params := theme.FgParams(pal.Semantic.Accent, pal.Mode); params != "" {
		s = s.WithBgParams("48" + strings.TrimPrefix(params, "38"))
	}
	return s
}

// isCaretInputStep reports whether the step edits a text field with a caret.
func isCaretInputStep(step modelPickerStep) bool {
	return isTextInputStep(step) || step == modelPickerProviderSearch
}

// currentInputLocked returns the text of the active input field. Caller must
// hold p.mu (read or write).
func (p *ModelPicker) currentInputLocked() string {
	switch p.step {
	case modelPickerProviderSearch:
		return p.providerSearch
	case modelPickerModels, modelPickerCustomModels:
		return p.search
	case modelPickerManual:
		return p.manualInput
	case modelPickerCustomName:
		return p.customName
	case modelPickerCustomBaseURL:
		return p.customBaseURL
	case modelPickerCustomAPIKey:
		return p.customAPIKey
	case modelPickerAPIKey:
		return p.apiKeyInput
	case modelPickerCustomContext:
		return p.customContextInput
	}
	return ""
}

// setCurrentInputLocked sets the text of the active input field. Caller must
// hold p.mu for writing.
func (p *ModelPicker) setCurrentInputLocked(text string) {
	switch p.step {
	case modelPickerProviderSearch:
		p.providerSearch = text
	case modelPickerModels, modelPickerCustomModels:
		p.search = text
	case modelPickerManual:
		p.manualInput = text
	case modelPickerCustomName:
		p.customName = text
	case modelPickerCustomBaseURL:
		p.customBaseURL = text
	case modelPickerCustomAPIKey:
		p.customAPIKey = text
	case modelPickerAPIKey:
		p.apiKeyInput = text
	case modelPickerCustomContext:
		p.customContextInput = text
	}
}

// resetCaretToEndLocked moves the caret to the end of the active input field.
// Caller must hold p.mu for writing.
func (p *ModelPicker) resetCaretToEndLocked() {
	p.caret = len([]rune(p.currentInputLocked()))
}

func (p *ModelPicker) moveCaret(delta int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	max := len([]rune(p.currentInputLocked()))
	p.caret = clampCaret(p.caret+delta, max)
}

func (p *ModelPicker) setCaretBoundary(start bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if start {
		p.caret = 0
		return
	}
	p.caret = len([]rune(p.currentInputLocked()))
}

func clampCaret(caret, max int) int {
	if caret < 0 {
		return 0
	}
	if caret > max {
		return max
	}
	return caret
}

// insertRuneAtCaret inserts r into text at the caret position and advances the
// caret past the inserted rune.
func insertRuneAtCaret(text string, caret *int, r rune) string {
	runes := []rune(text)
	pos := clampCaret(*caret, len(runes))
	runes = append(runes, 0)
	copy(runes[pos+1:], runes[pos:])
	runes[pos] = r
	*caret = pos + 1
	return string(runes)
}

// deleteRuneBeforeCaret removes the rune immediately before the caret.
func deleteRuneBeforeCaret(text string, caret *int) string {
	runes := []rune(text)
	pos := clampCaret(*caret, len(runes))
	if pos == 0 {
		return text
	}
	runes = append(runes[:pos-1], runes[pos:]...)
	*caret = pos - 1
	return string(runes)
}

// insertStringAtCaret inserts s into text at the caret position and advances
// the caret past the inserted text.
func insertStringAtCaret(text string, caret *int, s string) string {
	insert := []rune(s)
	if len(insert) == 0 {
		return text
	}
	runes := []rune(text)
	pos := clampCaret(*caret, len(runes))
	runes = append(runes, make([]rune, len(insert))...)
	copy(runes[pos+len(insert):], runes[pos:])
	copy(runes[pos:], insert)
	*caret = pos + len(insert)
	return string(runes)
}

func wrapIndex(i, n int) int {
	if n <= 0 {
		return 0
	}
	if i < 0 {
		return n - 1
	}
	if i >= n {
		return 0
	}
	return i
}

func copyProviderModels(models []ProviderModel) []ProviderModel {
	if len(models) == 0 {
		return nil
	}
	out := make([]ProviderModel, len(models))
	copy(out, models)
	return out
}

func copyCustomModels(models []CustomModel) []CustomModel {
	if len(models) == 0 {
		return nil
	}
	out := make([]CustomModel, len(models))
	copy(out, models)
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func apiKeyEnvForProvider(name string) string {
	key := strings.ToUpper(name)
	key = strings.Join(strings.FieldsFunc(key, func(r rune) bool {
		return !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9')
	}), "_")
	key = strings.Trim(key, "_")
	if key == "" {
		key = "CUSTOM"
	}
	return "COVO_" + key + "_API_KEY"
}

func filterProviderModels(models []ProviderModel, query string, limit int, scoped ...string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	var matches []string
	for _, m := range models {
		if query != "" && !providerModelMatches(m, query) {
			continue
		}
		if len(scoped) > 0 {
			found := false
			for _, s := range scoped {
				if strings.EqualFold(m.ID, s) || strings.EqualFold(m.Name, s) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		matches = append(matches, m.ID)
		if limit > 0 && len(matches) >= limit {
			break
		}
	}
	return matches
}

func providerModelMatches(model ProviderModel, query string) bool {
	return strings.Contains(strings.ToLower(model.ID), query) ||
		strings.Contains(strings.ToLower(model.Name), query) ||
		strings.Contains(strings.ToLower(model.Description), query)
}

func adjustPickerOffset(selected, offset, pageSize, total int) int {
	if pageSize <= 0 || total <= pageSize {
		return 0
	}
	if selected < offset {
		return selected
	}
	if selected >= offset+pageSize {
		return selected - pageSize + 1
	}
	return offset
}

func appendUnique(values []string, extras ...string) []string {
	seen := make(map[string]bool, len(values)+len(extras))
	result := make([]string, 0, len(values)+len(extras))
	for _, v := range append(values, extras...) {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		result = append(result, v)
	}
	return result
}

func parseSize(s string) int {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0
	}
	multiplier := 1
	if strings.HasSuffix(s, "K") {
		multiplier = 1000
		s = strings.TrimSuffix(s, "K")
	} else if strings.HasSuffix(s, "M") {
		multiplier = 1000000
		s = strings.TrimSuffix(s, "M")
	} else if strings.HasSuffix(s, "B") {
		multiplier = 1000000000
		s = strings.TrimSuffix(s, "B")
	}
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || v <= 0 {
		return 0
	}
	return v * multiplier
}

var _ core.Component = (*ModelPicker)(nil)
var _ core.Updatable = (*ModelPicker)(nil)
