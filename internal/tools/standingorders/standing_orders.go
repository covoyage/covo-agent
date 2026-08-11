package standingorders

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// StandingOrder is a persistent instruction injected into every session.
type StandingOrder struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// StandingOrdersStore manages persistent instructions with JSON persistence.
type StandingOrdersStore struct {
	mu       sync.RWMutex
	items    map[string]*StandingOrder
	filePath string
	seq      int
}

// NewStandingOrdersStore creates a store backed by the given directory.
func NewStandingOrdersStore(dir string) *StandingOrdersStore {
	s := &StandingOrdersStore{
		items:    make(map[string]*StandingOrder),
		filePath: filepath.Join(dir, "standing_orders.json"),
	}
	s.load()
	return s
}

// List returns all standing orders, newest first.
func (s *StandingOrdersStore) List() []StandingOrder {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]StandingOrder, 0, len(s.items))
	for _, o := range s.items {
		out = append(out, *o)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// Add creates a new standing order.
func (s *StandingOrdersStore) Add(content string) (*StandingOrder, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("standing order content cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.seq++
	o := &StandingOrder{
		ID:        fmt.Sprintf("so_%d", s.seq),
		Content:   content,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	s.items[o.ID] = o
	s.save()
	return o, nil
}

// Remove deletes a standing order by ID.
func (s *StandingOrdersStore) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return fmt.Errorf("standing order %q not found", id)
	}
	delete(s.items, id)
	s.save()
	return nil
}

// Clear removes all standing orders.
func (s *StandingOrdersStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = make(map[string]*StandingOrder)
	s.save()
	return nil
}

// BuildPromptSuffix returns the standing orders formatted for prompt injection.
func (s *StandingOrdersStore) BuildPromptSuffix() string {
	orders := s.List()
	if len(orders) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n<standing_orders>\n")
	b.WriteString("The following are your standing orders — persistent behavioral instructions that apply to every session:\n\n")
	for _, o := range orders {
		b.WriteString(fmt.Sprintf("- %s\n", o.Content))
	}
	b.WriteString("</standing_orders>\n")
	return b.String()
}

// --- persistence ---

func (s *StandingOrdersStore) load() {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return
	}
	var items []*StandingOrder
	if err := json.Unmarshal(data, &items); err != nil {
		return
	}
	for _, o := range items {
		s.items[o.ID] = o
		if o.ID != "" {
			var n int
			fmt.Sscanf(o.ID, "so_%d", &n)
			if n > s.seq {
				s.seq = n
			}
		}
	}
}

func (s *StandingOrdersStore) save() {
	items := make([]*StandingOrder, 0, len(s.items))
	for _, o := range s.items {
		items = append(items, o)
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return
	}
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return
	}
	os.WriteFile(s.filePath, data, 0600)
}

// StandingOrdersToolItem is the public view of a standing order for the tool.
type StandingOrdersToolItem struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

// ToToolItems converts internal StandingOrders to tool-safe items.
func (s *StandingOrdersStore) ToToolItems() []StandingOrdersToolItem {
	orders := s.List()
	items := make([]StandingOrdersToolItem, len(orders))
	for i, o := range orders {
		items[i] = StandingOrdersToolItem{ID: o.ID, Content: o.Content}
	}
	return items
}
