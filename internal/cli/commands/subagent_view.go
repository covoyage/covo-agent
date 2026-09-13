package commands

import (
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/agent"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
	agentui "github.com/covoyage/covo-agent/internal/tui"
	"github.com/covoyage/covonaut/tui/chat"
)

// openSubagentView switches the TUI to a spawned sub-agent's live transcript
// view (opencode-style "switch to the subagent view"). Called from the
// dashboard picker when the user selects a "subagent:<id>" entry.
//
// Interaction model:
//   - while the child runs, the transcript refreshes every 500ms;
//   - esc/q/p returns to the main agent view (the overlay closes);
//   - left/right jumps to the previous/next sub-agent's view.
func (s *interactiveSession) openSubagentView(id string) {
	mgr := s.agentRuntime.Current()
	if mgr == nil {
		return
	}
	views := mgr.SubagentViews()
	if _, ok := views.Get(id); !ok {
		return
	}
	s.setSubagentViewOpen(id)

	var (
		mu     sync.Mutex
		closed bool
		gen    int // panel generation; bumped on sibling switch
		ov     chat.OverlayRef
	)
	closePanel := func() {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return
		}
		closed = true
		gen++
		s.clearSubagentViewOpen()
		if ov != nil {
			loadUIBus().ClosePanel(ov)
			ov = nil
		}
	}
	open := func(entryID string) {
		e, ok := views.Get(entryID)
		if !ok {
			return
		}
		liveRegistry := func() *toolssubagent.SubagentRegistry {
			if mgr := s.agentRuntime.Current(); mgr != nil {
				return mgr.SubagentRegistry()
			}
			return nil
		}
		panel := agentui.NewSubagentViewPanel(e, closePanel, func(dir int) {
			// Sibling navigation: prev/next in spawn order, then reopen.
			entries := views.List()
			if len(entries) == 0 {
				return
			}
			idx := -1
			for i, en := range entries {
				if en.ID() == entryID {
					idx = i
					break
				}
			}
			if idx < 0 {
				return
			}
			next := (idx + dir + len(entries)) % len(entries)
			go s.openSubagentView(entries[next].ID())
		},
			func(msg string) bool { // steer: deliver input to the running child
				if r := liveRegistry(); r != nil {
					return r.SendInput(entryID, msg)
				}
				return false
			},
			func() bool { // interrupt the running child
				if r := liveRegistry(); r != nil {
					return r.Interrupt(entryID)
				}
				return false
			})
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return
		}
		gen++ // invalidate the previous panel's refresh ticker
		if ov != nil {
			loadUIBus().ClosePanel(ov)
		}
		ov = loadUIBus().ShowPanel(panel, 90, 90)
	}
	open(id)

	// Live refresh: re-render while the viewed child is running so its
	// transcript streams into the panel. Exits when the panel is closed or
	// replaced by a sibling switch.
	mu.Lock()
	myGen := gen
	mu.Unlock()
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			mu.Lock()
			done := closed || gen != myGen
			mu.Unlock()
			if done {
				return
			}
			e, ok := views.Get(id)
			if !ok || !e.Live() {
				continue // completed views are static; no refresh needed
			}
			if host := loadUIBus().Host(); host != nil {
				host.RequestRender()
			}
		}
	}()
}

// currentSubagentViews returns the current agent's subagent view registry,
// or nil when no agent is active.
func (s *interactiveSession) currentSubagentViews() *agent.SubagentViewRegistry {
	if s.agentRuntime == nil {
		return nil
	}
	mgr := s.agentRuntime.Current()
	if mgr == nil {
		return nil
	}
	return mgr.SubagentViews()
}

// cycleSubagentView is the codex-style fast-switch entry point bound to
// alt+left/alt+right: from the main view it opens the first (dir>0) or last
// (dir<0) sub-agent view; while a view is open it cycles through siblings.
func (s *interactiveSession) cycleSubagentView(dir int) {
	views := s.currentSubagentViews()
	if views == nil {
		return
	}
	entries := views.List()
	if len(entries) == 0 {
		loadUIBus().PrintSystem("no sub-agents in this session")
		return
	}

	s.subagentViewMu.Lock()
	current := s.subagentViewOpenID
	s.subagentViewMu.Unlock()

	idx := -1
	if current != "" {
		for i, e := range entries {
			if e.ID() == current {
				idx = i
				break
			}
		}
	}
	var target string
	if idx < 0 {
		// Not currently viewing: next opens the first, prev the last.
		if dir > 0 {
			target = entries[0].ID()
		} else {
			target = entries[len(entries)-1].ID()
		}
	} else {
		target = entries[(idx+dir+len(entries))%len(entries)].ID()
	}
	s.openSubagentView(target)
}

func (s *interactiveSession) setSubagentViewOpen(id string) {
	s.subagentViewMu.Lock()
	s.subagentViewOpenID = id
	s.subagentViewMu.Unlock()
}

func (s *interactiveSession) clearSubagentViewOpen() {
	s.subagentViewMu.Lock()
	s.subagentViewOpenID = ""
	s.subagentViewMu.Unlock()
}

var subagentIDRe = subagentIDPattern()

func subagentIDPattern() *regexp.Regexp {
	return regexp.MustCompile(`"id"\s*:\s*"((?:sub|agent)-\d+)"`)
}

// installSubagentViewClickHandler wires the chat-history tool-row click hook:
// clicking a sessions_spawn tool row whose result carries a background
// sub-agent id opens that sub-agent's live transcript view. Clicks on rows
// without a resolvable view fall through to the default expand/collapse.
func (s *interactiveSession) installSubagentViewClickHandler() {
	if s.app == nil || s.app.History() == nil {
		return
	}
	s.app.History().SetToolLineClickHandler(func(meta, argPreview, statusText, detail string) bool {
		if !strings.Contains(meta, "sessions_spawn") {
			return false
		}
		views := s.currentSubagentViews()
		if views == nil {
			return false
		}
		// 1) Finished/background rows carry the sub-agent id in the result.
		if m := subagentIDRe.FindStringSubmatch(detail); len(m) >= 2 {
			if _, ok := views.Get(m[1]); ok {
				go s.openSubagentView(m[1])
				return true
			}
		}
		// 2) Still-running row (no result yet): with a single live child it
		// is unambiguous — open it.
		var live []*agent.SubagentViewEntry
		for _, e := range views.List() {
			if e.Live() {
				live = append(live, e)
			}
		}
		if len(live) == 1 {
			go s.openSubagentView(live[0].ID())
			return true
		}
		// 3) Ambiguous (multiple live children): open the dashboard chooser.
		if len(live) > 1 {
			go s.openDashboard()
			return true
		}
		return false
	})
}
