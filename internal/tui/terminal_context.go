package tui

import (
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/terminal"
)

// TerminalContextGlobal 缓存进程级终端能力快照：首次通过 TerminalContext()
// 访问时从环境初始化，之后可由 ApplyTerminalProbe 用引擎端探测结果润色。
var (
	terminalCtxOnce       sync.Once
	terminalCtxMu         sync.RWMutex
	terminalContextGlobal = &TerminalContext{OS: runtime.GOOS}
)

// ---------------------------------------------------------------------------
// TerminalContext — 终端能力自适应探测。
//
// 在 TUI 层提供终端品牌探测、键盘协议能力、颜色支持等级等，
// 供 ActionRegistry 和 HotkeyRouter 做终端自适应决策。
//
// 探测策略（不执行子进程、不发 OSC 查询序列——只读环境变量）：
//   - TERM_PROGRAM → AppleTerminal / iTerm.app / WezTerm / vscode / ...
//   - TERM → xterm-256color / dumb / ...
//   - COLORTERM → truecolor
//   - TMUX / STY → 多路复用器嵌套
//   - SSH_CONNECTION → SSH 远程
// ---------------------------------------------------------------------------

// TerminalBrand 标识终端品牌。
type TerminalBrand string

const (
	BrandUnknown        TerminalBrand = "unknown"
	BrandAppleTerminal  TerminalBrand = "apple_terminal"
	BrandITerm2         TerminalBrand = "iterm2"
	BrandKitty          TerminalBrand = "kitty"
	BrandWezTerm        TerminalBrand = "wezterm"
	BrandGhostty        TerminalBrand = "ghostty"
	BrandAlacritty      TerminalBrand = "alacritty"
	BrandVSCode         TerminalBrand = "vscode"
	BrandVSCodeInsiders TerminalBrand = "vscode_insiders"
	BrandTabby          TerminalBrand = "tabby"
	BrandHyper          TerminalBrand = "hyper"
	BrandTerminalApp    TerminalBrand = "terminal_app" // macOS Terminal.app (generic)
	BrandLinuxConsole   TerminalBrand = "linux_console"
)

// MultiplexerKind 标识多路复用器类型。
type MultiplexerKind string

const (
	MuxNone   MultiplexerKind = "none"
	MuxTmux   MultiplexerKind = "tmux"
	MuxScreen MultiplexerKind = "screen"
	MuxByobu  MultiplexerKind = "byobu"
)

// ColorLevel 标识颜色支持等级。
type ColorLevel string

const (
	ColorNone      ColorLevel = "none"
	ColorBasic     ColorLevel = "basic"
	Color256       ColorLevel = "256"
	ColorTrueColor ColorLevel = "truecolor"
)

// TerminalContext 是终端能力的快照，在启动时一次性探测。
type TerminalContext struct {
	Brand       TerminalBrand
	Multiplexer MultiplexerKind
	Color       ColorLevel
	OverSSH     bool
	OS          string // runtime.GOOS
	TERMProgram string
	TERM        string

	// Kitty keyboard protocol support hints (from env, not probed).
	// We conservatively assume KKP is NOT available unless we detect a
	// terminal known to support it. The covonaut terminal layer may
	// override this at runtime via PushKittyKeyboard().
	KittyKeyboardLikely bool
}

// DetectTerminalContext 从环境变量探测终端能力（只读，无副作用）。
func DetectTerminalContext() *TerminalContext {
	ctx := &TerminalContext{
		OS:          runtime.GOOS,
		TERMProgram: os.Getenv("TERM_PROGRAM"),
		TERM:        os.Getenv("TERM"),
	}

	// Brand 探测
	ctx.Brand = detectBrand(ctx.TERMProgram, ctx.TERM)

	// 多路复用器
	if os.Getenv("TMUX") != "" {
		ctx.Multiplexer = MuxTmux
	} else if os.Getenv("STY") != "" {
		ctx.Multiplexer = MuxScreen
	} else if os.Getenv("BYOBU_BACKEND") != "" || os.Getenv("BYOBU_CONFIG_DIR") != "" {
		ctx.Multiplexer = MuxByobu
	} else {
		ctx.Multiplexer = MuxNone
	}

	// SSH
	ctx.OverSSH = os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != ""

	// 颜色
	ctx.Color = detectColorLevel(ctx.TERM, os.Getenv("COLORTERM"))

	// Kitty keyboard protocol 推测
	ctx.KittyKeyboardLikely = detectKittyKeyboard(ctx.Brand, ctx.Multiplexer)

	return ctx
}

// IsVSCodeFamily 返回是否属于 VS Code 系列终端（VS Code、Zed 等嵌入式终端）。
// 这些终端通常用 Ctrl+D 退出（而非 Ctrl+Q），且可能不支持 Kitty keyboard protocol。
func (c *TerminalContext) IsVSCodeFamily() bool {
	switch c.Brand {
	case BrandVSCode, BrandVSCodeInsiders:
		return true
	default:
		// Zed terminal also reports TERM_PROGRAM=vscode
		return c.TERMProgram == "vscode"
	}
}

// CtrlDotUnreliable 返回 Ctrl+. 在当前终端是否不可靠。
// 不可靠的终端需要用 Ctrl+X 或 ? 作为快捷键帮助的备选主键。
func (c *TerminalContext) CtrlDotUnreliable() bool {
	// tmux with extended-keys off, screen, unknown hosts
	if c.Multiplexer == MuxScreen {
		return true
	}
	if c.Brand == BrandUnknown && c.TERM == "" {
		return true
	}
	// VS Code family: no KKP, host may steal Ctrl+I
	return c.IsVSCodeFamily()
}

// SupportsTrueColor 返回终端是否支持 24-bit 真彩色。
func (c *TerminalContext) SupportsTrueColor() bool {
	return c.Color == ColorTrueColor
}

// Supports256Color 返回终端是否支持 256 色。
func (c *TerminalContext) Supports256Color() bool {
	return c.Color == Color256 || c.Color == ColorTrueColor
}

// IsMinimal 返回终端是否应该使用最小模式（无 alt-screen）。
// 在不支持完整 TUI 功能的终端中使用最小模式。
func (c *TerminalContext) IsMinimal() bool {
	return c.Color == ColorNone || (c.Brand == BrandUnknown && c.OverSSH)
}

// QuitKey 返回该终端推荐的退出键（展示用）。
func (c *TerminalContext) QuitKey() string {
	if c.IsVSCodeFamily() {
		return "Ctrl+D"
	}
	return "Ctrl+Q"
}

// NeedsModifierRescue 返回当前终端是否需要 macOS CoreGraphics 修饰键侧信道。
// 原生 macOS 终端（Apple Terminal、iTerm2）以及无法识别的 macOS 裸终端都有
// 丢失 Shift/Option/Cmd + Enter 修饰位的风险；CoreGraphics 快照是幂等的
// （只反映物理按键），因此覆盖得宽一些没有副作用。
func (c *TerminalContext) NeedsModifierRescue() bool {
	if c.OS != "darwin" {
		return false
	}
	switch c.Brand {
	case BrandAppleTerminal, BrandITerm2:
		return true
	}
	return c.Brand == BrandUnknown && c.TERMProgram == ""
}

// --- internal detection helpers ---

func detectBrand(termProgram, term string) TerminalBrand {
	tp := strings.ToLower(termProgram)

	switch {
	case tp == "apple_terminal":
		return BrandAppleTerminal
	case tp == "iterm.app" || tp == "iterm2":
		return BrandITerm2
	case tp == "kitty":
		return BrandKitty
	case tp == "wezterm":
		return BrandWezTerm
	case tp == "ghostty":
		return BrandGhostty
	case tp == "alacritty":
		return BrandAlacritty
	case tp == "vscode":
		return BrandVSCode
	case tp == "vscode-insiders" || tp == "code-insiders":
		return BrandVSCodeInsiders
	case tp == "tabby":
		return BrandTabby
	case tp == "hyper":
		return BrandHyper
	case strings.Contains(tp, "terminal"):
		return BrandTerminalApp
	case term == "linux" || term == "linux-c-nc":
		return BrandLinuxConsole
	default:
		return BrandUnknown
	}
}

func detectColorLevel(term, colorterm string) ColorLevel {
	ct := strings.ToLower(colorterm)
	t := strings.ToLower(term)

	if ct == "truecolor" || ct == "24bit" {
		return ColorTrueColor
	}
	if strings.Contains(t, "256color") || strings.Contains(t, "256") {
		return Color256
	}
	if t == "dumb" || t == "" {
		return ColorNone
	}
	return ColorBasic
}

// detectKittyKeyboard 推测终端是否支持 Kitty keyboard protocol。
// 只对已知支持的终端返回 true；保守策略——不确定就返回 false。
func detectKittyKeyboard(brand TerminalBrand, mux MultiplexerKind) bool {
	// screen 不支持 KKP
	if mux == MuxScreen {
		return false
	}
	switch brand {
	case BrandKitty, BrandGhostty, BrandWezTerm, BrandITerm2:
		// 这些终端原生支持 KKP
		// tmux 透传 KKP 取决于配置，保守返回 false
		if mux == MuxTmux {
			return false
		}
		return true
	default:
		return false
	}
}

// ApplyTerminalProbe 用引擎端（covonaut）主动探测的结果润色终端上下文。
// covonaut 在 Start 时对可探测的终端（无 env 快路径命中的）做一次 ≤200ms 的
// DA1/DA2/XTVERSION 探测，结果合并 env 后由引擎缓存；这里只把探测补充到的
// 信息（品牌、真彩色、以及 kitty keyboard protocol 证据）回写本地上下文。
func ApplyTerminalProbe(caps terminal.Capabilities) {
	if !caps.Probed || caps.Brand == "" && !caps.TrueColor {
		return
	}
	cp := *GetTerminalContext()
	if caps.Brand != "" {
		cp.Brand = brandFromNormalized(caps.Brand)
	}
	if caps.TrueColor && cp.Color != ColorTrueColor {
		cp.Color = ColorTrueColor
	}
	if caps.KittyKeyboard {
		cp.KittyKeyboardLikely = true
	}
	storeTerminalContext(&cp)
}

// brandFromNormalized maps covonaut 的归一化品牌 token 到本地 TerminalBrand。
func brandFromNormalized(token string) TerminalBrand {
	switch token {
	case "kitty":
		return BrandKitty
	case "wezterm":
		return BrandWezTerm
	case "ghostty":
		return BrandGhostty
	case "alacritty":
		return BrandAlacritty
	case "iterm":
		return BrandITerm2
	default:
		return BrandUnknown
	}
}

// GetTerminalContext 返回进程级终端能力快照。首次访问时从环境初始化，之后可被
// ApplyTerminalProbe 润色。返回值为不可变快照，可安全并发读取。
func GetTerminalContext() *TerminalContext {
	terminalCtxOnce.Do(func() { storeTerminalContext(DetectTerminalContext()) })
	terminalCtxMu.RLock()
	defer terminalCtxMu.RUnlock()
	return terminalContextGlobal
}

// storeTerminalContext 原子地替换进程级快照。
func storeTerminalContext(c *TerminalContext) {
	terminalCtxMu.Lock()
	terminalContextGlobal = c
	terminalCtxMu.Unlock()
}
