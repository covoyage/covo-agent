package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	covosession "github.com/covoyage/covonaut/session"

	"github.com/covoyage/covo-agent/internal/session/sqlite"
)

type Manager struct {
	store       *sqlite.Store
	sessionsDir string
	currentID   string
}

func NewManager(homeDir string) (*Manager, error) {
	sessionsDir := filepath.Join(homeDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		return nil, fmt.Errorf("create sessions dir: %w", err)
	}

	store, err := sqlite.NewStore(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("create sqlite store: %w", err)
	}

	return &Manager{
		store:       store,
		sessionsDir: sessionsDir,
	}, nil
}

// Store returns the SQLite store directly, implementing agentcore.Store.
// No AgentStore wrapper needed — the sqlite.Store implements Store directly.
func (m *Manager) Store() agentcore.Store {
	return m.store
}

func (m *Manager) DB() *sqlite.Store {
	return m.store
}

func (m *Manager) CurrentID() string {
	return m.currentID
}

func (m *Manager) NewSession(ctx context.Context, cwd string) (string, error) {
	sessionID := generateSessionID()
	// Defer persistence: session row is created lazily on first AppendMessage/Save
	m.currentID = sessionID
	return sessionID, nil
}

// EnsureCurrentSession creates a session if none is active. Used at startup.
func (m *Manager) EnsureCurrentSession(ctx context.Context, cwd string) {
	if m.currentID != "" {
		return
	}
	id, _ := m.NewSession(ctx, cwd)
	_ = id
}

// SetCurrentSessionID sets the current session ID without creating a new session.
// Used by --session-id flag to resume or start with a specific ID.
func (m *Manager) SetCurrentSessionID(id string) {
	m.currentID = id
}

func (m *Manager) ResumeSession(ctx context.Context, sessionID string) error {
	fullID, err := m.store.ResolveID(ctx, sessionID)
	if err != nil {
		return err
	}
	m.currentID = fullID
	return nil
}

// ForkSession creates a new session as a branch of the current one,
// copying all messages from the parent session and recording the parent_id.
func (m *Manager) ForkSession(ctx context.Context) (string, error) {
	if m.currentID == "" {
		return "", fmt.Errorf("no active session")
	}
	newID := generateSessionID()
	parentID := m.currentID

	// Ensure parent session row exists before copying
	_ = m.store.EnsureSession(ctx, parentID, "")

	// Create child session row with parent_id
	err := m.store.EnsureSessionWithParent(ctx, newID, parentID)
	if err != nil {
		return "", fmt.Errorf("fork: ensure child: %w", err)
	}

	// Copy all messages from parent to child
	if err := m.store.CopyMessages(ctx, parentID, newID); err != nil {
		return "", fmt.Errorf("fork: copy messages: %w", err)
	}

	m.currentID = newID
	return newID, nil
}

// LoadSession loads the full state snapshot from SQLite for session restoration.
func (m *Manager) LoadSession(ctx context.Context, sessionID string) (agentcore.StateSnapshot, error) {
	return m.store.Load(ctx, sessionID)
}

// SearchSessions performs FTS5 full-text search across all sessions.
func (m *Manager) SearchSessions(ctx context.Context, query string, limit int) ([]sqlite.SearchResult, error) {
	return m.store.SearchSessions(ctx, query, limit)
}

// ExportSession returns a formatted text export of a session.
func (m *Manager) ExportSession(ctx context.Context, sessionID string) (string, error) {
	return m.store.ExportSession(ctx, sessionID)
}

// ExportSessionMarkdown returns a Markdown export of a session.
func (m *Manager) ExportSessionMarkdown(ctx context.Context, sessionID string) (string, error) {
	return m.store.ExportSessionMarkdown(ctx, sessionID)
}

// ExportSessionHTML returns an HTML export of a session.
func (m *Manager) ExportSessionHTML(ctx context.Context, sessionID string) (string, error) {
	return m.store.ExportSessionHTML(ctx, sessionID)
}

func (m *Manager) ListSessions(ctx context.Context) ([]covosession.Info, error) {
	infos, err := m.store.ListSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	var result []covosession.Info
	for _, info := range infos {
		result = append(result, covosession.Info{
			ID:            info.ID,
			Name:          info.Title,
			Label:         info.Label,
			Summary:       info.Summary,
			ParentSession: info.ParentID,
			Cwd:           info.Cwd,
			CreatedAt:     info.CreatedAt,
			UpdatedAt:     info.UpdatedAt,
			MessageCount:  int64(info.MsgCount),
		})
	}
	return result, nil
}

func (m *Manager) DeleteSession(ctx context.Context, sessionID string) error {
	if m.currentID == sessionID {
		m.currentID = ""
	}
	return m.store.DeleteSession(ctx, sessionID)
}

func (m *Manager) RenameSession(ctx context.Context, sessionID, name string) error {
	return m.store.SetTitle(ctx, sessionID, name)
}

func (m *Manager) SetLabel(ctx context.Context, sessionID, label string) error {
	return m.store.SetLabel(ctx, sessionID, label)
}

func (m *Manager) SetSummary(ctx context.Context, sessionID, summary string) error {
	return m.store.SetSummary(ctx, sessionID, summary)
}

func (m *Manager) ParentID() string {
	if m.currentID == "" {
		return ""
	}
	ctx := context.Background()
	infos, err := m.store.ListSessions(ctx)
	if err != nil {
		return ""
	}
	for _, info := range infos {
		if info.ID == m.currentID {
			return info.ParentID
		}
	}
	return ""
}

func (m *Manager) PruneSessions(ctx context.Context, olderThanDays int) (int, error) {
	infos, err := m.ListSessions(ctx)
	if err != nil {
		return 0, err
	}
	if olderThanDays <= 0 {
		// Delete all sessions
		for _, info := range infos {
			if err := m.DeleteSession(ctx, info.ID); err != nil {
				return 0, fmt.Errorf("delete %s: %w", info.ID[:8], err)
			}
		}
		return len(infos), nil
	}
	cutoff := time.Now().Add(-time.Duration(olderThanDays) * 24 * time.Hour)
	var deleted int
	for _, info := range infos {
		if info.UpdatedAt.Before(cutoff) {
			if err := m.DeleteSession(ctx, info.ID); err != nil {
				return deleted, fmt.Errorf("delete %s: %w", info.ID[:8], err)
			}
			deleted++
		}
	}
	return deleted, nil
}

func (m *Manager) FormatSessionInfo(info covosession.Info) string {
	updated := info.UpdatedAt.Format("01-02 15:04")
	name := info.Name
	if name == "" {
		// No auto-title yet — show a friendly placeholder instead of raw ID
		name = fmt.Sprintf("会话 %s…", info.UpdatedAt.Format("1月2日"))
	}
	label := info.Label
	if label != "" {
		label = " [" + label + "]"
	}
	fork := ""
	if info.ParentSession != "" {
		fork = fmt.Sprintf(" ↤%s", info.ParentSession[:8])
	}
	marker := ""
	if info.ID == m.currentID {
		marker = " *"
	}
	return fmt.Sprintf("  %-10s%s%s %s  %3d msgs  %s%s",
		info.ID[:8], marker, fork, updated, info.MessageCount, name, label)
}

func (m *Manager) Close() {
	if m.currentID != "" && m.store != nil {
		// If the current session has no messages, clean it up
		if count, err := m.store.MessageCount(context.Background(), m.currentID); err == nil && count == 0 {
			_ = m.store.DeleteSession(context.Background(), m.currentID)
		}
	}
	m.currentID = ""
	if m.store != nil {
		m.store.Close()
	}
}

// generateSessionID creates a unique session identifier.
func generateSessionID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())
}
