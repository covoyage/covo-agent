package tui

import (
	"time"

	"github.com/covoyage/covonaut/tui/chat"
	enginetui "github.com/covoyage/covonaut/tui"
	"github.com/covoyage/covonaut/tui/terminal"
)

// CaptureTerminalProbe 等待引擎端能力探测（≤ProbeTimeout）完成后，用结果润色
// 进程级 TerminalContext。best-effort：不阻塞调用方，探针未启用/无结果时静默。
func CaptureTerminalProbe(app *chat.ChatApp) {
	if app == nil {
		return
	}
	go func() {
		deadline := time.Now().Add(terminal.ProbeTimeout + 60*time.Millisecond)
		var caps terminal.Capabilities
		ok := false
		for {
			var has bool
			caps, has = enginetui.TerminalProbe(app)
			if has && caps.Probed {
				ok = true
				break
			}
			if time.Now().After(deadline) {
				ok = has
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if ok {
			ApplyTerminalProbe(caps)
		}
	}()
}