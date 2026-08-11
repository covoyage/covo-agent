package gateway

import (
	"context"
	"sync"
)

type contextKey string

const (
	ctxSessionPlatform  contextKey = "session_platform"
	ctxSessionChatID    contextKey = "session_chat_id"
	ctxSessionChatName  contextKey = "session_chat_name"
	ctxSessionThreadID  contextKey = "session_thread_id"
	ctxSessionUserID    contextKey = "session_user_id"
	ctxSessionUserName  contextKey = "session_user_name"
	ctxSessionKey       contextKey = "session_key"
	ctxSessionID        contextKey = "session_id"
	ctxSessionMessageID contextKey = "session_message_id"
)

type SessionContext struct {
	mu        sync.RWMutex
	Platform  string
	ChatID    string
	ChatName  string
	ThreadID  string
	UserID    string
	UserName  string
	SessionID string
	MessageID string
}

func NewSessionContext() *SessionContext {
	return &SessionContext{}
}

func (sc *SessionContext) Set(platform, chatID, chatName, threadID, userID, userName, sessionID, messageID string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.Platform = platform
	sc.ChatID = chatID
	sc.ChatName = chatName
	sc.ThreadID = threadID
	sc.UserID = userID
	sc.UserName = userName
	sc.SessionID = sessionID
	sc.MessageID = messageID
}

func (sc *SessionContext) Snapshot() SessionContext {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return SessionContext{
		Platform:  sc.Platform,
		ChatID:    sc.ChatID,
		ChatName:  sc.ChatName,
		ThreadID:  sc.ThreadID,
		UserID:    sc.UserID,
		UserName:  sc.UserName,
		SessionID: sc.SessionID,
		MessageID: sc.MessageID,
	}
}

func (sc *SessionContext) Clear() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.Platform = ""
	sc.ChatID = ""
	sc.ChatName = ""
	sc.ThreadID = ""
	sc.UserID = ""
	sc.UserName = ""
	sc.SessionID = ""
	sc.MessageID = ""
}

func WithSessionContext(ctx context.Context, sc *SessionContext) context.Context {
	snap := sc.Snapshot()
	ctx = context.WithValue(ctx, ctxSessionPlatform, snap.Platform)
	ctx = context.WithValue(ctx, ctxSessionChatID, snap.ChatID)
	ctx = context.WithValue(ctx, ctxSessionChatName, snap.ChatName)
	ctx = context.WithValue(ctx, ctxSessionThreadID, snap.ThreadID)
	ctx = context.WithValue(ctx, ctxSessionUserID, snap.UserID)
	ctx = context.WithValue(ctx, ctxSessionUserName, snap.UserName)
	ctx = context.WithValue(ctx, ctxSessionKey, snap.Platform+":"+snap.ChatID)
	ctx = context.WithValue(ctx, ctxSessionID, snap.SessionID)
	ctx = context.WithValue(ctx, ctxSessionMessageID, snap.MessageID)
	return ctx
}

func SessionPlatform(ctx context.Context) string  { return ctxString(ctx, ctxSessionPlatform) }
func SessionChatID(ctx context.Context) string    { return ctxString(ctx, ctxSessionChatID) }
func SessionChatName(ctx context.Context) string  { return ctxString(ctx, ctxSessionChatName) }
func SessionThreadID(ctx context.Context) string  { return ctxString(ctx, ctxSessionThreadID) }
func SessionUserID(ctx context.Context) string    { return ctxString(ctx, ctxSessionUserID) }
func SessionUserName(ctx context.Context) string  { return ctxString(ctx, ctxSessionUserName) }
func SessionKey(ctx context.Context) string       { return ctxString(ctx, ctxSessionKey) }
func SessionID(ctx context.Context) string        { return ctxString(ctx, ctxSessionID) }
func SessionMessageID(ctx context.Context) string { return ctxString(ctx, ctxSessionMessageID) }

func ctxString(ctx context.Context, key contextKey) string {
	if v, ok := ctx.Value(key).(string); ok {
		return v
	}
	return ""
}
