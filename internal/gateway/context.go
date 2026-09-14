package gateway

import (
	"context"

	"github.com/covoyage/covo-agent/internal/plugin"
)

// Message origin context keys. The gateway injects these into the context
// handed to agents so downstream consumers (e.g. the approval bridge) can
// attribute events back to the platform conversation they came from.
type ctxKey int

const (
	ctxKeyPlatform ctxKey = iota
	ctxKeyChannel
	ctxKeyUserID
	ctxKeyUserName
)

// WithMessageContext returns a context carrying the message origin.
func WithMessageContext(ctx context.Context, msg plugin.IncomingMessage) context.Context {
	ctx = context.WithValue(ctx, ctxKeyPlatform, msg.Platform)
	ctx = context.WithValue(ctx, ctxKeyChannel, msg.ChannelID)
	ctx = context.WithValue(ctx, ctxKeyUserID, msg.UserID)
	ctx = context.WithValue(ctx, ctxKeyUserName, msg.UserName)
	return ctx
}

// PlatformFromContext returns the platform name the current agent turn
// originated from ("" outside gateway handling).
func PlatformFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyPlatform).(string)
	return v
}

// ChannelFromContext returns the channel/conversation ID of the current turn.
func ChannelFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyChannel).(string)
	return v
}

// UserIDFromContext returns the sender's platform user ID.
func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyUserID).(string)
	return v
}

// UserNameFromContext returns the sender's display name.
func UserNameFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyUserName).(string)
	return v
}
