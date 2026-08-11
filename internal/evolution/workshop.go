package evolution

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type SkillProposalKind string

const (
	ProposalCreate SkillProposalKind = "create"
	ProposalUpdate SkillProposalKind = "update"
)

type SkillProposalStatus string

const (
	ProposalPending     SkillProposalStatus = "pending"
	ProposalApplied     SkillProposalStatus = "applied"
	ProposalRejected    SkillProposalStatus = "rejected"
	ProposalQuarantined SkillProposalStatus = "quarantined"
)

type SkillProposal struct {
	ID        string             `yaml:"id"`
	Kind      SkillProposalKind  `yaml:"kind"`
	Status    SkillProposalStatus `yaml:"status"`
	SkillName string             `yaml:"skill_name"`
	Body      string             `yaml:"body"`
	Rationale string             `yaml:"rationale,omitempty"`
	CreatedAt time.Time          `yaml:"created_at"`
	UpdatedAt time.Time          `yaml:"updated_at"`
}

type WorkshopStore struct {
	mu       sync.RWMutex
	dir      string
	filePath string
	items    []SkillProposal
}

func NewWorkshopStore(dir string) *WorkshopStore {
	return &WorkshopStore{
		dir:      dir,
		filePath: filepath.Join(dir, "workshop_proposals.yaml"),
	}
}

func (s *WorkshopStore) Init() error {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return fmt.Errorf("create workshop dir: %w", err)
	}
	s.load()
	return nil
}

func (s *WorkshopStore) List() []SkillProposal {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]SkillProposal, len(s.items))
	copy(result, s.items)
	return result
}

func (s *WorkshopStore) ListPending() []SkillProposal {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []SkillProposal
	for _, p := range s.items {
		if p.Status == ProposalPending {
			result = append(result, p)
		}
	}
	return result
}

func (s *WorkshopStore) Get(id string) (*SkillProposal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.items {
		if p.ID == id {
			cp := p
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("proposal %q not found", id)
}

func (s *WorkshopStore) Create(kind SkillProposalKind, skillName, body, rationale string) (*SkillProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check for existing pending proposal for the same skill
	for _, p := range s.items {
		if p.SkillName == skillName && p.Status == ProposalPending {
			return nil, fmt.Errorf("pending proposal %q already exists for skill %q", p.ID, skillName)
		}
	}

	id := fmt.Sprintf("prop-%d", time.Now().UnixNano())
	p := SkillProposal{
		ID:        id,
		Kind:      kind,
		Status:    ProposalPending,
		SkillName: skillName,
		Body:      body,
		Rationale: rationale,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	s.items = append(s.items, p)
	if err := s.save(); err != nil {
		return nil, err
	}
	cp := p
	return &cp, nil
}

func (s *WorkshopStore) Revise(id, newBody string) (*SkillProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, p := range s.items {
		if p.ID == id {
			if p.Status != ProposalPending {
				return nil, fmt.Errorf("proposal %q is not pending (status: %s)", id, p.Status)
			}
			s.items[i].Body = newBody
			s.items[i].UpdatedAt = time.Now()
			if err := s.save(); err != nil {
				return nil, err
			}
			cp := s.items[i]
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("proposal %q not found", id)
}

func (s *WorkshopStore) Apply(id string, applyFn func(p SkillProposal) error) (*SkillProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, p := range s.items {
		if p.ID == id {
			if p.Status != ProposalPending {
				return nil, fmt.Errorf("proposal %q is not pending (status: %s)", id, p.Status)
			}
			if err := applyFn(p); err != nil {
				return nil, fmt.Errorf("apply proposal: %w", err)
			}
			s.items[i].Status = ProposalApplied
			s.items[i].UpdatedAt = time.Now()
			if err := s.save(); err != nil {
				return nil, err
			}
			cp := s.items[i]
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("proposal %q not found", id)
}

func (s *WorkshopStore) Reject(id string) (*SkillProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, p := range s.items {
		if p.ID == id {
			if p.Status != ProposalPending {
				return nil, fmt.Errorf("proposal %q is not pending (status: %s)", id, p.Status)
			}
			s.items[i].Status = ProposalRejected
			s.items[i].UpdatedAt = time.Now()
			if err := s.save(); err != nil {
				return nil, err
			}
			cp := s.items[i]
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("proposal %q not found", id)
}

func (s *WorkshopStore) Quarantine(id string) (*SkillProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, p := range s.items {
		if p.ID == id {
			s.items[i].Status = ProposalQuarantined
			s.items[i].UpdatedAt = time.Now()
			if err := s.save(); err != nil {
				return nil, err
			}
			cp := s.items[i]
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("proposal %q not found", id)
}

func (s *WorkshopStore) load() {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return
	}
	_ = yaml.Unmarshal(data, &s.items)
	if s.items == nil {
		s.items = []SkillProposal{}
	}
}

func (s *WorkshopStore) save() error {
	data, err := yaml.Marshal(s.items)
	if err != nil {
		return fmt.Errorf("marshal proposals: %w", err)
	}
	return os.WriteFile(s.filePath, data, 0644)
}
