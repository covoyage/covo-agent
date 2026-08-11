package tui

// ---------------------------------------------------------------------------
// 水平布局系统。
//
// 定义所有 scrollback entry 共享的列结构：
//
//   │A│PL│    Content    │PR│
//   │1│ 2│     flex      │ 1│
//
// Where:
//   A  = Accent line (1 char)
//   PL = Left padding (configurable, default 2)
//   Content = Flexible width
//   PR = Right padding (configurable, default 1)
//
// 注：covo-agent 目前用 covonaut/tui 的渲染层，不直接控制 Buffer 布局。
// 此模块为未来渲染层迁移准备——提供布局计算 API。
// ---------------------------------------------------------------------------

// LayoutConfig 控制水平布局参数。
type LayoutConfig struct {
	BlockPadLeft  int // 左 padding（默认 2）
	BlockPadRight int // 右 padding（默认 1）
}

// DefaultLayoutConfig 返回默认布局配置。
func DefaultLayoutConfig() LayoutConfig {
	return LayoutConfig{BlockPadLeft: 2, BlockPadRight: 1}
}

// HorizontalLayout 描述 entry 的水平列布局。
type HorizontalLayout struct {
	Accent       int // accent 列宽度（始终 1）
	LeftPadding  int // 左 padding 宽度
	Content      int // 内容区宽度（flex）
	RightPadding int // 右 padding 宽度
}

// AccentWidth accent 列宽度，始终为 1。
const AccentWidth = 1

// NewHorizontalLayout 为给定总宽度和配置计算布局。
func NewHorizontalLayout(totalWidth int, config LayoutConfig) HorizontalLayout {
	chrome := AccentWidth + config.BlockPadLeft + config.BlockPadRight
	content := totalWidth - chrome
	if content < 1 {
		content = 1
	}
	return HorizontalLayout{
		Accent:       AccentWidth,
		LeftPadding:  config.BlockPadLeft,
		Content:       content,
		RightPadding: config.BlockPadRight,
	}
}

// ChromeWidth 返回给定配置下的 chrome 总宽度。
func ChromeWidth(config LayoutConfig) int {
	return AccentWidth + config.BlockPadLeft + config.BlockPadRight
}

// ContentStart 返回内容区的起始列号（0-based）。
func (h HorizontalLayout) ContentStart() int {
	return h.Accent + h.LeftPadding
}

// ContentEnd 返回内容区的结束列号（0-based，不含）。
func (h HorizontalLayout) ContentEnd() int {
	return h.ContentStart() + h.Content
}
