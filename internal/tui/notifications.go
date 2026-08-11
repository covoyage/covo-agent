package tui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// 通知系统 — 终端通知。
//
// 通知能力包括：
//   - 焦点追踪（idle 检测）
//   - 睡眠抑制（caffeinate）
//   - 动态标题
//   - OSC 9;4 进度条
//   - 事件类型（turn complete / approval required / error）
//
// 本模块实现基础终端通知能力。
// ---------------------------------------------------------------------------

// NotificationEventKind 标识通知事件类型。
type NotificationEventKind int

const (
	NotifyTurnComplete NotificationEventKind = iota
	NotifyApprovalRequired
	NotifyError
	NotifyBgTaskComplete
)

// NotificationEvent 是一个通知事件。
type NotificationEvent struct {
	Kind      NotificationEventKind
	Title     string
	Body      string
	Timestamp time.Time
}

// NotificationService 管理终端通知。
type NotificationService struct {
	mu         sync.Mutex
	enabled    bool
	focused    bool // 终端是否聚焦
	titleBase  string
	lastNotify time.Time
}

// NewNotificationService 创建通知服务。
func NewNotificationService(titleBase string) *NotificationService {
	return &NotificationService{
		enabled:   true,
		focused:   true,
		titleBase: titleBase,
	}
}

// SetEnabled 启用/禁用通知。
func (n *NotificationService) SetEnabled(enabled bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.enabled = enabled
}

// SetFocused 设置终端焦点状态。
func (n *NotificationService) SetFocused(focused bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.focused = focused
}

// Notify 发送一个通知。
// 如果终端聚焦则跳过（用户已经在看），否则发送终端通知。
func (n *NotificationService) Notify(event NotificationEvent) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.enabled {
		return
	}

	// 焦点检查：如果终端聚焦，不发送通知
	if n.focused {
		return
	}

	n.lastNotify = time.Now()

	// 发送 OSC 9 序列（终端通知）
	switch event.Kind {
	case NotifyTurnComplete:
		n.sendOSCNotification(event.Title, event.Body)
		n.setTitle(fmt.Sprintf("%s ✓ turn complete", n.titleBase))
	case NotifyApprovalRequired:
		n.sendOSCNotification(event.Title, event.Body)
		n.setTitle(fmt.Sprintf("%s ⚠ approval needed", n.titleBase))
	case NotifyError:
		n.sendOSCNotification(event.Title, event.Body)
		n.setTitle(fmt.Sprintf("%s ✗ error", n.titleBase))
	case NotifyBgTaskComplete:
		n.sendOSCNotification(event.Title, event.Body)
	}

	// 延迟恢复标题
	go func() {
		time.Sleep(5 * time.Second)
		n.resetTitle()
	}()
}

// sendOSCNotification 发送 OSC 9 终端通知序列。
func (n *NotificationService) sendOSCNotification(title, body string) {
	// OSC 9;4 格式（iTerm2/Ghostty 支持）
	// \x1b]9;4;0;title\x07
	if title == "" {
		title = "covo-agent"
	}
	// 安全处理：去掉控制字符
	title = sanitizeNotificationText(title)
	body = sanitizeNotificationText(body)

	fmt.Fprintf(os.Stderr, "\x1b]9;4;0;%s\x07", title)
	if body != "" {
		fmt.Fprintf(os.Stderr, "\x1b]9;4;1;%s\x07", body)
	}
}

// setTitle 设置终端标题。
func (n *NotificationService) setTitle(title string) {
	title = sanitizeNotificationText(title)
	fmt.Fprintf(os.Stderr, "\x1b]2;%s\x07", title)
}

// resetTitle 恢复基础标题。
func (n *NotificationService) resetTitle() {
	n.mu.Lock()
	base := n.titleBase
	n.mu.Unlock()
	n.setTitle(base)
}

// sanitizeNotificationText 移除不安全字符。
func sanitizeNotificationText(s string) string {
	s = strings.ReplaceAll(s, "\x1b", "")
	s = strings.ReplaceAll(s, "\x07", "")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

// FormatNotificationBody 格式化通知正文。
func FormatNotificationBody(kind NotificationEventKind, details string) string {
	switch kind {
	case NotifyTurnComplete:
		return fmt.Sprintf("Turn complete: %s", details)
	case NotifyApprovalRequired:
		return fmt.Sprintf("Approval needed: %s", details)
	case NotifyError:
		return fmt.Sprintf("Error: %s", details)
	case NotifyBgTaskComplete:
		return fmt.Sprintf("Background task done: %s", details)
	}
	return details
}
