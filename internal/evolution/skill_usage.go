package evolution

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

const (
	StateActive   = "active"
	StateStale    = "stale"
	StateArchived = "archived"
)

type SkillRecord struct {
	ID             string `json:"id,omitempty"`
	Name           string `json:"name"`
	State          string `json:"state"`
	Provenance     string `json:"provenance"`
	Pinned         bool   `json:"pinned"`
	CreatedAt      string `json:"created_at"`
	LastActivityAt string `json:"last_activity_at"`
	UseCount       int    `json:"use_count"`
}

type SkillUsageTracker struct {
	mu       sync.RWMutex
	filePath string
	records  map[string]*SkillRecord
}

func NewSkillUsageTracker(skillsDir string) *SkillUsageTracker {
	return &SkillUsageTracker{
		filePath: filepath.Join(skillsDir, ".usage.json"),
		records:  make(map[string]*SkillRecord),
	}
}

func (t *SkillUsageTracker) Load() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	data, err := os.ReadFile(t.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, &t.records)
}

func (t *SkillUsageTracker) save() error {
	if err := os.MkdirAll(filepath.Dir(t.filePath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(t.records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(t.filePath, data, 0644)
}

func (t *SkillUsageTracker) RecordView(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	_, rec, ok := t.resolveRecordLocked(name)
	if !ok {
		return
	}
	rec.UseCount++
	rec.LastActivityAt = time.Now().UTC().Format(time.RFC3339)
	if rec.State == StateStale {
		rec.State = StateActive
	}
	_ = t.save()
}

func (t *SkillUsageTracker) RegisterSkill(name, provenance string) error {
	return t.RegisterSkillID(name, name, provenance)
}

func (t *SkillUsageTracker) RegisterSkillID(id, name, provenance string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	record, ok := t.records[id]
	if ok {
		changed := record.Provenance != provenance || record.State != StateActive
		if record.ID == "" {
			record.ID = id
			changed = true
		}
		record.Provenance = provenance
		record.State = StateActive
		if !changed {
			return nil
		}
		return t.save()
	}
	t.records[id] = &SkillRecord{
		ID:             id,
		Name:           name,
		State:          StateActive,
		Provenance:     provenance,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		LastActivityAt: time.Now().UTC().Format(time.RFC3339),
		UseCount:       0,
	}
	return t.save()
}

// ReconcileSkills registers missing on-disk skills in one atomic save while
// preserving existing state, provenance, timestamps, pinning, and use counts.
func (t *SkillUsageTracker) ReconcileSkills(skills map[string]SkillRegistration) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	changed := false
	ids := make([]string, 0, len(skills))
	for id := range skills {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		iQualified := strings.Contains(ids[i], "/")
		jQualified := strings.Contains(ids[j], "/")
		if iQualified != jQualified {
			return !iQualified
		}
		return ids[i] < ids[j]
	})
	for _, id := range ids {
		skill := skills[id]
		if id == "" || skill.Name == "" {
			continue
		}
		if record, exists := t.records[id]; exists {
			if record.ID == "" {
				record.ID = id
				changed = true
			}
			continue
		}
		// Migrate a legacy bare-name key to the canonical ID while preserving
		// all usage metadata. Ambiguous names deterministically migrate to the
		// first ID supplied by the inventory; remaining IDs receive fresh rows.
		_, flatSkillExists := skills[skill.Name]
		if legacy, exists := t.records[skill.Name]; exists && id != skill.Name && !flatSkillExists {
			delete(t.records, skill.Name)
			legacy.ID = id
			legacy.Name = skill.Name
			t.records[id] = legacy
			changed = true
			continue
		}
		t.records[id] = &SkillRecord{
			ID:             id,
			Name:           skill.Name,
			State:          StateActive,
			Provenance:     skill.Provenance,
			CreatedAt:      now,
			LastActivityAt: now,
		}
		changed = true
	}
	if !changed {
		return nil
	}
	return t.save()
}

type SkillRegistration struct {
	Name       string
	Provenance string
}

func (t *SkillUsageTracker) SetState(name, state string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	_, rec, ok := t.resolveRecordLocked(name)
	if !ok {
		return fmt.Errorf("skill %q not found", name)
	}
	rec.State = state
	return t.save()
}

func (t *SkillUsageTracker) RemoveSkill(name string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	key, _, ok := t.resolveRecordLocked(name)
	if !ok {
		return fmt.Errorf("skill %q not found", name)
	}
	delete(t.records, key)
	return t.save()
}

func (t *SkillUsageTracker) GetRecord(name string) (*SkillRecord, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	_, rec, ok := t.resolveRecordLocked(name)
	if !ok {
		return nil, false
	}
	copy := *rec
	return &copy, true
}

func (t *SkillUsageTracker) resolveRecordLocked(idOrName string) (string, *SkillRecord, bool) {
	if record, ok := t.records[idOrName]; ok {
		return idOrName, record, true
	}
	keys := make([]string, 0)
	for key, record := range t.records {
		if record.Name == idOrName {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return "", nil, false
	}
	sort.Strings(keys)
	key := keys[0]
	return key, t.records[key], true
}

func (t *SkillUsageTracker) AgentCreatedReport() []SkillRecord {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var result []SkillRecord
	for _, rec := range t.records {
		if rec.Provenance == "agent-created" {
			result = append(result, *rec)
		}
	}
	return result
}

func (t *SkillUsageTracker) AllRecords() []SkillRecord {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var result []SkillRecord
	for _, rec := range t.records {
		result = append(result, *rec)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
