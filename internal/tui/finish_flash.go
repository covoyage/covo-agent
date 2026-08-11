package tui

import (
	"sync"
	"time"

	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// Finish Flash — 工具完成时的 accent 高亮闪烁动画。
//
// 追踪最近完成的 entry，并在短时间内使用 accent 高亮。
// ---------------------------------------------------------------------------

// FinishFlashDuration 闪烁持续时间。
const FinishFlashDuration = 800 * time.Millisecond

// FinishFlashTracker 追踪最近完成的 entry 闪烁状态。
type FinishFlashTracker struct {
	mu       sync.Mutex
	flashing map[EntryID]time.Time // entry ID → 完成时间
}

// NewFinishFlashTracker 创建追踪器。
func NewFinishFlashTracker() *FinishFlashTracker {
	return &FinishFlashTracker{
		flashing: make(map[EntryID]time.Time),
	}
}

// OnFinish 在 entry 完成时调用，记录闪烁开始时间。
func (t *FinishFlashTracker) OnFinish(entryID EntryID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flashing[entryID] = time.Now()
}

// IsFlashing 检查 entry 是否正在闪烁。
func (t *FinishFlashTracker) IsFlashing(entryID EntryID) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	startTime, exists := t.flashing[entryID]
	if !exists {
		return false
	}
	if time.Since(startTime) > FinishFlashDuration {
		delete(t.flashing, entryID)
		return false
	}
	return true
}

// Tick 清理过期的闪烁记录。应在渲染 tick 中调用。
func (t *FinishFlashTracker) Tick() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for id, startTime := range t.flashing {
		if now.Sub(startTime) > FinishFlashDuration {
			delete(t.flashing, id)
		}
	}
}

// FlashIntensity 返回闪烁强度 [0.0, 1.0]。
// 1.0 = 刚完成，0.0 = 闪烁结束。
func (t *FinishFlashTracker) FlashIntensity(entryID EntryID) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	startTime, exists := t.flashing[entryID]
	if !exists {
		return 0
	}
	elapsed := time.Since(startTime)
	if elapsed > FinishFlashDuration {
		delete(t.flashing, entryID)
		return 0
	}
	return 1.0 - float64(elapsed)/float64(FinishFlashDuration)
}

// RenderFlashAccent 渲染带闪烁效果的 accent line。
// 如果 entry 正在闪烁，用更亮的颜色渲染。
func (t *FinishFlashTracker) RenderFlashAccent(entry *ScrollbackEntry, pal *theme.Palette) string {
	if t.IsFlashing(entry.ID) {
		// 闪烁时用 accent 高亮
		return pal.Accent.Render("┃")
	}
	return renderAccentLine(entry, pal)
}
