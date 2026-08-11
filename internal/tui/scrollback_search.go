package tui

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// Scrollback 全文搜索。
//
// 架构：
//   - ScrollbackSearchIndex：缓存每个 entry 的 searchable_text，按 content_generation
//     增量同步——只有内容变化时重建缓存
//   - 后台 goroutine 跑 regex 扫描，UI 线程每次按键只需 O(1) 入队最新 query
//   - ScrollbackMatch：精确定位到 entry + line + byte range
//
// 使用方式：
//
//	idx := NewScrollbackSearchIndex()
//	idx.Sync(pipeline)
//	matches := idx.Find("hello world")
//
// 在 UI 线程上同步调用即可——长对话扫描通常 < 1ms。
// 超长对话可切换到异步模式（SetAsyncMode）。
// ---------------------------------------------------------------------------

// ScrollbackMatch 描述一次搜索匹配的位置。
type ScrollbackMatch struct {
	EntryID    EntryID
	LineIndex  int    // 在 entry 的 searchable_text 中的行索引
	ByteRange  [2]int // 匹配在 searchable_text 中的字节范围 [start, end)
}

// indexedEntry 缓存单个 entry 的纯文本。
type indexedEntry struct {
	id   EntryID
	text string
}

// ScrollbackSearchIndex 缓存 scrollback 文本的搜索索引。
type ScrollbackSearchIndex struct {
	mu             sync.RWMutex
	entries        []indexedEntry
	builtGeneration uint64 // 上次同步时的 content_generation

	// 异步搜索模式
	asyncMu      sync.Mutex
	asyncEnabled bool
	queryCh      chan string
	resultCh     chan []ScrollbackMatch
	asyncDone    chan struct{}
}

// NewScrollbackSearchIndex 创建空索引。
func NewScrollbackSearchIndex() *ScrollbackSearchIndex {
	return &ScrollbackSearchIndex{}
}

// Sync 当 scrollback 内容变化时重建缓存。
// 返回 true 表示发生了重建，false 表示无变化（跳过）。
func (idx *ScrollbackSearchIndex) Sync(p *ScrollbackPipeline) bool {
	p.mu.RLock()
	gen := p.contentGeneration
	entries := p.entries
	p.mu.RUnlock()

	idx.mu.Lock()
	defer idx.mu.Unlock()

	if gen == idx.builtGeneration {
		return false
	}

	idx.builtGeneration = gen
	idx.entries = make([]indexedEntry, 0, len(entries))
	for _, e := range entries {
		idx.entries = append(idx.entries, indexedEntry{
			id:   e.ID,
			text: searchableText(e),
		})
	}
	return true
}

// Find 在缓存中搜索匹配项。
// query 支持纯文本（大小写不敏感子串匹配）和 regex（以 / 开头）。
func (idx *ScrollbackSearchIndex) Find(query string) []ScrollbackMatch {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if query == "" {
		return nil
	}

	var re *regexp.Regexp
	isRegex := strings.HasPrefix(query, "/") && len(query) > 1
	if isRegex {
		pattern := query[1:]
		compiled, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			return nil
		}
		re = compiled
	} else {
		lower := strings.ToLower(query)
		re = regexp.MustCompile(regexp.QuoteMeta(lower))
	}

	var matches []ScrollbackMatch
	for _, ie := range idx.entries {
		lines := strings.Split(ie.text, "\n")
		for lineIdx, line := range lines {
			// 非regex：对 line 做 ToLower 实现 case-insensitive 子串匹配
			// regex：(?i) flag 已处理大小写，不对 line 做 ToLower（避免 \p{Lu} 失效）
			searchLine := line
			if !isRegex {
				searchLine = strings.ToLower(line)
			}
			locs := re.FindAllStringIndex(searchLine, -1)
			for _, loc := range locs {
				matches = append(matches, ScrollbackMatch{
					EntryID:   ie.id,
					LineIndex: lineIdx,
					ByteRange: [2]int{loc[0], loc[1]},
				})
			}
		}
	}
	return matches
}

// HasIndex 返回索引是否已构建。
func (idx *ScrollbackSearchIndex) HasIndex() bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.builtGeneration > 0
}

// Clear 重置索引。
func (idx *ScrollbackSearchIndex) Clear() {
	idx.mu.Lock()
	idx.entries = nil
	idx.builtGeneration = 0
	idx.mu.Unlock()
}

// ---------------------------------------------------------------------------
// 异步搜索模式
//
// 超长对话（数千 entry）时，同步 Find 可能阻塞 UI 线程。
// SetAsyncMode(true) 启动后台 goroutine，AsyncFind 通过 channel 通信。
// UI 线程每次按键只需 AsyncFind(query) 入队最新 query（O(1)），
// 后台 goroutine 执行 regex 扫描并通过 Results() channel 返回匹配。
// ---------------------------------------------------------------------------

// SetAsyncMode 启用或禁用异步搜索模式。
// 启用后，使用 AsyncFind 提交查询，Results 接收结果。
func (idx *ScrollbackSearchIndex) SetAsyncMode(enabled bool) {
	idx.asyncMu.Lock()
	defer idx.asyncMu.Unlock()

	if enabled == idx.asyncEnabled {
		return
	}

	if enabled {
		idx.queryCh = make(chan string, 1)
		idx.resultCh = make(chan []ScrollbackMatch, 1)
		idx.asyncDone = make(chan struct{})
		idx.asyncEnabled = true
		go idx.asyncSearchLoop()
	} else {
		idx.asyncEnabled = false
		if idx.asyncDone != nil {
			close(idx.asyncDone)
			idx.asyncDone = nil
		}
	}
}

// AsyncFind 提交异步搜索查询。
// 非阻塞：如果已有待处理查询则替换（只保留最新）。
// 必须先调用 SetAsyncMode(true) 启用异步模式。
func (idx *ScrollbackSearchIndex) AsyncFind(query string) {
	idx.asyncMu.Lock()
	if !idx.asyncEnabled || idx.queryCh == nil {
		idx.asyncMu.Unlock()
		return
	}
	idx.asyncMu.Unlock()

	// 非阻塞写入：清空旧查询，写入新查询
	select {
	case <-idx.queryCh:
	default:
	}
	select {
	case idx.queryCh <- query:
	default:
	}
}

// Results 返回异步搜索结果 channel。
// 每次后台搜索完成后通过此 channel 发送结果。
func (idx *ScrollbackSearchIndex) Results() <-chan []ScrollbackMatch {
	idx.asyncMu.Lock()
	defer idx.asyncMu.Unlock()
	return idx.resultCh
}

// IsAsyncMode 返回是否处于异步模式。
func (idx *ScrollbackSearchIndex) IsAsyncMode() bool {
	idx.asyncMu.Lock()
	defer idx.asyncMu.Unlock()
	return idx.asyncEnabled
}

// asyncSearchLoop 是后台搜索 goroutine。
func (idx *ScrollbackSearchIndex) asyncSearchLoop() {
	for {
		select {
		case query := <-idx.queryCh:
			results := idx.Find(query)
			select {
			case idx.resultCh <- results:
			default:
				// 丢弃旧结果，只保留最新
			}
		case <-idx.asyncDone:
			return
		}
	}
}

// searchableText 返回 entry 的纯文本表示（用于搜索）。
// 尽可能提取完整文本（而非仅 Summary），使搜索能命中正文。
func searchableText(e *ScrollbackEntry) string {
	if e == nil || e.Block == nil {
		return ""
	}
	// 用类型断言提取完整文本
	switch block := e.Block.(type) {
	case *UserPromptBlock:
		return block.Text
	case *AgentMessageBlock:
		return block.Text
	case *ThinkingBlock:
		return block.Text
	case *ToolCallBlock:
		return block.ToolName + " " + block.Args + " " + block.Result + " " + block.Error
	case *EditToolBlock:
		return block.FilePath + " " + block.DiffText + " " + block.Result + " " + block.Error
	case *ExecuteToolBlock:
		return block.Command + " " + block.Output + " " + block.Error
	case *ReadToolBlock:
		return block.FilePath + " " + block.Preview + " " + block.Error
	case *SearchToolBlock:
		return block.Pattern + " " + block.Path + " " + block.Error
	case *SystemBlock:
		return block.Text
	case *ErrorBlock:
		return block.Text
	case *SessionEventBlock:
		return block.Text
	case *SubagentBlock:
		return block.AgentName + " " + block.SummaryText + " " + block.Result
	case *BgTaskBlock:
		return block.TaskName + " " + block.Status + " " + block.Output
	case *WorkflowBlock:
		return block.Name + " " + fmt.Sprintf("%d", block.Phase)
	case *ToolResultBlockAdapter:
		return block.ToolName + " " + block.Result + " " + block.Error
	}
	// 回退到 Summary
	return e.Block.Summary()
}
