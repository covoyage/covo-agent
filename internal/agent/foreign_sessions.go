package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// ForeignSessionType identifies the source agent tool.
type ForeignSessionType string

const (
	ForeignClaude ForeignSessionType = "claude"
	ForeignCodex  ForeignSessionType = "codex"
)

// ForeignSessionInfo describes a discovered foreign session file.
type ForeignSessionInfo struct {
	Type         ForeignSessionType `json:"type"`
	Path         string             `json:"path"`
	Name         string             `json:"name"`
	Modified     time.Time          `json:"modified"`
	Size         int64              `json:"size"`
	Preview      string             `json:"preview"`
	MessageCount int                `json:"message_count"`
}

// DiscoverForeignSessions scans supported external session directories and
// returns a list of discoverable sessions.
//
// Each supported format has its own conventional JSONL directory.
func DiscoverForeignSessions(homeDir string) ([]ForeignSessionInfo, error) {
	var sessions []ForeignSessionInfo

	// Discover the nested external session format.
	userHome, _ := os.UserHomeDir()
	claudeDir := filepath.Join(userHome, ".claude", "projects")
	if claudeSessions, err := discoverInDir(claudeDir, ForeignClaude); err == nil {
		sessions = append(sessions, claudeSessions...)
	}

	// Discover the second supported external session format.
	codexDir := filepath.Join(userHome, ".codex", "sessions")
	if codexSessions, err := discoverInDir(codexDir, ForeignCodex); err == nil {
		sessions = append(sessions, codexSessions...)
	}

	return sessions, nil
}

// discoverInDir walks a directory tree looking for .jsonl files.
func discoverInDir(dir string, sessionType ForeignSessionType) ([]ForeignSessionInfo, error) {
	var sessions []ForeignSessionInfo

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err // directory doesn't exist or not readable
	}

	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())

		if entry.IsDir() {
			// Recurse into subdirectories used to group sessions by project path.
			sub, err := discoverInDir(path, sessionType)
			if err == nil {
				sessions = append(sessions, sub...)
			}
			continue
		}

		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		preview, msgCount := peekSessionFile(path, sessionType)
		if msgCount == 0 {
			continue // skip empty files
		}

		name := strings.TrimSuffix(entry.Name(), ".jsonl")
		// Include the parent directory for nested session context.
		if sessionType == ForeignClaude {
			parentDir := filepath.Base(filepath.Dir(path))
			if parentDir != "projects" {
				name = parentDir + "/" + name
			}
		}

		sessions = append(sessions, ForeignSessionInfo{
			Type:         sessionType,
			Path:         path,
			Name:         name,
			Modified:     info.ModTime(),
			Size:         info.Size(),
			Preview:      preview,
			MessageCount: msgCount,
		})
	}

	return sessions, nil
}

// peekSessionFile reads the first few lines to get a preview and message count.
func peekSessionFile(path string, sessionType ForeignSessionType) (string, int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0
	}

	lines := strings.Split(string(data), "\n")
	var preview string
	count := 0

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		count++

		// Get preview from first user message
		if preview == "" {
			preview = extractPreviewFromLine(line, sessionType)
		}

		// Only scan first 100 lines for count estimate
		if i >= 100 {
			break
		}
	}

	return preview, count
}

func extractPreviewFromLine(line string, sessionType ForeignSessionType) string {
	switch sessionType {
	case ForeignClaude:
		// Nested envelope format: {"type":"user","message":{"role":"user","content":"..."}}
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return ""
		}
		if entry.Type == "user" || entry.Message.Role == "user" {
			return truncateStr(fmt.Sprintf("%v", entry.Message.Content), 100)
		}
	case ForeignCodex:
		// Flat format: {"role":"user","content":"..."}
		var entry struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return ""
		}
		if entry.Role == "user" {
			return truncateStr(entry.Content, 100)
		}
	}
	return ""
}

// ConvertForeignSession reads a foreign session file and converts it to
// covo-agent message format. Returns a slice of messages suitable for
// importing via session manager.
func ConvertForeignSession(path string, sessionType ForeignSessionType) ([]agentcore.Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read foreign session: %w", err)
	}

	var messages []agentcore.Message

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		msg, ok := convertLine(line, sessionType)
		if !ok {
			continue
		}
		messages = append(messages, msg)
	}

	if len(messages) == 0 {
		return nil, fmt.Errorf("no valid messages found in %s session", sessionType)
	}

	return messages, nil
}

func convertLine(line string, sessionType ForeignSessionType) (agentcore.Message, bool) {
	switch sessionType {
	case ForeignClaude:
		return convertClaudeLine(line)
	case ForeignCodex:
		return convertCodexLine(line)
	default:
		return agentcore.Message{}, false
	}
}

// This converter maps a nested external JSONL line to an agentcore.Message.
// Format: {"type":"user"|"assistant", "message":{"role":"...", "content":"..."}}
// Content can be a string or an array of content blocks.
func convertClaudeLine(line string) (agentcore.Message, bool) {
	var entry struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return agentcore.Message{}, false
	}

	// Skip non-conversational entries (summaries, tool results, etc.)
	if entry.Type != "user" && entry.Type != "assistant" {
		return agentcore.Message{}, false
	}

	role := agentcore.RoleUser
	if entry.Message.Role == "assistant" || entry.Type == "assistant" {
		role = agentcore.RoleAssistant
	}

	content := extractContentString(entry.Message.Content)
	if content == "" {
		return agentcore.Message{}, false
	}

	return agentcore.Message{
		Role:    role,
		Content: content,
	}, true
}

// This converter maps a flat external JSONL line to an agentcore.Message.
// Format: {"role":"user"|"assistant", "content":"..."}
func convertCodexLine(line string) (agentcore.Message, bool) {
	var entry struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return agentcore.Message{}, false
	}

	if entry.Content == "" {
		return agentcore.Message{}, false
	}

	role := agentcore.RoleUser
	if entry.Role == "assistant" {
		role = agentcore.RoleAssistant
	}

	return agentcore.Message{
		Role:    role,
		Content: entry.Content,
	}, true
}

// extractContentString handles a content field that can be a string or an
// array of content blocks (e.g., [{"type":"text","text":"..."}]).
func extractContentString(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, block := range v {
			if m, ok := block.(map[string]any); ok {
				if t, ok := m["type"].(string); ok && t == "text" {
					if text, ok := m["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprintf("%v", v)
	}
}

// DetectForeignType attempts to detect the foreign session type from a file path.
func DetectForeignType(path string) (ForeignSessionType, bool) {
	lower := strings.ToLower(path)
	if strings.Contains(lower, ".claude") {
		return ForeignClaude, true
	}
	if strings.Contains(lower, ".codex") {
		return ForeignCodex, true
	}
	return "", false
}

func truncateStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
