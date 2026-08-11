package plugin

import (
	"fmt"
	"sort"
	"sync"
)

type Entry struct {
	ID       string
	Name     string
	Category Category
	Enabled  bool
	Provider any
}

type Registry struct {
	mu      sync.RWMutex
	entries map[string]*Entry
}

func NewRegistry() *Registry {
	return &Registry{
		entries: make(map[string]*Entry),
	}
}

func (r *Registry) Register(entry *Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if entry.ID == "" {
		return fmt.Errorf("plugin entry ID is required")
	}
	if _, exists := r.entries[entry.ID]; exists {
		return fmt.Errorf("plugin %q already registered", entry.ID)
	}
	r.entries[entry.ID] = entry
	return nil
}

func (r *Registry) Unregister(id string) {
	if id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, id)
}

func (r *Registry) Get(id string) *Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.entries[id]
}

func (r *Registry) List() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Entry, 0, len(r.entries))
	for _, e := range r.entries {
		result = append(result, *e)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Category != result[j].Category {
			return result[i].Category < result[j].Category
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func (r *Registry) ListByCategory(cat Category) []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []Entry
	for _, e := range r.entries {
		if e.Category == cat {
			result = append(result, *e)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func (r *Registry) ListEnabledByCategory(cat Category) []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []Entry
	for _, e := range r.entries {
		if e.Category == cat && e.Enabled {
			result = append(result, *e)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func (r *Registry) Enable(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.entries[id]
	if !ok {
		return fmt.Errorf("plugin %q not found", id)
	}
	entry.Enabled = true
	return nil
}

func (r *Registry) Disable(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.entries[id]
	if !ok {
		return fmt.Errorf("plugin %q not found", id)
	}
	entry.Enabled = false
	return nil
}

func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

func (r *Registry) EnabledCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := 0
	for _, e := range r.entries {
		if e.Enabled {
			count++
		}
	}
	return count
}
