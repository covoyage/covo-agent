package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
)

// SubagentViewEntry is the read-only data the transcript panel renders.
// Kept interface-shaped so the panel never imports the agent package.
type SubagentViewEntry interface {
	ID() string
	Task() string
	Status() string
	Live() bool
	Depth() int
	StartedAt() time.Time
	Messages() []agentcore.Message
}

// SubagentViewPanel renders a live read-only transcript of one spawned
// subagent (opencode-style "switch to the subagent view"): the panel
// refreshes while the child runs, and esc/q returns to the main view.
type SubagentViewPanel struct {
	mu          sync.Mutex
	entry       SubagentViewEntry
	onClose     func()
	onPeer      func(dir int)         // switch to prev/next sibling (-1/+1); nil disables
	onSteer     func(msg string) bool // deliver a steering message to the running child; nil disables
	onInterrupt func() bool           // interrupt the running child; nil disables

	offset    int // scroll offset from the bottom (0 = follow tail)
	width     int64
	steerMode bool   // capturing a steering message
	steerBuf  []rune // steering message under composition
}

// NewSubagentViewPanel builds the transcript panel. onSteer/onInterrupt are
// optional in-place controls for a running child (hermes-style dashboard
// controls); nil disables the corresponding key.
func NewSubagentViewPanel(entry SubagentViewEntry, onClose func(), onPeer func(dir int), onSteer func(string) bool, onInterrupt func() bool) *SubagentViewPanel {
	return &SubagentViewPanel{entry: entry, onClose: onClose, onPeer: onPeer, onSteer: onSteer, onInterrupt: onInterrupt}
}

// ID returns the displayed subagent id (used for sibling navigation).
func (p *SubagentViewPanel) ID() string { return p.entry.ID() }

// Invalidate clears cached layout (core.Component).
func (p *SubagentViewPanel) Invalidate() {}

// Render draws the header and transcript window (core.Component).
func (p *SubagentViewPanel) Render(width int64) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.width = width

	var out []string
	status := p.entry.Status()
	flag := ""
	if p.entry.Live() {
		flag = " ● live"
	}
	title := SubagentTaskPreview(p.entry.Task(), int(width)-24)
	out = append(out,
		fmt.Sprintf("── Subagent %s [%s]%s ──", p.entry.ID(), status, flag),
		fmt.Sprintf("task: %s", title),
		"",
	)

	lines := transcriptLines(p.entry.Messages(), width)
	height := panelBodyHeight(int(width))
	maxOffset := len(lines) - height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if p.offset > maxOffset {
		p.offset = maxOffset
	}
	start := len(lines) - height - p.offset
	if start < 0 {
		start = 0
	}
	end := start + height
	if end > len(lines) {
		end = len(lines)
	}
	out = append(out, lines[start:end]...)

	if p.steerMode {
		out = append(out, "", fmt.Sprintf("steer> %s█", string(p.steerBuf)), "enter: send · esc: cancel")
	} else {
		footer := "esc/q: back · ↑/↓ scroll · alt+←/→ prev/next"
		if p.onSteer != nil && p.entry.Live() {
			footer += " · s: steer"
		}
		if p.onInterrupt != nil && p.entry.Live() {
			footer += " · x: interrupt"
		}
		out = append(out, "", footer)
	}
	return out
}

// Update handles key input (core.Updatable).
func (p *SubagentViewPanel) Update(msg core.Msg) core.Cmd {
	keyMsg, ok := msg.(core.KeyMsg)
	if !ok {
		return nil
	}
	keys := terminal.ParseKeys(keyMsg.Data)
	if len(keys) == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	lines := transcriptLines(p.entry.Messages(), p.width)
	height := panelBodyHeight(int(p.width))
	maxOffset := len(lines) - height
	if maxOffset < 0 {
		maxOffset = 0
	}

	for _, key := range keys {
		if key.IsRelease() {
			continue
		}
		// Steer composition mode captures all printable input.
		if p.steerMode {
			switch {
			case key.Name == "escape":
				p.steerMode = false
				p.steerBuf = nil
				return nil
			case key.Name == "enter":
				msg := strings.TrimSpace(string(p.steerBuf))
				p.steerMode = false
				p.steerBuf = nil
				if msg != "" && p.onSteer != nil {
					p.onSteer(msg)
				}
				return nil
			case key.Name == "backspace":
				if len(p.steerBuf) > 0 {
					p.steerBuf = p.steerBuf[:len(p.steerBuf)-1]
				}
			case key.Rune != 0 && key.Mods&terminal.ModCtrl == 0:
				p.steerBuf = append(p.steerBuf, key.Rune)
			}
			return nil
		}
		switch key.Name {
		case "escape", "q", "p":
			// Return to the main agent view.
			if p.onClose != nil {
				p.onClose()
			}
			return nil
		case "s":
			// Begin a steering message (running children only).
			if p.entry.Live() && p.onSteer != nil {
				p.steerMode = true
				p.steerBuf = nil
			}
			return nil
		case "x":
			// Interrupt the running child in place.
			if p.entry.Live() && p.onInterrupt != nil {
				p.onInterrupt()
			}
			return nil
		case "up", "k":
			if p.offset < maxOffset {
				p.offset++
			}
		case "down", "j":
			if p.offset > 0 {
				p.offset--
			}
		case "pgup":
			p.offset += height
			if p.offset > maxOffset {
				p.offset = maxOffset
			}
		case "pgdown", "pagedown":
			p.offset -= height
			if p.offset < 0 {
				p.offset = 0
			}
		case "home", "g":
			p.offset = maxOffset
		case "end", "G":
			p.offset = 0
		case "left":
			if p.onPeer != nil {
				p.onPeer(-1)
			}
		case "right":
			if p.onPeer != nil {
				p.onPeer(1)
			}
		}
	}
	return nil
}

// SubagentTaskPreview distills a sub-agent task into a one-line preview:
// persona/role injections wrap guidance in <system-reminder> blocks followed
// by a "## Your Task" marker — the real task lives after that marker. Lines
// that are empty, reminder tags, or headings are skipped. maxRunes <= 0
// means no truncation.
func SubagentTaskPreview(task string, maxRunes int) string {
	if i := strings.LastIndex(task, "## Your Task"); i >= 0 {
		task = task[i+len("## Your Task"):]
	}
	out := ""
	for _, line := range strings.Split(task, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "<system-reminder>") ||
			strings.HasPrefix(t, "</system-reminder>") || strings.HasPrefix(t, "## ") {
			continue
		}
		out = t
		break
	}
	if out == "" {
		out = strings.TrimSpace(strings.Split(task, "\n")[0])
	}
	if maxRunes > 0 {
		if runes := []rune(out); len(runes) > maxRunes {
			out = string(runes[:maxRunes]) + "..."
		}
	}
	return out
}

// transcriptLines converts child messages into display lines.
func transcriptLines(msgs []agentcore.Message, width int64) []string {
	var out []string
	for _, m := range msgs {
		content := strings.TrimSpace(m.Content)
		if content == "" && len(m.ToolCalls) == 0 {
			continue
		}
		switch m.Role {
		case "user":
			out = append(out, wrapLine("❯ "+firstLine(content), width)...)
		case "assistant":
			for _, l := range wrapLine(content, width) {
				out = append(out, "  "+l)
			}
		case "tool":
			toolName := m.Name
			if toolName == "" {
				toolName = m.ToolCallID
			}
			out = append(out, wrapLine(fmt.Sprintf("  ⚙ [%s] %s", toolName, firstLine(content)), width)...)
		case "system":
			out = append(out, wrapLine(fmt.Sprintf("  · %s", firstLine(content)), width)...)
		}
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				out = append(out, wrapLine(fmt.Sprintf("  → %s", tc.Name), width)...)
			}
		}
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

// wrapLine hard-wraps a line to the viewport width.
func wrapLine(s string, width int64) []string {
	if width <= 8 {
		return []string{s}
	}
	w := int(width) - 2
	runes := []rune(s)
	if len(runes) <= w {
		return []string{s}
	}
	var out []string
	for len(runes) > w {
		out = append(out, string(runes[:w]))
		runes = runes[w:]
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}

// panelBodyHeight derives the transcript window height from the terminal
// width as a cheap proxy for height.
func panelBodyHeight(width int) int {
	h := width / 3
	if h < 12 {
		h = 12
	}
	if h > 40 {
		h = 40
	}
	return h
}
