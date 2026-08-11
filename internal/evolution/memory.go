package evolution

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	entryDelimiter = "\n§\n"
	maxEntryChars  = 4000
)

type MemoryStore string

const (
	MemoryAgent MemoryStore = "agent"
	MemoryUser  MemoryStore = "user"
)

type MemoryEntry struct {
	Content string
	Index   int
}

// MemoryProvider defines the interface for pluggable memory backends.
type MemoryProvider interface {
	// Init initializes the provider (create directories, tables, etc.).
	Init() error

	// Read returns all entries for the given store.
	Read(store MemoryStore) ([]MemoryEntry, error)

	// Add appends a new entry to the given store.
	Add(store MemoryStore, content string) error

	// Replace finds oldSubstr and replaces it with newContent in the given store.
	Replace(store MemoryStore, oldSubstr, newContent string) error

	// Remove removes entries containing substr from the given store.
	Remove(store MemoryStore, substr string) error

	// Snapshot returns the full content of a store as a plain string.
	Snapshot(store MemoryStore) string

	// Name returns a unique identifier for this provider.
	Name() string

	// Ping checks whether the backend is reachable and healthy.
	Ping() error

	// Close shuts down the provider and releases resources.
	Close() error
}

// MemoryProviderConfig holds configuration for creating a MemoryProvider.
type MemoryProviderConfig struct {
	// HomeDir is the agent's home directory (typically ~/.covo).
	HomeDir string
	// Options provides provider-specific configuration key-value pairs.
	// Populated from COVO_MEMORY_OPTIONS env var (JSON) or programmatic config.
	Options map[string]string
}

// MemoryProviderFactory creates a MemoryProvider from config.
type MemoryProviderFactory func(cfg MemoryProviderConfig) (MemoryProvider, error)

var (
	memoryProviders   = map[string]MemoryProviderFactory{}
	memoryProvidersMu sync.RWMutex
)

// RegisterMemoryProvider registers a memory provider by name.
// Panics if a provider with the same name is already registered.
func RegisterMemoryProvider(name string, factory MemoryProviderFactory) {
	memoryProvidersMu.Lock()
	defer memoryProvidersMu.Unlock()
	if _, exists := memoryProviders[name]; exists {
		panic(fmt.Sprintf("memory provider %q already registered", name))
	}
	memoryProviders[name] = factory
}

// GetMemoryProvider returns the factory for the given name.
func GetMemoryProvider(name string) (MemoryProviderFactory, bool) {
	memoryProvidersMu.RLock()
	defer memoryProvidersMu.RUnlock()
	f, ok := memoryProviders[name]
	return f, ok
}

// MemoryProviderNames returns all registered provider names.
func MemoryProviderNames() []string {
	memoryProvidersMu.RLock()
	defer memoryProvidersMu.RUnlock()
	names := make([]string, 0, len(memoryProviders))
	for n := range memoryProviders {
		names = append(names, n)
	}
	return names
}

func newMemoryProviderFromEnv(homeDir string) MemoryProvider {
	providerName := os.Getenv("COVO_MEMORY_PROVIDER")
	if providerName == "" {
		providerName = "file"
	}

	options := parseMemoryOptions()

	if factory, ok := GetMemoryProvider(providerName); ok {
		p, err := factory(MemoryProviderConfig{HomeDir: homeDir, Options: options})
		if err == nil {
			slog.Debug("memory provider selected", "provider", providerName)
			return p
		}
		slog.Warn("memory provider factory failed, falling back to file", "provider", providerName, "error", err)
	}

	dir := filepath.Join(homeDir, "memories")
	return NewFileMemoryProvider(dir)
}

// parseMemoryOptions reads COVO_MEMORY_OPTIONS as a JSON object.
func parseMemoryOptions() map[string]string {
	raw := os.Getenv("COVO_MEMORY_OPTIONS")
	if raw == "" {
		return nil
	}
	var opts map[string]string
	if err := json.Unmarshal([]byte(raw), &opts); err != nil {
		slog.Warn("invalid COVO_MEMORY_OPTIONS JSON", "error", err)
		return nil
	}
	return opts
}

func init() {
	RegisterMemoryProvider("file", func(cfg MemoryProviderConfig) (MemoryProvider, error) {
		dir := filepath.Join(cfg.HomeDir, "memories")
		return NewFileMemoryProvider(dir), nil
	})
	RegisterMemoryProvider("sqlite", func(cfg MemoryProviderConfig) (MemoryProvider, error) {
		dir := filepath.Join(cfg.HomeDir, "memories")
		dbPath := cfg.Options["db_path"]
		if dbPath == "" {
			dbPath = filepath.Join(dir, "memory.db")
		}
		if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
			return nil, fmt.Errorf("create memories dir: %w", err)
		}
		return NewSQLiteMemoryProvider(dbPath), nil
	})
}

// MemorySystem wraps a MemoryProvider with prompt-building logic.
type MemorySystem struct {
	provider MemoryProvider
}

func NewMemorySystem(homeDir string) *MemorySystem {
	return &MemorySystem{
		provider: newMemoryProviderFromEnv(homeDir),
	}
}

// NewMemorySystemWithProvider creates a MemorySystem with a custom provider.
func NewMemorySystemWithProvider(provider MemoryProvider) *MemorySystem {
	return &MemorySystem{
		provider: provider,
	}
}

// Provider returns the underlying MemoryProvider.
func (m *MemorySystem) Provider() MemoryProvider {
	return m.provider
}

// SetProvider swaps the memory provider at runtime.
func (m *MemorySystem) SetProvider(p MemoryProvider) {
	if m.provider != nil && m.provider != p {
		m.provider.Close()
	}
	m.provider = p
}

func (m *MemorySystem) Ping() error {
	return m.provider.Ping()
}

func (m *MemorySystem) Init() error {
	return m.provider.Init()
}

func (m *MemorySystem) Read(store MemoryStore) ([]MemoryEntry, error) {
	return m.provider.Read(store)
}

func (m *MemorySystem) Add(store MemoryStore, content string) error {
	return m.provider.Add(store, content)
}

func (m *MemorySystem) Replace(store MemoryStore, oldSubstr, newContent string) error {
	return m.provider.Replace(store, oldSubstr, newContent)
}

func (m *MemorySystem) Remove(store MemoryStore, substr string) error {
	return m.provider.Remove(store, substr)
}

func (m *MemorySystem) Snapshot(store MemoryStore) string {
	return m.provider.Snapshot(store)
}

func (m *MemorySystem) BuildPromptSuffix() string {
	var b strings.Builder

	agentSnapshot := m.Snapshot(MemoryAgent)
	if agentSnapshot != "" {
		b.WriteString("\n<agent_memory>\n")
		b.WriteString("The following is your persistent memory, accumulated across sessions. ")
		b.WriteString("It captures environment facts, project conventions, tool quirks, and things you have learned.\n\n")
		b.WriteString(agentSnapshot)
		b.WriteString("</agent_memory>\n")
	}

	userSnapshot := m.Snapshot(MemoryUser)
	if userSnapshot != "" {
		b.WriteString("\n<user_profile>\n")
		b.WriteString("The following is what you know about the user — their preferences, communication style, and workflow habits.\n\n")
		b.WriteString(userSnapshot)
		b.WriteString("</user_profile>\n")
	}

	return b.String()
}

// FileMemoryProvider is the default file-based memory backend.
type FileMemoryProvider struct {
	mu    sync.RWMutex
	dir   string
	agent string
	user  string
}

func NewFileMemoryProvider(dir string) *FileMemoryProvider {
	return &FileMemoryProvider{
		dir:   dir,
		agent: filepath.Join(dir, "MEMORY.md"),
		user:  filepath.Join(dir, "USER.md"),
	}
}

func (f *FileMemoryProvider) Name() string {
	return "file"
}

func (f *FileMemoryProvider) Init() error {
	if err := os.MkdirAll(f.dir, 0755); err != nil {
		return fmt.Errorf("create memories dir: %w", err)
	}
	for _, p := range []string{f.agent, f.user} {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			if err := os.WriteFile(p, nil, 0644); err != nil {
				return fmt.Errorf("create memory file %s: %w", p, err)
			}
		}
	}
	return nil
}

func (f *FileMemoryProvider) filePath(store MemoryStore) string {
	switch store {
	case MemoryUser:
		return f.user
	default:
		return f.agent
	}
}

func (f *FileMemoryProvider) Read(store MemoryStore) ([]MemoryEntry, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	data, err := os.ReadFile(f.filePath(store))
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil, nil
	}
	parts := strings.Split(content, entryDelimiter)
	var entries []MemoryEntry
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		entries = append(entries, MemoryEntry{Content: p, Index: i})
	}
	return entries, nil
}

func (f *FileMemoryProvider) Add(store MemoryStore, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	if len(content) > maxEntryChars {
		content = content[:maxEntryChars]
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	fp := f.filePath(store)
	existing, _ := os.ReadFile(fp)
	existingStr := strings.TrimSpace(string(existing))

	var newContent string
	if existingStr == "" {
		newContent = content
	} else {
		newContent = existingStr + entryDelimiter + content
	}
	return os.WriteFile(fp, []byte(newContent+"\n"), 0644)
}

func (f *FileMemoryProvider) Replace(store MemoryStore, oldSubstr, newContent string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	fp := f.filePath(store)
	data, err := os.ReadFile(fp)
	if err != nil {
		return err
	}
	content := string(data)
	if !strings.Contains(content, oldSubstr) {
		return fmt.Errorf("substring not found in memory")
	}
	replaced := strings.Replace(content, oldSubstr, strings.TrimSpace(newContent), 1)
	return os.WriteFile(fp, []byte(replaced), 0644)
}

func (f *FileMemoryProvider) Remove(store MemoryStore, substr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	fp := f.filePath(store)
	data, err := os.ReadFile(fp)
	if err != nil {
		return err
	}
	content := string(data)
	if !strings.Contains(content, substr) {
		return fmt.Errorf("substring not found in memory")
	}

	parts := strings.Split(content, entryDelimiter)
	var kept []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || strings.Contains(p, substr) {
			continue
		}
		kept = append(kept, p)
	}
	return os.WriteFile(fp, []byte(strings.Join(kept, entryDelimiter)+"\n"), 0644)
}

func (f *FileMemoryProvider) Snapshot(store MemoryStore) string {
	entries, err := f.Read(store)
	if err != nil || len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.Content)
		b.WriteString("\n")
	}
	return b.String()
}

func (f *FileMemoryProvider) Ping() error {
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, p := range []string{f.agent, f.user} {
		if _, err := os.Stat(p); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("memory file %s not found (run Init first)", p)
			}
			return fmt.Errorf("stat %s: %w", p, err)
		}
	}
	return nil
}

func (f *FileMemoryProvider) Close() error {
	return nil
}

// MigrateMemoryProvider migrates all entries from src to dst.
// Both providers must be initialized before calling.
// The destination must be empty or have --clear semantics applied beforehand;
// entries are appended to whatever already exists.
func MigrateMemoryProvider(src, dst MemoryProvider) error {
	for _, store := range []MemoryStore{MemoryAgent, MemoryUser} {
		entries, err := src.Read(store)
		if err != nil {
			return fmt.Errorf("read %s from source: %w", store, err)
		}
		for _, e := range entries {
			if err := dst.Add(store, e.Content); err != nil {
				return fmt.Errorf("add to %s in destination: %w", store, err)
			}
		}
	}
	return nil
}
