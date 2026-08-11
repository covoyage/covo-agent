package tui

// ---------------------------------------------------------------------------
// Sticky Header — iOS 风格粘性标题。
//
// 当用户滚动 scrollback 时，当前 turn 的 UserPrompt "粘"在顶部。
// 下一个 prompt 接近时推走前一个。
//
// 算法是纯计算的（1D 坐标数学），不涉及渲染逻辑：
//   - PromptDescriptor：每个 prompt 的虚拟坐标和高度
//   - computeStickyLayout：输入 prompt 描述符 + 滚动位置 → 输出需要渲染的粘性 prompt
// ---------------------------------------------------------------------------

// PromptDescriptor 描述一个 prompt entry 的粘性标题参数。
type PromptDescriptor struct {
	EntryIdx   int  // 在 entries 列表中的索引
	YVirtual   int  // 虚拟滚动空间中的 Y 位置
	FullHeight int  // 内联渲染时的总高度
	MinHeight  int  // 作为粘性标题时的最小高度
	Sticky     bool // 是否粘性
}

// MinPinnedHeight 粘性标题最小高度。
const MinPinnedHeight = 4

// RenderedPrompt 是计算后的粘性 prompt 渲染指令。
type RenderedPrompt struct {
	EntryIdx     int // prompt 的 entry 索引
	RenderHeight int // 渲染高度预算
	ClipTop      int // 顶部裁剪行数（0=不裁剪）
}

// StickyHeaderLayout 是粘性标题区域的计算结果。
type StickyHeaderLayout struct {
	Pinned  *RenderedPrompt // 当前粘住的 prompt（nil=无）
	Next    *RenderedPrompt // 即将进入视口的下一个 prompt（nil=无）
}

// ComputeStickyLayout 根据 prompt 描述符列表和当前滚动位置计算粘性布局。
//
// scrollOffset 是虚拟滚动空间的 Y 位置（0=最顶部）。
// viewportHeight 是可见区域高度。
func ComputeStickyLayout(prompts []PromptDescriptor, scrollOffset, viewportHeight int) StickyHeaderLayout {
	if len(prompts) == 0 {
		return StickyHeaderLayout{}
	}

	var pinned *RenderedPrompt
	var nextPrompt *RenderedPrompt

	// 找到当前应该粘住的 prompt：最后一个已滚出视口顶部的 sticky prompt
	for i, pd := range prompts {
		promptBottom := pd.YVirtual + pd.FullHeight
		// prompt 已完全滚出顶部
		if promptBottom <= scrollOffset && pd.Sticky {
			// 默认使用 MinHeight 作为粘性高度
			renderHeight := pd.MinHeight

			// 下一个 prompt 在视口内 → push 效果
			if i+1 < len(prompts) {
				nextPd := prompts[i+1]
				nextTop := nextPd.YVirtual
				distanceToNext := nextTop - scrollOffset
				if distanceToNext >= 0 && distanceToNext < viewportHeight {
					// push：减少 pinned 高度
					renderHeight = maxInt(pd.MinHeight, renderHeight-distanceToNext)
				}
			}

			if renderHeight > 0 {
				pinned = &RenderedPrompt{
					EntryIdx:     pd.EntryIdx,
					RenderHeight: renderHeight,
					ClipTop:      0,
				}
			}
		}

		// 找到即将进入视口的下一个 prompt
		if pd.YVirtual > scrollOffset && pd.YVirtual < scrollOffset+viewportHeight {
			nextPrompt = &RenderedPrompt{
				EntryIdx:     pd.EntryIdx,
				RenderHeight: pd.FullHeight,
				ClipTop:      0,
			}
		}
	}

	return StickyHeaderLayout{
		Pinned: pinned,
		Next:   nextPrompt,
	}
}

// CollectPromptDescriptors 从 ScrollbackPipeline 收集所有 UserPrompt entry 的描述符。
func (p *ScrollbackPipeline) CollectPromptDescriptors() []PromptDescriptor {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var descriptors []PromptDescriptor
	yVirtual := 0
	for i, entry := range p.entries {
		if entry.Kind == BlockKindUserPrompt {
			// 估算高度：Summary 行数 + padding
			summary := entry.Block.Summary()
			fullHeight := 3 // 保守估计：accent + summary + blank
			if len(summary) > 60 {
				fullHeight = 4
			}
			descriptors = append(descriptors, PromptDescriptor{
				EntryIdx:   i,
				YVirtual:   yVirtual,
				FullHeight: fullHeight,
				MinHeight:   MinPinnedHeight,
				Sticky:      true,
			})
		}
		// 估算每个 entry 的高度
		yVirtual += 3 // 保守估计
	}
	return descriptors
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
