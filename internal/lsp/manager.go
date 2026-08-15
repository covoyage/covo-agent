package lsp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/logutil"
)

const defaultIdleTimeout = 10 * time.Minute

type Manager struct {
	clients       map[ClientID]*Client
	brokenSet     map[ClientID]bool
	mu            sync.Mutex
	logger        *slog.Logger
	active        bool
	idleTimeout   time.Duration
	lastActivity  map[ClientID]time.Time
	deltaBaseline map[string]map[string][]Diagnostic
	baselineMu    sync.RWMutex
}

type ManagerConfig struct {
	Enabled     bool
	IdleTimeout time.Duration
}

func NewManager(cfg ManagerConfig) *Manager {
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = defaultIdleTimeout
	}
	active := cfg.Enabled
	if active {
		homeDir, _ := os.UserHomeDir()
		if homeDir == "" {
			active = false
		}
	}
	return &Manager{
		clients:   make(map[ClientID]*Client),
		brokenSet: make(map[ClientID]bool),
		logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: logutil.ResolveLevel(slog.LevelInfo),
		})),
		active:        active,
		idleTimeout:   cfg.IdleTimeout,
		lastActivity:  make(map[ClientID]time.Time),
		deltaBaseline: make(map[string]map[string][]Diagnostic),
	}
}

func (m *Manager) IsActive() bool {
	return m.active
}

func (m *Manager) Enable() {
	m.active = true
}

func (m *Manager) Disable() {
	m.active = false
	m.shutdownAll()
}

func (m *Manager) GetDiagnosticsForFile(filePath string, timeout time.Duration) ([]Diagnostic, error) {
	if !m.active {
		return nil, nil
	}

	serverDef := FindServerForFile(filePath)
	if serverDef == nil {
		return nil, nil
	}

	workspaceRoot := ResolveWorkspaceForFile(filePath, serverDef.RootPatterns)
	if workspaceRoot == "" {
		return nil, nil
	}

	clientID := ClientID{ServerID: serverDef.ID, WorkspaceRoot: workspaceRoot}

	m.mu.Lock()
	if m.brokenSet[clientID] {
		m.mu.Unlock()
		return nil, nil
	}
	client := (*Client)(nil)
	for id, c := range m.clients {
		if id == clientID {
			client = c
			break
		}
	}
	m.mu.Unlock()

	if client == nil {
		var err error
		client, err = m.spawnClient(serverDef, workspaceRoot)
		if err != nil {
			m.mu.Lock()
			m.brokenSet[clientID] = true
			m.mu.Unlock()
			m.logger.Warn("failed to spawn lsp client", "server", serverDef.ID, "error", err)
			return nil, nil
		}
	}

	if err := client.OpenFile(filePath); err != nil {
		m.logger.Warn("failed to open file in lsp", "file", filePath, "error", err)
		return nil, nil
	}

	if err := client.ChangeFile(filePath); err != nil {
		m.logger.Warn("failed to notify change in lsp", "file", filePath, "error", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	diags := client.WaitForDiagnostics(ctx, filePath, timeout)

	m.mu.Lock()
	m.lastActivity[clientID] = time.Now()
	m.mu.Unlock()

	return diags, nil
}

func (m *Manager) GetNewDiagnostics(filePath string, timeout time.Duration) ([]Diagnostic, error) {
	diags, err := m.GetDiagnosticsForFile(filePath, timeout)
	if err != nil {
		return nil, err
	}

	m.baselineMu.RLock()
	baseline := m.deltaBaseline[filePath]
	m.baselineMu.RUnlock()

	return filterByBaseline(diags, baseline), nil
}

// filterByBaseline returns diagnostics that are not present in the baseline.
// The baseline is a map keyed by "line:char:message" for O(1) lookup.
// If baseline is nil, all diagnostics are returned (no filtering) — this is
// the first-edit behavior where no prior state was captured.
func filterByBaseline(diags []Diagnostic, baseline map[string][]Diagnostic) []Diagnostic {
	if baseline == nil {
		return diags
	}
	var newDiags []Diagnostic
	for _, d := range diags {
		key := fmt.Sprintf("%d:%d:%s", d.Range.Start.Line, d.Range.Start.Character, d.Message)
		if _, exists := baseline[key]; !exists {
			newDiags = append(newDiags, d)
		}
	}
	return newDiags
}

func (m *Manager) SnapshotBaseline(filePath string) {
	serverDef := FindServerForFile(filePath)
	if serverDef == nil {
		return
	}

	workspaceRoot := ResolveWorkspaceForFile(filePath, serverDef.RootPatterns)
	if workspaceRoot == "" {
		return
	}

	clientID := ClientID{ServerID: serverDef.ID, WorkspaceRoot: workspaceRoot}

	m.mu.Lock()
	client := (*Client)(nil)
	for id, c := range m.clients {
		if id == clientID {
			client = c
			break
		}
	}
	m.mu.Unlock()

	if client == nil {
		return
	}

	diags := client.DiagnosticsFor(filePath)
	baseline := make(map[string][]Diagnostic)
	key := ""
	for _, d := range diags {
		key = fmt.Sprintf("%d:%d:%s", d.Range.Start.Line, d.Range.Start.Character, d.Message)
		baseline[key] = append(baseline[key], d)
	}

	m.baselineMu.Lock()
	m.deltaBaseline[filePath] = baseline
	m.baselineMu.Unlock()
}

func (m *Manager) ReportForFile(filePath string, severities []int) string {
	diags, err := m.GetDiagnosticsForFile(filePath, diagnosticsDocumentWait)
	if err != nil {
		return ""
	}
	return ReportForFile(filePath, diags, severities)
}

// clientForFile returns a started LSP client for filePath, spawning one if
// needed and opening the file. Returns nil when LSP is inactive, no server
// matches, or the client could not be started.
func (m *Manager) clientForFile(filePath string) *Client {
	if !m.active {
		return nil
	}
	serverDef := FindServerForFile(filePath)
	if serverDef == nil {
		return nil
	}
	workspaceRoot := ResolveWorkspaceForFile(filePath, serverDef.RootPatterns)
	if workspaceRoot == "" {
		return nil
	}
	clientID := ClientID{ServerID: serverDef.ID, WorkspaceRoot: workspaceRoot}

	m.mu.Lock()
	if m.brokenSet[clientID] {
		m.mu.Unlock()
		return nil
	}
	var client *Client
	for id, c := range m.clients {
		if id == clientID {
			client = c
			break
		}
	}
	m.mu.Unlock()

	if client == nil {
		var err error
		client, err = m.spawnClient(serverDef, workspaceRoot)
		if err != nil {
			m.mu.Lock()
			m.brokenSet[clientID] = true
			m.mu.Unlock()
			m.logger.Warn("failed to spawn lsp client", "server", serverDef.ID, "error", err)
			return nil
		}
	}

	if err := client.OpenFile(filePath); err != nil {
		m.logger.Warn("failed to open file in lsp", "file", filePath, "error", err)
		return nil
	}

	m.mu.Lock()
	m.lastActivity[clientID] = time.Now()
	m.mu.Unlock()
	return client
}

// Definition resolves the definition location(s) for the symbol at the given
// 0-based line/character. Returns nil when LSP is unavailable for the file.
func (m *Manager) Definition(filePath string, line, char int) ([]Location, error) {
	client := m.clientForFile(filePath)
	if client == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), navTimeout)
	defer cancel()
	return client.Definition(ctx, filePath, line, char)
}

// References finds all references (including the declaration) to the symbol at
// the given 0-based position.
func (m *Manager) References(filePath string, line, char int) ([]Location, error) {
	client := m.clientForFile(filePath)
	if client == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), navTimeout)
	defer cancel()
	return client.References(ctx, filePath, line, char, true)
}

// Hover returns hover documentation for the symbol at the given 0-based position.
func (m *Manager) Hover(filePath string, line, char int) (string, error) {
	client := m.clientForFile(filePath)
	if client == nil {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), navTimeout)
	defer cancel()
	return client.Hover(ctx, filePath, line, char)
}

func (m *Manager) spawnClient(serverDef *ServerDef, workspaceRoot string) (*Client, error) {
	clientID := ClientID{ServerID: serverDef.ID, WorkspaceRoot: workspaceRoot}
	client := NewClient(serverDef, workspaceRoot)

	ctx, cancel := context.WithTimeout(context.Background(), initializeTimeout)
	defer cancel()

	if err := client.Start(ctx); err != nil {
		return nil, fmt.Errorf("start lsp client: %w", err)
	}

	m.mu.Lock()
	m.clients[clientID] = client
	m.mu.Unlock()

	return client, nil
}

func (m *Manager) shutdownAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, client := range m.clients {
		client.Shutdown()
		delete(m.clients, id)
	}
}

func (m *Manager) ReapIdle() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for id, client := range m.clients {
		last, ok := m.lastActivity[id]
		if !ok {
			last = time.Now()
		}
		if now.Sub(last) > m.idleTimeout {
			client.Shutdown()
			delete(m.clients, id)
			delete(m.lastActivity, id)
		}
	}
}

// CollectAllDiagnostics gathers diagnostics from all active LSP clients and
// returns a formatted report of errors and warnings. Only files under the
// given cwd are included. The report is capped at maxTotalChars characters.
func (m *Manager) CollectAllDiagnostics(cwd string) string {
	if !m.active {
		return ""
	}
	cwdAbs, _ := filepath.Abs(cwd)

	m.mu.Lock()
	clients := make([]*Client, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	m.mu.Unlock()

	type fileDiags struct {
		path string
		diag []Diagnostic
	}
	var all []fileDiags
	for _, c := range clients {
		for path, diags := range c.AllDiagnostics() {
			if len(diags) == 0 {
				continue
			}
			// Only include files under the working directory.
			if cwdAbs != "" {
				rel, err := filepath.Rel(cwdAbs, path)
				if err != nil || strings.HasPrefix(rel, "..") {
					continue
				}
			}
			// Filter to errors and warnings only (severity 1 and 2).
			var filtered []Diagnostic
			for _, d := range diags {
				sev := d.Severity
				if sev == 0 {
					sev = 1
				}
				if sev <= 2 {
					filtered = append(filtered, d)
				}
			}
			if len(filtered) > 0 {
				all = append(all, fileDiags{path: path, diag: filtered})
			}
		}
	}

	if len(all) == 0 {
		return ""
	}

	// Sort by file path for stable output.
	sort.Slice(all, func(i, j int) bool {
		return all[i].path < all[j].path
	})

	var b strings.Builder
	totalCount := 0
	for _, fd := range all {
		rel := fd.path
		if cwdAbs != "" {
			if r, err := filepath.Rel(cwdAbs, fd.path); err == nil {
				rel = r
			}
		}
		report := ReportForFile(rel, fd.diag, []int{1, 2})
		if report == "" {
			continue
		}
		b.WriteString(report)
		b.WriteString("\n")
		totalCount += len(fd.diag)
	}
	if totalCount == 0 {
		return ""
	}
	result := fmt.Sprintf("Total: %d problems across %d files\n\n%s", totalCount, len(all), b.String())
	return Truncate(result, 8000)
}

func (m *Manager) Shutdown() {
	m.shutdownAll()
	ClearWorkspaceCache()
}
