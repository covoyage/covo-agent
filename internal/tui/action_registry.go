package tui

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/terminal"

	"github.com/covoyage/covo-agent/internal/i18n"
)

// ---------------------------------------------------------------------------
// Action Registry — 统一快捷键注册表。
//
// 在 covonaut KeybindingsManager 之上增加：
//   - ActionId 枚举（编译期检查，无字符串拼写错误）
//   - Category 分组（Essentials / Navigation / Panels / Scrollback / Agent / Input）
//   - When 上下文（Always / AgentScreen / PromptFocused / ScrollbackFocused）
//   - Label + Description + LongHelp（自省：快捷键帮助、提示栏）
//   - 终端自适应（不同终端返回不同键绑定）
//
// 三个消费者：
//   - 快捷键提示栏：registry.Hints(context) → 过滤+排序后的提示
//   - 快捷键帮助面板：registry.All() → 全量列表
//   - 键派发路由：registry.Lookup(data, context) → 匹配的 ActionId
// ---------------------------------------------------------------------------

// ActionId 唯一标识一个快捷键动作。
type ActionId string

const (
	// Essentials
	ActionQuit      ActionId = "quit"
	ActionHelp      ActionId = "help"
	ActionInterrupt ActionId = "interrupt"

	// Panels
	ActionOpenSessions       ActionId = "open_sessions"
	ActionOpenTodos          ActionId = "open_todos"
	ActionOpenSkillCenter    ActionId = "open_skill_center"
	ActionOpenCommandPalette ActionId = "open_command_palette"
	ActionOpenHistorySearch  ActionId = "open_history_search"
	ActionOpenDashboard      ActionId = "open_dashboard"
	ActionOpenModelPicker    ActionId = "open_model_picker"
	ActionOpenEditor         ActionId = "open_editor"
	ActionOpenSessionTree    ActionId = "open_session_tree"
	ActionOpenChangedFiles   ActionId = "open_changed_files"

	// Input
	ActionSubmitPrompt ActionId = "submit_prompt"
	ActionNewLine      ActionId = "new_line"
	ActionTabComplete  ActionId = "tab_complete"
)

// Category 分组快捷键用于帮助面板展示。
type Category int

const (
	CatEssentials Category = iota // Essentials
	CatPanels                     // Panels
	CatInput                      // Input
)

func (c Category) String() string {
	switch c {
	case CatEssentials:
		return "Essentials"
	case CatPanels:
		return "Panels"
	case CatInput:
		return "Input"
	default:
		return "Other"
	}
}

// When 描述快捷键生效的上下文（输入冒泡的层级）。
type When int

const (
	WhenAlways            When = iota // 全局生效
	WhenAgentScreen                   // Agent 屏幕（非弹窗、非搜索模式）
	WhenPromptFocused                 // Prompt 聚焦时
	WhenScrollbackFocused             // Scrollback 聚焦时
)

func (w When) String() string {
	switch w {
	case WhenAlways:
		return "always"
	case WhenAgentScreen:
		return "agent"
	case WhenPromptFocused:
		return "prompt"
	case WhenScrollbackFocused:
		return "scrollback"
	default:
		return "unknown"
	}
}

// ActionDef 描述一个快捷键动作。
type ActionDef struct {
	ID          ActionId
	Label       string           // 短标签（如 "quit"）
	Description string           // 描述（如 "Quit application"）
	DefaultKeys []terminal.KeyID // 默认键（如 ["ctrl+q", "ctrl+d"]）
	AltKeys     []terminal.KeyID // 备选键
	Category    Category
	Context     When
	LongHelp    string // 可选的详细帮助
}

// ActionRegistry 是所有快捷键的集中注册表。
type ActionRegistry struct {
	mu    sync.RWMutex
	defs  map[ActionId]ActionDef
	order []ActionId // 注册顺序
	km    *terminal.KeybindingsManager
}

// NewActionRegistry 创建空注册表。
func NewActionRegistry() *ActionRegistry {
	return &ActionRegistry{
		defs: make(map[ActionId]ActionDef),
	}
}

// Register 注册或覆盖一个动作定义。
func (r *ActionRegistry) Register(def ActionDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.defs[def.ID]; !exists {
		r.order = append(r.order, def.ID)
	}
	r.defs[def.ID] = def

	// 同步到 KeybindingsManager（如果已设置）
	if r.km != nil {
		r.km.Register(string(def.ID), terminal.KeybindingDef{
			DefaultKeys: def.DefaultKeys,
			Description: def.Description,
		})
	}
}

// AttachKeybindingsManager 关联一个 KeybindingsManager，后续注册会自动同步。
func (r *ActionRegistry) AttachKeybindingsManager(km *terminal.KeybindingsManager) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.km = km
	// 把已有定义同步过去
	for _, id := range r.order {
		def := r.defs[id]
		km.Register(string(def.ID), terminal.KeybindingDef{
			DefaultKeys: def.DefaultKeys,
			Description: def.Description,
		})
	}
}

// Lookup 检查原始输入 data 是否匹配某动作（在给定上下文中）。
// 返回匹配的 ActionId，或 "" 表示无匹配。
func (r *ActionRegistry) Lookup(data string, context When) ActionId {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, id := range r.order {
		def := r.defs[id]
		if !contextMatches(def.Context, context) {
			continue
		}
		for _, k := range def.DefaultKeys {
			if terminal.MatchesKey(data, k) {
				return def.ID
			}
		}
		for _, k := range def.AltKeys {
			if terminal.MatchesKey(data, k) {
				return def.ID
			}
		}
	}
	return ""
}

// Matches 检查 data 是否匹配指定 ActionId。
func (r *ActionRegistry) Matches(data string, id ActionId) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.defs[id]
	if !ok {
		return false
	}
	for _, k := range def.DefaultKeys {
		if terminal.MatchesKey(data, k) {
			return true
		}
	}
	for _, k := range def.AltKeys {
		if terminal.MatchesKey(data, k) {
			return true
		}
	}
	return false
}

// HintItem 是快捷键提示栏的一行。
type HintItem struct {
	KeyDisplay string // 展示用的键名（如 "Ctrl+Q"）
	Label      string // 动作标签
	ID         ActionId
}

// Hints 返回在给定上下文中生效的快捷键提示。
// 按 Category 顺序排列。
func (r *ActionRegistry) Hints(context When) []HintItem {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var hints []HintItem
	for _, id := range r.order {
		def := r.defs[id]
		if !contextMatches(def.Context, context) {
			continue
		}
		if len(def.DefaultKeys) == 0 {
			continue
		}
		hints = append(hints, HintItem{
			KeyDisplay: formatKeyDisplay(def.DefaultKeys[0]),
			Label:      def.Label,
			ID:         def.ID,
		})
	}
	return hints
}

// All 按注册顺序返回所有动作定义。
func (r *ActionRegistry) All() []ActionDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ActionDef, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.defs[id])
	}
	return out
}

// ByCategory 按分组返回动作定义。
func (r *ActionRegistry) ByCategory() map[Category][]ActionDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[Category][]ActionDef)
	for _, id := range r.order {
		def := r.defs[id]
		out[def.Category] = append(out[def.Category], def)
	}
	return out
}

// Definition 返回指定动作的定义。
func (r *ActionRegistry) Definition(id ActionId) (ActionDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.defs[id]
	return def, ok
}

// Categories 按顺序返回所有非空分组。
func (r *ActionRegistry) Categories() []Category {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[Category]bool)
	var out []Category
	for _, id := range r.order {
		cat := r.defs[id].Category
		if !seen[cat] {
			seen[cat] = true
			out = append(out, cat)
		}
	}
	return out
}

// SearchCheatsheet 返回所有匹配查询的动作（用于快捷键帮助面板的搜索）。
func (r *ActionRegistry) SearchCheatsheet(query string) []ActionDef {
	if query == "" {
		return r.All()
	}
	q := strings.ToLower(strings.TrimSpace(query))
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []ActionDef
	for _, id := range r.order {
		def := r.defs[id]
		if strings.Contains(strings.ToLower(def.Label), q) ||
			strings.Contains(strings.ToLower(def.Description), q) ||
			strings.Contains(strings.ToLower(string(def.ID)), q) ||
			strings.Contains(strings.ToLower(def.LongHelp), q) {
			out = append(out, def)
		}
	}
	return out
}

// --- helpers ---

// contextMatches 检查动作上下文是否在当前上下文中生效。
// Always 的动作在所有上下文生效；其他上下文精确匹配。
func contextMatches(actionCtx, currentCtx When) bool {
	if actionCtx == WhenAlways {
		return true
	}
	return actionCtx == currentCtx
}

// formatKeyDisplay 将 KeyID 转为展示用格式。
// "ctrl+q" → "Ctrl+Q", "ctrl+/" → "Ctrl+/", "enter" → "Enter"
func formatKeyDisplay(keyID terminal.KeyID) string {
	parts := strings.Split(keyID, "+")
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) == 1 {
			parts[i] = strings.ToUpper(p)
		} else if len(p) > 1 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		} else {
			parts[i] = p
		}
	}
	return strings.Join(parts, "+")
}

// DefaultActions 返回 covo-agent 的默认快捷键集合。
// 根据 terminalCtx 做终端自适应（如 VS Code family 用 Ctrl+D 退出）。
func DefaultActions(terminalCtx *TerminalContext) []ActionDef {
	// 终端自适应：VS Code family 用 Ctrl+D 退出
	quitKeys := []terminal.KeyID{"ctrl+q", "ctrl+d"}
	if terminalCtx != nil && terminalCtx.IsVSCodeFamily() {
		quitKeys = []terminal.KeyID{"ctrl+d", "ctrl+q"}
	}

	helpKey := "ctrl+/"
	if terminalCtx != nil && terminalCtx.CtrlDotUnreliable() {
		helpKey = "ctrl+x"
	}

	return []ActionDef{
		// Essentials
		{
			ID:          ActionQuit,
			Label:       "quit",
			Description: "Quit application (press twice to confirm)",
			DefaultKeys: quitKeys,
			Category:    CatEssentials,
			Context:     WhenAlways,
			LongHelp:    "Press Ctrl+Q (or Ctrl+D in VS Code) twice within 3 seconds to quit. The first press shows a confirmation message.",
		},
		{
			ID:          ActionHelp,
			Label:       "help",
			Description: "Show keyboard shortcuts cheatsheet",
			DefaultKeys: []terminal.KeyID{helpKey},
			Category:    CatEssentials,
			Context:     WhenAlways,
			LongHelp:    "Opens a searchable list of all keyboard shortcuts grouped by category.",
		},
		// 注意：ActionInterrupt (Ctrl+C) 不在 DefaultActions 中。
		// HotkeyRouter 不应拦截 Ctrl+C——它由 editor/chat 层消费（copy/interrupt）。
		// 如果在此注册，dispatchViaRegistry 会匹配并返回 true，
		// 导致 HotkeyRouter 吞掉 Ctrl+C 但不做任何事，editor 收不到。

		// Panels
		{
			ID:          ActionOpenSessions,
			Label:       "sessions",
			Description: "Open session picker",
			DefaultKeys: []terminal.KeyID{"ctrl+o"},
			Category:    CatPanels,
			Context:     WhenAgentScreen,
		},
		{
			ID:          ActionOpenTodos,
			Label:       "todos",
			Description: "Toggle TODO panel",
			DefaultKeys: []terminal.KeyID{"ctrl+t"},
			Category:    CatPanels,
			Context:     WhenAgentScreen,
		},
		{
			ID:          ActionOpenCommandPalette,
			Label:       "palette",
			Description: i18n.T("keybinding.palette"),
			DefaultKeys: []terminal.KeyID{"ctrl+k"},
			Category:    CatPanels,
			Context:     WhenAgentScreen,
		},
		{
			ID:          ActionOpenHistorySearch,
			Label:       "search",
			Description: i18n.T("keybinding.search"),
			DefaultKeys: []terminal.KeyID{"ctrl+s"},
			Category:    CatPanels,
			Context:     WhenAgentScreen,
		},
		{
			ID:          ActionOpenSkillCenter,
			Label:       "skills",
			Description: "Open skill center",
			Category:    CatPanels,
			Context:     WhenAgentScreen,
		},
		{
			ID:          ActionOpenDashboard,
			Label:       "dashboard",
			Description: i18n.T("commands.dashboard"),
			Category:    CatPanels,
			Context:     WhenAgentScreen,
		},
		{
			ID:          ActionOpenModelPicker,
			Label:       "model",
			Description: "Open model picker",
			DefaultKeys: []terminal.KeyID{"ctrl+p"},
			Category:    CatPanels,
			Context:     WhenAgentScreen,
		},
		{
			ID:          ActionOpenEditor,
			Label:       "editor",
			Description: "Open external editor",
			DefaultKeys: []terminal.KeyID{"ctrl+e"},
			Category:    CatPanels,
			Context:     WhenAgentScreen,
		},
		{
			ID:          ActionOpenSessionTree,
			Label:       "tree",
			Description: i18n.T("keybinding.session_tree"),
			DefaultKeys: []terminal.KeyID{"ctrl+y"},
			Category:    CatPanels,
			Context:     WhenAgentScreen,
		},
		{
			ID:          ActionOpenChangedFiles,
			Label:       "files",
			Description: i18n.T("keybinding.changed_files"),
			DefaultKeys: []terminal.KeyID{"ctrl+g"},
			Category:    CatPanels,
			Context:     WhenAgentScreen,
		},

		// Input
		{
			ID:          ActionSubmitPrompt,
			Label:       "send",
			Description: "Submit prompt to agent",
			DefaultKeys: []terminal.KeyID{"enter"},
			Category:    CatInput,
			Context:     WhenPromptFocused,
		},
		{
			ID:          ActionNewLine,
			Label:       "newline",
			Description: "Insert newline in prompt",
			DefaultKeys: []terminal.KeyID{"shift+enter", "alt+enter"},
			Category:    CatInput,
			Context:     WhenPromptFocused,
		},
		{
			ID:          ActionTabComplete,
			Label:       "complete",
			Description: "Autocomplete slash commands and @ references",
			DefaultKeys: []terminal.KeyID{"tab"},
			Category:    CatInput,
			Context:     WhenPromptFocused,
		},
	}
}

// RenderCheatsheet 生成快捷键帮助面板的纯文本行。
// 用于 ShowPanel 展示。
func (r *ActionRegistry) RenderCheatsheet(query string) []string {
	defs := r.SearchCheatsheet(query)

	// 按分组组织
	byCat := make(map[Category][]ActionDef)
	var cats []Category
	for _, d := range defs {
		if _, ok := byCat[d.Category]; !ok {
			cats = append(cats, d.Category)
		}
		byCat[d.Category] = append(byCat[d.Category], d)
	}
	sort.Slice(cats, func(i, j int) bool { return cats[i] < cats[j] })

	var lines []string
	for _, cat := range cats {
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("  ── %s ──", cat.String()))
		for _, def := range byCat[cat] {
			if len(def.DefaultKeys) == 0 {
				continue
			}
			keyStr := formatKeyDisplay(def.DefaultKeys[0])
			if len(def.DefaultKeys) > 1 || len(def.AltKeys) > 0 {
				allKeys := make([]string, 0, len(def.DefaultKeys)+len(def.AltKeys))
				for _, k := range def.DefaultKeys {
					allKeys = append(allKeys, formatKeyDisplay(k))
				}
				for _, k := range def.AltKeys {
					allKeys = append(allKeys, formatKeyDisplay(k))
				}
				keyStr = strings.Join(allKeys, " / ")
			}
			lines = append(lines, fmt.Sprintf("  %-14s  %s", keyStr, def.Description))
		}
	}
	return lines
}
