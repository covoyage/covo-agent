package tui

import (
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// 文本选择 + 复制。
//
// 应用级文本选择，支持鼠标拖选跨越多个 block entry。
// 与终端原生选择不同，应用级选择可以精确到 span 级别，
// 并支持 OSC 8 超链接的选择。
//
// 架构：
//   - SelectionRange：选中区域 [startEntry, startLine, startCol] → [endEntry, endLine, endCol]
//   - TextSelection：管理拖选状态，计算选中文本
//   - AutoScrollDirection：拖选到边缘时自动滚动
// ---------------------------------------------------------------------------

// SelectionAnchor 标记选择的一个端点。
type SelectionAnchor struct {
	EntryID EntryID
	LineIdx int // 在 entry 渲染行中的索引
	Col     int // 列号（字符宽度）
}

// SelectionRange 描述一个选择区间。
type SelectionRange struct {
	Start SelectionAnchor
	End   SelectionAnchor
}

// AutoScrollDirection 拖选自动滚动方向。
type AutoScrollDirection int

const (
	AutoScrollNone AutoScrollDirection = iota
	AutoScrollUp
	AutoScrollDown
)

// DragAutoScrollState 拖选边缘自动滚动状态。
type DragAutoScrollState struct {
	Direction AutoScrollDirection
	Speed     int // 每帧滚动行数
}

// EdgeThreshold 边缘检测行数。
const edgeThreshold = 2

// TextSelection 管理应用级文本选择。
type TextSelection struct {
	mu       sync.Mutex
	active   bool
	start    SelectionAnchor
	end      SelectionAnchor
	dragging bool
}

// NewTextSelection 创建选择管理器。
func NewTextSelection() *TextSelection {
	return &TextSelection{}
}

// StartDrag 开始鼠标拖选。
func (ts *TextSelection) StartDrag(anchor SelectionAnchor) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.active = true
	ts.dragging = true
	ts.start = anchor
	ts.end = anchor
}

// UpdateDrag 更新拖选位置。
func (ts *TextSelection) UpdateDrag(anchor SelectionAnchor) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.dragging {
		ts.end = anchor
	}
}

// EndDrag 结束拖选（保持选中状态，但不再跟随鼠标）。
func (ts *TextSelection) EndDrag() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.dragging = false
}

// Clear 清除选择。
func (ts *TextSelection) Clear() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.active = false
	ts.dragging = false
}

// IsActive 返回是否有活跃选择。
func (ts *TextSelection) IsActive() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.active
}

// Range 返回当前选择区间（start 和 end 已按位置排序）。
func (ts *TextSelection) Range() (SelectionRange, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if !ts.active {
		return SelectionRange{}, false
	}
	// 确保 start <= end
	if compareAnchors(ts.start, ts.end) <= 0 {
		return SelectionRange{Start: ts.start, End: ts.end}, true
	}
	return SelectionRange{Start: ts.end, End: ts.start}, true
}

// ComputeAutoScroll 根据鼠标位置计算自动滚动方向和速度。
// mouseRow 是相对于 scrollback 内容区顶部的行号。
// contentHeight 是内容区高度。
func ComputeAutoScroll(mouseRow, contentHeight int) *DragAutoScrollState {
	if contentHeight == 0 {
		return nil
	}
	if mouseRow < edgeThreshold {
		distance := edgeThreshold - mouseRow
		return &DragAutoScrollState{
			Direction: AutoScrollUp,
			Speed:     1 + distance/2,
		}
	}
	if mouseRow >= contentHeight-edgeThreshold {
		distance := mouseRow - (contentHeight - edgeThreshold)
		return &DragAutoScrollState{
			Direction: AutoScrollDown,
			Speed:     1 + distance/2,
		}
	}
	return nil
}

// ExtractSelectedText 从 pipeline entries 中提取选中文本。
// 不依赖 RenderLines（避免 nil palette panic），直接用类型断言提取纯文本。
func ExtractSelectedText(p *ScrollbackPipeline, r SelectionRange) string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var sb strings.Builder
	inRange := false
	for _, entry := range p.entries {
		if entry.ID == r.Start.EntryID {
			inRange = true
		}
		if inRange {
			// 用 Summary() 作为安全文本源——所有 Block 都实现这个方法
			// 且不依赖 palette。
			lines := strings.Split(entry.Block.Summary(), "\n")
			startLine, endLine := 0, len(lines)-1
			if entry.ID == r.Start.EntryID {
				startLine = r.Start.LineIdx
			}
			if entry.ID == r.End.EntryID {
				endLine = r.End.LineIdx
			}
			for i := startLine; i <= endLine && i < len(lines); i++ {
				line := stripANSI(lines[i])
				if entry.ID == r.Start.EntryID && i == r.Start.LineIdx {
					if r.Start.Col < len(line) {
						line = line[r.Start.Col:]
					}
				}
				if entry.ID == r.End.EntryID && i == r.End.LineIdx {
					if r.End.Col < len(line) {
						line = line[:r.End.Col]
					}
				}
				sb.WriteString(line)
				sb.WriteByte('\n')
			}
		}
		if entry.ID == r.End.EntryID {
			break
		}
	}
	return sb.String()
}

// compareAnchors 比较两个 anchor 的位置。-1 < 0 < 1。
func compareAnchors(a, b SelectionAnchor) int {
	if a.EntryID < b.EntryID {
		return -1
	}
	if a.EntryID > b.EntryID {
		return 1
	}
	if a.LineIdx < b.LineIdx {
		return -1
	}
	if a.LineIdx > b.LineIdx {
		return 1
	}
	if a.Col < b.Col {
		return -1
	}
	if a.Col > b.Col {
		return 1
	}
	return 0
}

// stripANSI 移除 ANSI 转义序列。
func stripANSI(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// AutoScrollController — 拖选边缘自动滚动的定时器驱动。
//
// 当用户拖选到内容区顶部/底部边缘时，启动一个 ticker 定时滚动，
// 使选区可以跨越可视范围之外的行。鼠标移离边缘或结束拖选时停止。
// ---------------------------------------------------------------------------

// AutoScrollController 管理自动滚动的生命周期。
type AutoScrollController struct {
	mu        sync.Mutex
	ticker    *time.Ticker
	stopCh    chan struct{}
	scrollFn  func(direction AutoScrollDirection, speed int)
	direction AutoScrollDirection
	speed     int
}

// NewAutoScrollController 创建控制器。scrollFn 在每次 tick 时被调用，
// 参数为滚动方向和速度。
func NewAutoScrollController(scrollFn func(direction AutoScrollDirection, speed int)) *AutoScrollController {
	return &AutoScrollController{
		scrollFn: scrollFn,
	}
}

// Update 根据鼠标位置更新自动滚动状态。
// mouseRow 是相对于 scrollback 内容区顶部的行号，contentHeight 是内容区高度。
// 当鼠标在边缘阈值内时启动 ticker，否则停止。
func (asc *AutoScrollController) Update(mouseRow, contentHeight int) {
	state := ComputeAutoScroll(mouseRow, contentHeight)
	if state == nil {
		asc.Stop()
		return
	}
	asc.start(state.Direction, state.Speed)
}

// Stop 停止自动滚动。
func (asc *AutoScrollController) Stop() {
	asc.mu.Lock()
	defer asc.mu.Unlock()
	if asc.ticker != nil {
		asc.ticker.Stop()
		close(asc.stopCh)
		asc.ticker = nil
		asc.stopCh = nil
		asc.direction = AutoScrollNone
		asc.speed = 0
	}
}

func (asc *AutoScrollController) start(dir AutoScrollDirection, speed int) {
	asc.mu.Lock()
	defer asc.mu.Unlock()

	// Restart when the pointer crosses edges or changes distance from the edge.
	if asc.ticker != nil {
		if asc.direction == dir && asc.speed == speed {
			return
		}
		asc.ticker.Stop()
		close(asc.stopCh)
		asc.ticker = nil
		asc.stopCh = nil
	}

	// 速度越高 tick 间隔越短（范围 30ms ~ 100ms）
	interval := 100 - speed*15
	if interval < 30 {
		interval = 30
	}

	asc.ticker = time.NewTicker(time.Duration(interval) * time.Millisecond)
	asc.stopCh = make(chan struct{})
	asc.direction = dir
	asc.speed = speed
	ticker := asc.ticker
	stopCh := asc.stopCh

	go func() {
		for {
			select {
			case <-ticker.C:
				if asc.scrollFn != nil {
					asc.scrollFn(dir, speed)
				}
			case <-stopCh:
				return
			}
		}
	}()
}

// IsActive 返回自动滚动是否正在运行。
func (asc *AutoScrollController) IsActive() bool {
	asc.mu.Lock()
	defer asc.mu.Unlock()
	return asc.ticker != nil
}
