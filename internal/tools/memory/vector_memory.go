package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// VectorMem provides file-based semantic memory using embedding + cosine similarity.
type VectorMem struct {
	docDir string
	mu     sync.RWMutex
	docs   []vecDoc
}

type vecDoc struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Source    string    `json:"source"`
	Embedding []float64 `json:"embedding"`
	CreatedAt time.Time `json:"created_at"`
}

func NewVectorMem(homeDir string) *VectorMem {
	dir := filepath.Join(homeDir, "vector_memory")
	os.MkdirAll(dir, 0755)
	vm := &VectorMem{docDir: dir}
	vm.load()
	return vm
}

func (vm *VectorMem) load() {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(vm.docDir, "mem.jsonl"))
	if err != nil {
		vm.docs = []vecDoc{}
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var doc vecDoc
		if json.Unmarshal([]byte(line), &doc) == nil {
			vm.docs = append(vm.docs, doc)
		}
	}
}

// saveLocked persists docs to disk. Caller must hold vm.mu (write or read).
func (vm *VectorMem) saveLocked() {
	var buf bytes.Buffer
	for _, doc := range vm.docs {
		b, _ := json.Marshal(doc)
		buf.Write(b)
		buf.WriteByte('\n')
	}
	os.WriteFile(filepath.Join(vm.docDir, "mem.jsonl"), buf.Bytes(), 0644)
}

func (vm *VectorMem) Store(id, content, source string) error {
	emb, err := getEmbedding(content)
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}
	vm.mu.Lock()
	defer vm.mu.Unlock()
	// Overwrite if same id exists
	for i, doc := range vm.docs {
		if doc.ID == id {
			vm.docs[i] = vecDoc{
				ID: id, Content: content, Source: source,
				Embedding: emb, CreatedAt: time.Now(),
			}
			vm.saveLocked()
			return nil
		}
	}
	vm.docs = append(vm.docs, vecDoc{
		ID:        id,
		Content:   content,
		Source:    source,
		Embedding: emb,
		CreatedAt: time.Now(),
	})
	vm.saveLocked()
	return nil
}

func (vm *VectorMem) Search(query string, limit int) ([]map[string]any, error) {
	queryEmb, err := getEmbedding(query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	type scored struct {
		doc   vecDoc
		score float64
	}
	var results []scored
	for _, doc := range vm.docs {
		score := cosine(queryEmb, doc.Embedding)
		results = append(results, scored{doc, score})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})
	if limit > len(results) {
		limit = len(results)
	}

	var out []map[string]any
	for i := 0; i < limit; i++ {
		r := results[i]
		out = append(out, map[string]any{
			"id":      r.doc.ID,
			"content": truncateMax(r.doc.Content, 500),
			"source":  r.doc.Source,
			"score":   fmt.Sprintf("%.4f", r.score),
		})
	}
	return out, nil
}

func (vm *VectorMem) Forget(id string) int {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	var kept []vecDoc
	for _, doc := range vm.docs {
		if doc.ID != id {
			kept = append(kept, doc)
		}
	}
	removed := len(vm.docs) - len(kept)
	vm.docs = kept
	vm.saveLocked()
	return removed
}

// StoreBatch indexes multiple documents. Useful for bulk session indexing.
func (vm *VectorMem) StoreBatch(docs []vecDoc) error {
	for i := range docs {
		emb, err := getEmbedding(docs[i].Content)
		if err != nil {
			return fmt.Errorf("embed doc %q: %w", docs[i].ID, err)
		}
		docs[i].Embedding = emb
		docs[i].CreatedAt = time.Now()
	}

	vm.mu.Lock()
	defer vm.mu.Unlock()
	for _, doc := range docs {
		found := false
		for i, existing := range vm.docs {
			if existing.ID == doc.ID {
				vm.docs[i] = doc
				found = true
				break
			}
		}
		if !found {
			vm.docs = append(vm.docs, doc)
		}
	}
	vm.saveLocked()
	return nil
}

func (vm *VectorMem) Stats() map[string]any {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	return map[string]any{
		"total_docs": len(vm.docs),
		"size_bytes": vm.fileSize(),
	}
}

func (vm *VectorMem) fileSize() int64 {
	s, err := os.Stat(filepath.Join(vm.docDir, "mem.jsonl"))
	if err != nil {
		return 0
	}
	return s.Size()
}

// --- Embedding helpers ---

func getEmbedding(text string) ([]float64, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY not set (required for embeddings)")
	}
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	body, _ := json.Marshal(map[string]any{
		"model": "text-embedding-3-small",
		"input": text,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&errBody)
		return nil, fmt.Errorf("embedding API returned %d: %s", resp.StatusCode, errBody.Error.Message)
	}

	var result struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("empty embedding response")
	}
	return result.Data[0].Embedding, nil
}

func cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (float64Sqrt(normA) * float64Sqrt(normB))
}

func float64Sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 20; i++ {
		z -= (z*z - x) / (2 * z)
	}
	return z
}

func truncateMax(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// --- Tool builders ---

func BuildMemorySemanticSearchTool(vm *VectorMem) *agentcore.Tool {
	return buildVecTool(vm, "memory_semantic_search",
		"搜索向量记忆库，返回语义接近的内容。用于在知识库中查找相关的历史对话、决策或笔记。",
	)
}

func BuildMemorySemanticStoreTool(vm *VectorMem) *agentcore.Tool {
	return buildVecStoreTool(vm, "memory_semantic_store",
		"将内容存入向量记忆库。id 由调用者指定，相同 id 会覆盖已有记录。",
	)
}

func BuildMemorySemanticForgetTool(vm *VectorMem) *agentcore.Tool {
	return buildVecForgetTool(vm, "memory_semantic_forget",
		"从向量记忆库中删除指定 id 的记录。",
	)
}

func BuildMemorySemanticStatsTool(vm *VectorMem) *agentcore.Tool {
	return buildVecStatsTool(vm, "memory_semantic_stats",
		"查看向量记忆库的统计信息，包括总记录数和文件大小。",
	)
}

// --- Tool builder helpers ---

func buildVecTool(vm *VectorMem, name, desc string) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        name,
		Description: desc,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "搜索查询语句。",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "返回结果数量（默认: 5）。",
				},
			},
			"required": []string{"query"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			json.Unmarshal(args, &params)
			if params.Limit <= 0 {
				params.Limit = 5
			}
			results, err := vm.Search(params.Query, params.Limit)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"results": results,
				"query":   params.Query,
				"count":   len(results),
			}, nil
		},
	}
}

func buildVecStoreTool(vm *VectorMem, name, desc string) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        name,
		Description: desc,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "记录唯一标识。",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "要存储的文本内容。",
				},
				"source": map[string]any{
					"type":        "string",
					"description": "来源标签（如 'conversation', 'note', 'code'）。",
				},
			},
			"required": []string{"id", "content"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				ID      string `json:"id"`
				Content string `json:"content"`
				Source  string `json:"source"`
			}
			json.Unmarshal(args, &params)
			if err := vm.Store(params.ID, params.Content, params.Source); err != nil {
				return nil, err
			}
			return map[string]any{
				"stored": true,
				"id":     params.ID,
			}, nil
		},
	}
}

func buildVecForgetTool(vm *VectorMem, name, desc string) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        name,
		Description: desc,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "要删除的记录标识。",
				},
			},
			"required": []string{"id"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				ID string `json:"id"`
			}
			json.Unmarshal(args, &params)
			removed := vm.Forget(params.ID)
			return map[string]any{
				"forgotten": removed > 0,
				"id":        params.ID,
				"removed":   removed,
			}, nil
		},
	}
}

func buildVecStatsTool(vm *VectorMem, name, desc string) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        name,
		Description: desc,
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			return vm.Stats(), nil
		},
	}
}
