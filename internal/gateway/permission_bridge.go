package gateway

import (
	"context"

	"github.com/covoyage/covo-agent/internal/agent/approval"
)

// ApprovalBridge returns an external approver for approval.System: manual
// approvals from gateway-mode agents are registered in the permission store
// (attributed to the platform conversation carried in ctx) and block until a
// human decides via the IPC API — permissions_list_open /
// permissions_respond MCP tools, or `covo-agent gateway permission`.
func (g *Gateway) ApprovalBridge() func(ctx context.Context, req approval.ExternalApprovalRequest) (string, error) {
	perms, events := g.perms, g.events
	return func(ctx context.Context, req approval.ExternalApprovalRequest) (string, error) {
		registered := perms.Register(PermissionRequest{
			Platform:    PlatformFromContext(ctx),
			ChannelID:   ChannelFromContext(ctx),
			UserID:      UserIDFromContext(ctx),
			UserName:    UserNameFromContext(ctx),
			SessionKey:  req.SessionKey,
			Command:     req.Command,
			PatternKey:  req.PatternKey,
			Description: req.Description,
		})
		events.Append(Event{
			Type: EventPermissionRequest, Platform: registered.Platform,
			ChannelID: registered.ChannelID, UserID: registered.UserID,
			UserName: registered.UserName,
			Text:     req.Command, Detail: req.Description,
		})
		choice, err := perms.Wait(ctx, registered.ID)
		events.Append(Event{
			Type: EventPermissionDecision, Platform: registered.Platform,
			ChannelID: registered.ChannelID, UserID: registered.UserID,
			UserName: registered.UserName,
			Text:     req.Command, Detail: choice,
		})
		return choice, err
	}
}
