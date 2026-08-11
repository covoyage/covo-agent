package tui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// Picker — 通用列表选择器框架。
//
// 消除 ModelPicker / SessionTree / ChangedFiles / MCPMarketplace / ApprovalPicker
// 重复实现的列表/选择/分页/搜索逻辑。
//
// 使用方式：
//
//	picker := NewPicker(PickerConfig{
//	    Title:    "Select Provider",
//	    PageSize: 15,
//	    Searchable: true,
//	})
//	picker.SetItems([]PickerItem{...})
//	picker.OnSelect(func(item PickerItem) { ... })
//	picker.OnCancel(func() { ... })
//	uibus.ShowPanel(picker, 80, 80)
//
// 特性：
//   - ↑↓ / Ctrl+P/N 导航
//   - / 进入搜索模式，输入过滤，Enter 确认，Esc 退出搜索
//   - PgUp/PgDn 翻页
//   - 自动滚动偏移调整
//   - 行渲染：标签列对齐 + 描述截断 + 选中高亮
//   - 线程安全（sync.RWMutex）
// ---------------------------------------------------------------------------

// PickerItem 描述选择列表中的一条。
type PickerItem struct {
	Value       string // 逻辑值（如 provider type "openai"）
	Label       string // 展示标签
	Description string // 展示描述
	Tag         string // 可选标签后缀（如 "new"、"configured"）
	Selected    bool   // 是否标记为已选中（radio 风格）
	Category    string // 可选分组标题
}

// PickerConfig 配置选择器行为。
type PickerConfig struct {
	Title     string
	PageSize int
	// Searchable 为 true 时支持 / 进入搜索模式
	Searchable bool
	// MultiSelect 为 true 时允许空格多选（radio 风格）
	MultiSelect bool
	// ShowCount 为 true 时显示 "X of Y" 计数
	ShowCount bool
	// Hint 行的提示文本（覆盖默认）
	Hint string
}

// PickerOutcome 描述选择器的操作结果。
type PickerOutcome int

const (
	PickerOutcomeNone     PickerOutcome = iota
	PickerOutcomeSelect   // 选中某项（Enter）
	PickerOutcomeCancel   // 取消（Esc）
	PickerOutcomeSearch   // 切换搜索模式（/）
)

// Picker 是通用列表选择器。
type Picker struct {
	mu sync.RWMutex

	config PickerConfig
	km     *terminal.KeybindingsManager

	items    []PickerItem
	filtered []PickerItem

	selected int
	offset   int

	searching bool
	search    string

	// 回调
	onSelect  func(PickerItem)
	onCancel  func()
	onToggle  func(PickerItem) // MultiSelect 模式下空格切换

	// 光标闪烁
	cursorVisible    bool
	cursorTickActive bool
}

// NewPicker 构造一个选择器。
func NewPicker(config PickerConfig) *Picker {
	if config.PageSize <= 0 {
		config.PageSize = 15
	}
	return &Picker{
		config: config,
		km: terminal.NewKeybindingsManager(map[string]terminal.KeybindingDef{
			"picker.up":      {DefaultKeys: []terminal.KeyID{"up", "ctrl+p"}},
			"picker.down":    {DefaultKeys: []terminal.KeyID{"down", "ctrl+n"}},
			"picker.confirm": {DefaultKeys: []terminal.KeyID{"enter"}},
			"picker.cancel":  {DefaultKeys: []terminal.KeyID{"escape"}},
			"picker.pageUp":  {DefaultKeys: []terminal.KeyID{"pageUp"}},
			"picker.pageDn":  {DefaultKeys: []terminal.KeyID{"pageDown"}},
			"picker.toggle":  {DefaultKeys: []terminal.KeyID{" "}},
			"picker.search":  {DefaultKeys: []terminal.KeyID{"/"}},
			"picker.backspace": {DefaultKeys: []terminal.KeyID{"backspace", "ctrl+h"}},
		}),
	}
}

// SetItems 替换全部项目。
func (p *Picker) SetItems(items []PickerItem) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.items = make([]PickerItem, len(items))
	copy(p.items, items)
	p.applyFilterLocked()
	if p.selected >= len(p.filtered) {
		p.selected = 0
		p.offset = 0
	}
}

// Items 返回当前（过滤后的）可见项目。
func (p *Picker) Items() []PickerItem {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]PickerItem, len(p.filtered))
	copy(out, p.filtered)
	return out
}

// SelectedItem 返回当前选中项（无选中返回零值）。
func (p *Picker) SelectedItem() PickerItem {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.selected < 0 || p.selected >= len(p.filtered) {
		return PickerItem{}
	}
	return p.filtered[p.selected]
}

// SelectedIndex 返回当前选中索引。
func (p *Picker) SelectedIndex() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.selected
}

// SetSelected 设置选中索引（用于恢复状态）。
func (p *Picker) SetSelected(idx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx >= 0 && idx < len(p.filtered) {
		p.selected = idx
		p.adjustOffsetLocked()
	}
}

// OnSelect 设置选中回调。
func (p *Picker) OnSelect(fn func(PickerItem)) { p.onSelect = fn }

// OnCancel 设置取消回调。
func (p *Picker) OnCancel(fn func()) { p.onCancel = fn }

// OnToggle 设置多选切换回调。
func (p *Picker) OnToggle(fn func(PickerItem)) { p.onToggle = fn }

// Invalidate 实现 core.Component。
func (p *Picker) Invalidate() {}

// Update 实现 core.Updatable。
func (p *Picker) Update(msg core.Msg) core.Cmd {
	switch m := msg.(type) {
	case core.KeyMsg:
		return p.handleKey(m.Data)
	case core.PasteMsg:
		if p.searching {
			p.handleSearchPaste(m.Text)
		}
	case cursorTickMsg:
		return p.handleCursorTick()
	}
	return nil
}

// Render 实现 core.Component。
func (p *Picker) Render(width int64) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	pal := theme.CurrentPalette()

	if width < 48 {
		width = 48
	}
	panelWidth := width - 8
	if panelWidth > 96 {
		panelWidth = 96
	}
	if panelWidth < 48 {
		panelWidth = width
	}
	innerWidth := panelWidth - 4

	var body []string
	body = append(body, pal.Accent.Render(p.config.Title))

	// 搜索/提示行
	if p.searching {
		body = append(body, pal.Accent.Render(fmt.Sprintf("Search: %s▎  enter confirm  esc cancel", p.search)))
	} else if p.search != "" {
		body = append(body, pal.Dim.Render(fmt.Sprintf("Filter: %s  / refine  esc clear  PgUp/PgDn", p.search)))
	} else if p.config.Hint != "" {
		body = append(body, pal.Dim.Render(p.config.Hint))
	} else {
		body = append(body, pal.Dim.Render("↑↓ move  Enter select  / filter  PgUp/PgDn  Esc cancel"))
	}

	if p.config.ShowCount {
		body = append(body, pal.Dim.Render(fmt.Sprintf("%d items", len(p.filtered))))
	}

	if len(p.filtered) == 0 {
		body = append(body, "")
		body = append(body, pal.Dim.Render("  No matching items."))
	} else {
		body = append(body, "")
		// 分页
		total := len(p.filtered)
		start := p.offset
		end := start + p.config.PageSize
		if end > total {
			end = total
		}
		if total > p.config.PageSize {
			page := p.offset/p.config.PageSize + 1
			pages := (total-1)/p.config.PageSize + 1
			body = append(body, pal.Dim.Render(fmt.Sprintf("  Page %d/%d", page, pages)))
			body = append(body, "")
		}

		lastCat := ""
		for i := start; i < end; i++ {
			item := p.filtered[i]
			// 分组标题
			if item.Category != "" && item.Category != lastCat {
				lastCat = item.Category
				body = append(body, pal.Dim.Render(" ── "+item.Category+" ──"))
			}
			body = append(body, p.renderItemLocked(item, i == p.selected, innerWidth, pal))
		}
	}

	panel := renderPickerPanel(body, panelWidth)
	return centerPanelLines(panel, width)
}

// --- internal ---

type cursorTickMsg struct {
	core.MsgBase
}

func cursorTickCmd() core.Cmd {
	return func() core.Msg {
		return cursorTickMsg{}
	}
}

func (p *Picker) handleCursorTick() core.Cmd {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.searching {
		p.cursorTickActive = false
		return nil
	}
	p.cursorVisible = !p.cursorVisible
	return cursorTickCmd()
}

func (p *Picker) handleKey(data string) core.Cmd {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 搜索模式下捕获可打印字符
	if p.searching {
		for _, key := range terminal.ParseKeys(data) {
			if key.IsRelease() {
				continue
			}
			switch {
			case key.Name == "enter":
				p.searching = false
				p.selected = 0
				p.offset = 0
				p.applyFilterLocked()
			case key.Name == "escape":
				p.searching = false
				p.search = ""
				p.selected = 0
				p.offset = 0
				p.applyFilterLocked()
			case key.Name == "backspace":
				if len(p.search) > 0 {
					p.search = p.search[:len(p.search)-1]
					p.selected = 0
					p.offset = 0
					p.applyFilterLocked()
				}
			default:
				if key.IsPrintable() {
					p.search += string(key.Rune)
					p.selected = 0
					p.offset = 0
					p.applyFilterLocked()
				}
			}
		}
		return nil
	}

	// 非搜索模式：常规导航
	// 使用 km.Matches(data, ...) 直接匹配原始数据（KeybindingsManager 内部会解析键序列）
	switch {
	case p.km.Matches(data, "picker.up"):
		p.moveLocked(-1)
	case p.km.Matches(data, "picker.down"):
		p.moveLocked(1)
	case p.km.Matches(data, "picker.pageUp"):
		p.pageLocked(-1)
	case p.km.Matches(data, "picker.pageDn"):
		p.pageLocked(1)
	case p.km.Matches(data, "picker.confirm"):
		if len(p.filtered) > 0 && p.onSelect != nil {
			item := p.filtered[p.selected]
			p.mu.Unlock()
			p.onSelect(item)
			p.mu.Lock()
		}
	case p.km.Matches(data, "picker.cancel"):
		if p.onCancel != nil {
			p.mu.Unlock()
			p.onCancel()
			p.mu.Lock()
		}
	case p.km.Matches(data, "picker.toggle"):
		if p.config.MultiSelect && len(p.filtered) > 0 && p.onToggle != nil {
			item := p.filtered[p.selected]
			p.mu.Unlock()
			p.onToggle(item)
			p.mu.Lock()
		}
	case p.km.Matches(data, "picker.search"):
		if p.config.Searchable {
			p.searching = true
			p.search = ""
		}
	}
	return nil
}

func (p *Picker) handleSearchPaste(text string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.searching {
		p.search += text
		p.selected = 0
		p.offset = 0
		p.applyFilterLocked()
	}
}

func (p *Picker) moveLocked(delta int) {
	n := len(p.filtered)
	if n == 0 {
		return
	}
	p.selected = wrapIndex(p.selected+delta, n)
	p.adjustOffsetLocked()
}

func (p *Picker) pageLocked(dir int) {
	n := len(p.filtered)
	if n == 0 {
		return
	}
	p.offset += dir * p.config.PageSize
	if p.offset < 0 {
		p.offset = 0
	}
	maxOff := ((n - 1) / p.config.PageSize) * p.config.PageSize
	if p.offset > maxOff {
		p.offset = maxOff
	}
	if p.selected < p.offset || p.selected >= p.offset+p.config.PageSize {
		p.selected = p.offset
	}
}

func (p *Picker) adjustOffsetLocked() {
	n := len(p.filtered)
	if n <= p.config.PageSize {
		p.offset = 0
		return
	}
	if p.selected < p.offset {
		p.offset = p.selected
	}
	if p.selected >= p.offset+p.config.PageSize {
		p.offset = p.selected - p.config.PageSize + 1
	}
}

func (p *Picker) applyFilterLocked() {
	if p.search == "" {
		p.filtered = make([]PickerItem, len(p.items))
		copy(p.filtered, p.items)
		return
	}
	q := strings.ToLower(p.search)
	var out []PickerItem
	for _, item := range p.items {
		if strings.Contains(strings.ToLower(item.Label), q) ||
			strings.Contains(strings.ToLower(item.Value), q) ||
			strings.Contains(strings.ToLower(item.Description), q) {
			out = append(out, item)
		}
	}
	p.filtered = out
}

func (p *Picker) renderItemLocked(item PickerItem, selected bool, width int64, pal *theme.Palette) string {
	// radio / checkmark
	mark := "  "
	if item.Selected {
		mark = pal.Success.Render("● ")
	} else {
		mark = pal.Dim.Render("○ ")
	}

	label := item.Label
	if item.Tag != "" {
		label += " [" + item.Tag + "]"
	}
	label = core.TruncateToWidth(label, width-6, "…")

	prefix := "  "
	markStyle := pal.Dim
	labelStyle := pal.Assistant

	if item.Selected {
		markStyle = pal.Success
		labelStyle = pal.Assistant
	}

	if selected {
		prefix = pal.SelectHighlight.Render("→ ")
		markStyle = pal.SelectHighlight
		labelStyle = pal.SelectHighlight
	}

	return prefix + markStyle.Render(mark) + labelStyle.Render(label)
}

// 确保实现接口
var _ core.Component = (*Picker)(nil)
var _ core.Updatable = (*Picker)(nil)
