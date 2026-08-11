package gateway

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

type MirrorEntry struct {
	Role         string `json:"role"`
	Content      string `json:"content"`
	Timestamp    string `json:"timestamp"`
	Mirror       bool   `json:"mirror"`
	MirrorSource string `json:"mirror_source"`
}

type MirrorConfig struct {
	SessionsDir string
}

func NewMirror(cfg MirrorConfig) *Mirror {
	if cfg.SessionsDir == "" {
		cfg.SessionsDir = defaultSessionsDir()
	}
	return &Mirror{cfg: cfg}
}

type Mirror struct {
	cfg MirrorConfig
}

func (m *Mirror) MirrorToSession(platform, chatID, messageText, sourceLabel string) bool {
	sessionID, err := m.findSessionID(platform, chatID)
	if err != nil || sessionID == "" {
		slog.Debug("mirror: no session found", "platform", platform, "chat_id", chatID)
		return false
	}

	entry := MirrorEntry{
		Role:         "assistant",
		Content:      messageText,
		Timestamp:    time.Now().Format(time.RFC3339),
		Mirror:       true,
		MirrorSource: sourceLabel,
	}

	if err := m.appendToTranscript(sessionID, entry); err != nil {
		slog.Debug("mirror: append failed", "session_id", sessionID, "error", err)
		return false
	}

	slog.Debug("mirror: wrote to session", "session_id", sessionID, "source", sourceLabel)
	return true
}

func (m *Mirror) findSessionID(platform, chatID string) (string, error) {
	indexPath := filepath.Join(m.cfg.SessionsDir, "sessions.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return "", err
	}

	var sessions []struct {
		ID     string `json:"id"`
		Origin struct {
			Platform string `json:"platform"`
			ChatID   string `json:"chat_id"`
		} `json:"origin"`
	}
	if err := json.Unmarshal(data, &sessions); err != nil {
		return "", err
	}

	for _, s := range sessions {
		if s.Origin.Platform == platform && s.Origin.ChatID == chatID {
			return s.ID, nil
		}
	}

	return "", nil
}

func (m *Mirror) appendToTranscript(sessionID string, entry MirrorEntry) error {
	sessionPath := filepath.Join(m.cfg.SessionsDir, sessionID+".jsonl")
	f, err := os.OpenFile(sessionPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open session transcript: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal mirror entry: %w", err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write mirror entry: %w", err)
	}

	return nil
}

func defaultSessionsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".covo-agent", "sessions")
}
