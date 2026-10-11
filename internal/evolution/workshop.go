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
	ProposalDelete SkillProposalKind = "delete"
)

type SkillProposalStatus string

const (
	ProposalPending     SkillProposalStatus = "pending"
	ProposalApplied     SkillProposalStatus = "applied"
	ProposalRejected    SkillProposalStatus = "rejected"
	ProposalQuarantined SkillProposalStatus = "quarantined"
	ProposalApplying    SkillProposalStatus = "applying"
)

type SkillProposal struct {
	ID         string              `yaml:"id"`
	Kind       SkillProposalKind   `yaml:"kind"`
	Status     SkillProposalStatus `yaml:"status"`
	SkillName  string              `yaml:"skill_name"`
	Body       string              `yaml:"body"`
	Rationale  string              `yaml:"rationale,omitempty"`
	CreatedAt  time.Time           `yaml:"created_at"`
	UpdatedAt  time.Time           `yaml:"updated_at"`
	BaseHash   string              `yaml:"base_hash,omitempty"`
	TargetPath string              `yaml:"target_path,omitempty"`
	Plan       *SkillApplyPlan     `yaml:"apply_plan,omitempty"`
}

type WorkshopStore struct {
	mu       sync.RWMutex
	dir      string
	filePath string
	items    []SkillProposal
	skillMgr *SkillManager
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
	return s.load()
}

func (s *WorkshopStore) List() []SkillProposal {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]SkillProposal, len(s.items))
	for index, proposal := range s.items {
		result[index] = cloneProposal(proposal)
	}
	return result
}

func (s *WorkshopStore) ListPending() []SkillProposal {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []SkillProposal
	for _, p := range s.items {
		if p.Status == ProposalPending {
			result = append(result, cloneProposal(p))
		}
	}
	return result
}

func (s *WorkshopStore) Get(id string) (*SkillProposal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.items {
		if p.ID == id {
			cp := cloneProposal(p)
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
		if p.SkillName == skillName && (p.Status == ProposalPending || p.Status == ProposalApplying) {
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
	if s.skillMgr != nil {
		if err := s.captureBase(&p); err != nil {
			return nil, err
		}
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

func cloneProposal(proposal SkillProposal) SkillProposal {
	if proposal.Plan != nil {
		plan := *proposal.Plan
		proposal.Plan = &plan
	}
	return proposal
}

func (s *WorkshopStore) load() error {
	data, err := os.ReadFile(s.filePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read proposals: %w", err)
	}
	var items []SkillProposal
	if err := yaml.Unmarshal(data, &items); err != nil {
		return fmt.Errorf("decode proposals: %w", err)
	}
	s.items = items
	if s.items == nil {
		s.items = []SkillProposal{}
	}
	return nil
}

func (s *WorkshopStore) save() error {
	data, err := yaml.Marshal(s.items)
	if err != nil {
		return fmt.Errorf("marshal proposals: %w", err)
	}
	return atomicWorkshopWrite(s.filePath, data)
}
