package tui

import (
	"strings"
	"sync"

	"github.com/covoyage/covonaut/agentcore"
)

// ---------------------------------------------------------------------------
// ScrollbackBridge — 将 agentcore 事件镜像到 ScrollbackPipeline。
//
// agentcore.Agent 发出的事件（AgentStart、ToolCallStart、MessageDelta 等）
// 同时被 ChatApp（通过 agentadapter.BindAgent）和本桥接器消费。
// 桥接器把事件转为类型化 Block 写入 ScrollbackPipeline，
// 使搜索、文本选择、turn 导航等组件可以基于 pipeline 工作。
//
// 使用方式：
//
//	bridge := NewScrollbackBridge()
//	bridge.Bind(agentCore)
//	// bridge.Pipeline() 随时可用
// ---------------------------------------------------------------------------

// ScrollbackBridge 将 agent 事件桥接到 ScrollbackPipeline。
type ScrollbackBridge struct {
	mu       sync.Mutex
	pipeline *ScrollbackPipeline

	// 流式消息追踪
	streamingEntry *ScrollbackEntry // 当前正在流式追加的 AgentMessageBlock
	streamingText  strings.Builder

	// 工具调用追踪
	toolEntries map[string]*ScrollbackEntry // toolCallID → entry

	// Finish flash 追踪
	flashTracker *FinishFlashTracker
}

// NewScrollbackBridge 创建桥接器。
func NewScrollbackBridge() *ScrollbackBridge {
	return &ScrollbackBridge{
		pipeline:     NewScrollbackPipeline(),
		toolEntries:  make(map[string]*ScrollbackEntry),
		flashTracker: NewFinishFlashTracker(),
	}
}

// Pipeline 返回底层 ScrollbackPipeline。
func (b *ScrollbackBridge) Pipeline() *ScrollbackPipeline {
	return b.pipeline
}

// Bind 将桥接器绑定到 agentcore.Agent，订阅所有相关事件。
func (b *ScrollbackBridge) Bind(core *agentcore.Agent) {
	if core == nil {
		return
	}

	core.On(agentcore.EventAgentStart, func(event agentcore.Event) {
		if e, ok := event.(*agentcore.AgentStartEvent); ok {
			b.mu.Lock()
			b.streamingText.Reset()
			b.streamingEntry = nil
			b.mu.Unlock()

			if strings.TrimSpace(e.Input) != "" {
				b.pipeline.Append(&UserPromptBlock{Text: e.Input})
			}
		}
	})

	core.On(agentcore.EventMessageDelta, func(event agentcore.Event) {
		if e, ok := event.(*agentcore.MessageDeltaEvent); ok {
			b.mu.Lock()
			defer b.mu.Unlock()

			if e.Kind == agentcore.BlockKindThinking {
				b.pipeline.Append(&ThinkingBlock{Text: e.Delta})
				return
			}

			// 文本 delta：追加到当前流式消息
			b.streamingText.WriteString(e.Delta)
			if b.streamingEntry == nil {
				b.streamingEntry = b.pipeline.Append(&AgentMessageBlock{
					Text: b.streamingText.String(),
				})
			} else {
				if block, ok := b.streamingEntry.Block.(*AgentMessageBlock); ok {
					block.Text = b.streamingText.String()
				}
			}
		}
	})

	core.On(agentcore.EventToolCallStart, func(event agentcore.Event) {
		if e, ok := event.(*agentcore.ToolCallStartEvent); ok {
			b.mu.Lock()
			defer b.mu.Unlock()

			// finalize 流式消息
			b.streamingEntry = nil
			b.streamingText.Reset()

			// 使用工具特化 block 工厂
			block := NewToolBlock(e.ToolCall.Name, e.ToolCall.Arguments)
			entry := b.pipeline.Append(block)
			b.pipeline.StartRunning(entry)
			b.toolEntries[e.ToolCall.ID] = entry
		}
	})

	core.On(agentcore.EventToolCallEnd, func(event agentcore.Event) {
		if e, ok := event.(*agentcore.ToolCallEndEvent); ok {
			b.mu.Lock()
			defer b.mu.Unlock()

			entry, exists := b.toolEntries[e.ToolCallID]
			if !exists {
				return
			}
			delete(b.toolEntries, e.ToolCallID)

			// 更新工具特化 block 的结果
			updateToolResult(entry.Block, e.Result, e.Err)
			b.pipeline.FinishRunning(entry)
			if b.flashTracker != nil {
				b.flashTracker.OnFinish(entry.ID)
			}
		}
	})

	core.On(agentcore.EventAgentEnd, func(event agentcore.Event) {
		b.mu.Lock()
		b.streamingEntry = nil
		b.streamingText.Reset()
		b.mu.Unlock()
	})

	core.On(agentcore.EventAgentError, func(event agentcore.Event) {
		if e, ok := event.(*agentcore.AgentErrorEvent); ok {
			b.mu.Lock()
			b.streamingEntry = nil
			b.streamingText.Reset()
			b.mu.Unlock()

			b.pipeline.Append(&ErrorBlock{Text: e.Err.Error()})
		}
	})

	core.On(agentcore.EventHandoffStart, func(event agentcore.Event) {
		if e, ok := event.(*agentcore.HandoffStartEvent); ok {
			b.pipeline.Append(&SubagentBlock{
				AgentName: e.TargetAgent,
				KindExt:   SubagentKindHandoff,
			})
		}
	})

	core.On(agentcore.EventCompactionStart, func(event agentcore.Event) {
		if _, ok := event.(*agentcore.CompactionStartEvent); ok {
			b.pipeline.Append(&SystemBlock{
				Text: "compacting context",
			})
		}
	})

	core.On(agentcore.EventCompactionEnd, func(event agentcore.Event) {
		if _, ok := event.(*agentcore.CompactionEndEvent); ok {
			b.pipeline.Append(&SystemBlock{
				Text: "compacted",
			})
		}
	})
}

// RestoreFromMessages 从消息列表重建 pipeline（用于 session resume）。
func (b *ScrollbackBridge) RestoreFromMessages(messages []agentcore.Message) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.pipeline.Clear()
	for _, msg := range messages {
		block := FromAgentMessage(msg)
		if block != nil {
			b.pipeline.Append(block)
		}
	}
}

// FlashTracker 返回 finish flash 追踪器。
func (b *ScrollbackBridge) FlashTracker() *FinishFlashTracker {
	return b.flashTracker
}

// updateToolResult 更新工具特化 block 的结果字段。
func updateToolResult(block Block, result string, err error) {
	switch b := block.(type) {
	case *ToolCallBlock:
		b.Result = result
		if err != nil {
			b.Error = err.Error()
		}
	case *EditToolBlock:
		b.Result = result
		if err != nil {
			b.Error = err.Error()
		}
	case *ExecuteToolBlock:
		b.Output = result
		if err != nil {
			b.Error = err.Error()
		}
	case *ReadToolBlock:
		if err != nil {
			b.Error = err.Error()
		}
	case *SearchToolBlock:
		if err != nil {
			b.Error = err.Error()
		}
	}
}
