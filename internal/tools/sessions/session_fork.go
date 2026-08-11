package sessions

import (
	"fmt"
	"strings"
	"sync"
)

type forkMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type SessionFork struct {
	mu       sync.Mutex
	sessions map[string][]forkMessage
}

func NewSessionFork() *SessionFork {
	return &SessionFork{sessions: make(map[string][]forkMessage)}
}

func (sf *SessionFork) RecordMessage(sessionID, role, content string) {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	sf.sessions[sessionID] = append(sf.sessions[sessionID], forkMessage{Role: role, Content: content})
}

func (sf *SessionFork) Fork(parentID string, messageIndex int) ([]forkMessage, string) {
	sf.mu.Lock()
	defer sf.mu.Unlock()

	messages := sf.sessions[parentID]
	if messageIndex < 0 || messageIndex >= len(messages) {
		messageIndex = len(messages) - 1
	}

	childID := fmt.Sprintf("%s-fork-%d", parentID, messageIndex)
	sf.sessions[childID] = append([]forkMessage(nil), messages[:messageIndex+1]...)
	return sf.sessions[childID], childID
}

func (sf *SessionFork) GetMessages(sessionID string) []forkMessage {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	return append([]forkMessage(nil), sf.sessions[sessionID]...)
}

func (sf *SessionFork) RenderContext(sessionID string) string {
	msgs := sf.GetMessages(sessionID)
	var b strings.Builder
	b.WriteString("<inherited_conversation>\n")
	for _, m := range msgs {
		b.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, truncateForContext(m.Content, 2000)))
	}
	b.WriteString("</inherited_conversation>\n")
	return b.String()
}

func truncateForContext(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...[truncated]"
}
