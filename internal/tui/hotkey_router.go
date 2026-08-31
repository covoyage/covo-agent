package tui

import (
	"sync"
	"time"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"

	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covo-agent/internal/safego"
)

// HotkeyRouterConfig supplies command callbacks for application-level shortcuts.
type HotkeyRouterConfig struct {
	Stop               func() error
	PrintSystem        func(string)
	OpenSessions       func()
	OpenSessionTree    func()
	OpenTodos          func()
	OpenSkillCenter    func()
	OpenKeyHelp        func()
	OpenModelPicker    func()
	OpenEditor         func()
	OpenChangedFiles   func()
	OpenSettings       func()
	OpenPromptQueue    func()
	OpenCommandPalette func()
	OpenHistorySearch  func()
	OpenDashboard      func()
	ToggleVerbose      func()
	Suggestions        *SuggestionsManager
	QuitWindow         time.Duration
}

// HotkeyRouter dispatches application-level keyboard shortcuts.
type HotkeyRouter struct {
	config         HotkeyRouterConfig
	exitWarnMu     sync.Mutex
	exitWarnActive bool
	exitWarnTimer  *time.Timer
}

func NewHotkeyRouter(config HotkeyRouterConfig) *HotkeyRouter {
	if config.QuitWindow <= 0 {
		config.QuitWindow = 3 * time.Second
	}
	return &HotkeyRouter{config: config}
}

func (*HotkeyRouter) Render(int64) []string { return nil }
func (*HotkeyRouter) Invalidate()           {}
func (router *HotkeyRouter) Update(message core.Msg) core.Cmd {
	if key, ok := message.(core.KeyMsg); ok {
		router.HandleInput(key.Data)
	}
	return nil
}

func (router *HotkeyRouter) HandleInput(data string) {
	switch {
	case terminal.MatchesKey(data, "ctrl+/"):
		invoke(router.config.OpenKeyHelp)
	case terminal.MatchesKey(data, "ctrl+q") || terminal.MatchesKey(data, "ctrl+d"):
		router.handleQuit()
	case terminal.MatchesKey(data, "ctrl+o"):
		invoke(router.config.OpenSessions)
	case terminal.MatchesKey(data, "ctrl+t"):
		invoke(router.config.OpenTodos)
	case terminal.MatchesKey(data, "ctrl+k"):
		invoke(router.config.OpenCommandPalette)
	case terminal.MatchesKey(data, "ctrl+s"):
		invoke(router.config.OpenHistorySearch)
	case terminal.MatchesKey(data, "ctrl+p"):
		invoke(router.config.OpenModelPicker)
	case terminal.MatchesKey(data, "ctrl+y"):
		invoke(router.config.OpenSessionTree)
	case terminal.MatchesKey(data, "ctrl+e"):
		invoke(router.config.OpenEditor)
	case terminal.MatchesKey(data, "ctrl+g"):
		invoke(router.config.OpenChangedFiles)
	case terminal.MatchesKey(data, "ctrl+r"):
		invoke(router.config.ToggleVerbose)
	case terminal.MatchesKey(data, "f2"):
		invoke(router.config.OpenSettings)
	case terminal.MatchesKey(data, "ctrl+shift+p"):
		invoke(router.config.OpenPromptQueue)
	default:
		if router.config.Suggestions != nil && router.config.Suggestions.HasActive() {
			router.config.Suggestions.HandleHotkey(data)
		}
	}
}

func (router *HotkeyRouter) handleQuit() {
	router.exitWarnMu.Lock()
	if router.exitWarnActive {
		if router.exitWarnTimer != nil {
			router.exitWarnTimer.Stop()
		}
		router.exitWarnActive = false
		router.exitWarnMu.Unlock()
		if router.config.Stop != nil {
			safego.SafeGo(func() { _ = router.config.Stop() }, nil)
		}
		return
	}
	router.exitWarnActive = true
	if router.exitWarnTimer != nil {
		router.exitWarnTimer.Stop()
	}
	router.exitWarnTimer = time.AfterFunc(router.config.QuitWindow, func() {
		router.exitWarnMu.Lock()
		router.exitWarnActive = false
		router.exitWarnMu.Unlock()
	})
	router.exitWarnMu.Unlock()
	if router.config.PrintSystem != nil {
		router.config.PrintSystem(i18n.T("helpers.quit_confirm"))
	}
}

func invoke(callback func()) {
	if callback != nil {
		callback()
	}
}

var _ core.Component = (*HotkeyRouter)(nil)
var _ core.Updatable = (*HotkeyRouter)(nil)
