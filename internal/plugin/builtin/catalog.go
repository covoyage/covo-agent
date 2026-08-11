// Package builtin owns the catalog of platform plugins compiled into covo-agent.
package builtin

import (
	"sort"

	apiserver "github.com/covoyage/covo-agent/internal/plugin/platforms/api_server"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/bluebubbles"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/cron"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/dingtalk"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/discord"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/email"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/feishu"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/googlechat"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/homeassistant"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/imessage"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/irc"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/line"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/matrix"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/mattermost"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/msgraph"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/msteams"
	nextcloudtalk "github.com/covoyage/covo-agent/internal/plugin/platforms/nextcloud-talk"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/nostr"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/qqbot"
	signalplatform "github.com/covoyage/covo-agent/internal/plugin/platforms/signal"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/slack"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/sms"
	synologychat "github.com/covoyage/covo-agent/internal/plugin/platforms/synology-chat"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/telegram"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/tlon"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/twitch"
	voicecall "github.com/covoyage/covo-agent/internal/plugin/platforms/voice-call"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/webhook"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/wecom"
	wecomcallback "github.com/covoyage/covo-agent/internal/plugin/platforms/wecom_callback"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/weixin"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/whatsapp"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/yuanbao"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/zalo"
	"github.com/covoyage/covo-agent/internal/plugin/platforms/zalouser"
)

// Entry describes one platform plugin compiled into the binary.
type Entry struct {
	ID          string
	Description string
	New         func() any
}

var catalog = []Entry{
	{ID: "api_server", Description: "REST API Server - HTTP-based messaging endpoint", New: func() any { return apiserver.New() }},
	{ID: "bluebubbles", Description: "BlueBubbles - iMessage bridge for Android/PC", New: func() any { return bluebubbles.New() }},
	{ID: "cron", Description: "Cron Scheduler - scheduled message delivery", New: func() any { return cron.New() }},
	{ID: "dingtalk", Description: "DingTalk - enterprise communication platform", New: func() any { return dingtalk.New() }},
	{ID: "discord", Description: "Discord Bot - gaming/community chat platform", New: func() any { return discord.New() }},
	{ID: "email", Description: "Email - SMTP/IMAP email integration", New: func() any { return email.New() }},
	{ID: "feishu", Description: "Feishu/Lark - enterprise collaboration platform", New: func() any { return feishu.New() }},
	{ID: "googlechat", Description: "Google Chat - Google Workspace Chat API", New: func() any { return googlechat.New() }},
	{ID: "homeassistant", Description: "Home Assistant - smart home integration", New: func() any { return homeassistant.New() }},
	{ID: "imessage", Description: "iMessage - Apple Messages integration", New: func() any { return imessage.New() }},
	{ID: "irc", Description: "IRC - Internet Relay Chat protocol", New: func() any { return irc.New() }},
	{ID: "line", Description: "LINE - LINE Messaging API bot", New: func() any { return line.New() }},
	{ID: "matrix", Description: "Matrix - decentralized communication protocol", New: func() any { return matrix.New() }},
	{ID: "mattermost", Description: "Mattermost - open-source workplace messaging", New: func() any { return mattermost.New() }},
	{ID: "msgraph", Description: "Microsoft Graph - Teams/Outlook integration", New: func() any { return msgraph.New() }},
	{ID: "msteams", Description: "Microsoft Teams - Teams SDK integration", New: func() any { return msteams.New() }},
	{ID: "nextcloud-talk", Description: "Nextcloud Talk - self-hosted chat webhook", New: func() any { return nextcloudtalk.New() }},
	{ID: "nostr", Description: "Nostr - decentralized NIP-04 encrypted DMs", New: func() any { return nostr.New() }},
	{ID: "qqbot", Description: "QQ Bot - QQ messaging platform", New: func() any { return qqbot.New() }},
	{ID: "signal", Description: "Signal - encrypted messaging platform", New: func() any { return signalplatform.New() }},
	{ID: "slack", Description: "Slack Bot - workplace messaging platform", New: func() any { return slack.New() }},
	{ID: "sms", Description: "SMS - text message integration", New: func() any { return sms.New() }},
	{ID: "synology-chat", Description: "Synology Chat - NAS chat webhook integration", New: func() any { return synologychat.New() }},
	{ID: "telegram", Description: "Telegram Bot API - chat messaging platform", New: func() any { return telegram.New() }},
	{ID: "tlon", Description: "Tlon - decentralized messaging on Urbit", New: func() any { return tlon.New() }},
	{ID: "twitch", Description: "Twitch - Twitch Chat integration", New: func() any { return twitch.New() }},
	{ID: "voice-call", Description: "Voice Call - Twilio/Telnyx/Plivo phone calls", New: func() any { return voicecall.New() }},
	{ID: "webhook", Description: "Generic Webhook - custom HTTP integration", New: func() any { return webhook.New() }},
	{ID: "wecom", Description: "WeCom (企业微信) - enterprise WeChat platform", New: func() any { return wecom.New() }},
	{ID: "wecom_callback", Description: "WeCom Callback - enterprise WeChat callback server", New: func() any { return wecomcallback.New() }},
	{ID: "weixin", Description: "WeChat Official Account (微信公众号)", New: func() any { return weixin.New() }},
	{ID: "whatsapp", Description: "WhatsApp Business API - global messaging platform", New: func() any { return whatsapp.New() }},
	{ID: "yuanbao", Description: "Yuanbao (腾讯元宝) - Tencent AI messaging platform", New: func() any { return yuanbao.New() }},
	{ID: "zalo", Description: "Zalo - Vietnam messaging platform Bot API", New: func() any { return zalo.New() }},
	{ID: "zalouser", Description: "Zalo Personal - Zalo personal account via QR code", New: func() any { return zalouser.New() }},
}

// Entries returns a copy of the built-in platform metadata.
func Entries() []Entry {
	entries := make([]Entry, len(catalog))
	copy(entries, catalog)
	return entries
}

// Providers constructs every built-in platform provider.
func Providers() []any {
	providers := make([]any, 0, len(catalog))
	for _, entry := range catalog {
		providers = append(providers, entry.New())
	}
	return providers
}

// Names returns sorted built-in platform IDs.
func Names() []string {
	names := make([]string, 0, len(catalog))
	for _, entry := range catalog {
		names = append(names, entry.ID)
	}
	sort.Strings(names)
	return names
}

// Description returns the user-facing description for a platform ID.
func Description(id string) string {
	for _, entry := range catalog {
		if entry.ID == id {
			return entry.Description
		}
	}
	return ""
}
