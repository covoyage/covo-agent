package evolution

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type SkillApplyPlan struct {
	Content  string `yaml:"content,omitempty"`
	PostHash string `yaml:"post_hash"`
	Archive  string `yaml:"archive,omitempty"`
}

func atomicWorkshopWrite(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".workshop-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func workshopHash(path string) (string, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return "", fmt.Errorf("skill manifest is not a bounded regular file")
	}
	data, err := boundedWorkshopRead(path, 1<<20)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func workshopPackageHash(dir string) (string, error) {
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		return "missing", nil
	} else if err != nil {
		return "", err
	}
	hash := sha256.New()
	var total int64
	count := 0
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		count++
		if count > 4096 || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsupported or oversized skill package")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if !info.Mode().IsRegular() || total > 8<<20 {
			return fmt.Errorf("unsupported or oversized skill package")
		}
		data, err := boundedWorkshopRead(path, info.Size())
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		fmt.Fprintf(hash, "%d:%s:%x;", len(rel), rel, sha256.Sum256(data))
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func boundedWorkshopRead(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err == nil && int64(len(data)) > limit {
		return nil, fmt.Errorf("skill file exceeds read budget")
	}
	return data, err
}

func (s *WorkshopStore) targetFor(p SkillProposal) (string, error) {
	if _, err := validateSkillName(p.SkillName); err != nil {
		return "", err
	}
	dir := filepath.Join(s.skillMgr.skillsDir, p.SkillName)
	if p.Kind == ProposalDelete && p.Status == ProposalApplying && p.TargetPath != "" {
		dir = filepath.Dir(p.TargetPath)
		allowed := false
		for _, root := range s.skillMgr.lookupRoots() {
			resolved, err := filepath.EvalSymlinks(root)
			if err != nil {
				continue
			}
			rel, err := filepath.Rel(resolved, dir)
			if err != nil {
				continue
			}
			parts := strings.Split(rel, string(filepath.Separator))
			if (len(parts) == 1 || len(parts) == 2) && parts[len(parts)-1] == p.SkillName {
				if _, err := validateSkillName(parts[0]); err == nil {
					allowed = true
					break
				}
			}
		}
		if !allowed || filepath.Base(p.TargetPath) != "SKILL.md" {
			return "", fmt.Errorf("recorded skill target is outside configured roots")
		}
	} else if p.Kind != ProposalCreate {
		var err error
		dir, err = s.skillMgr.resolveSkillDirForRead(p.SkillName)
		if err != nil {
			return "", err
		}
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(dir))
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(dir); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return "", fmt.Errorf("skill directory is not a real directory")
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(dir), "SKILL.md"), nil
}

func (s *WorkshopStore) currentHash(p SkillProposal) (string, error) {
	if p.Kind == ProposalDelete {
		return workshopPackageHash(filepath.Dir(p.TargetPath))
	}
	return workshopHash(p.TargetPath)
}

func (s *WorkshopStore) captureBase(p *SkillProposal) error {
	target, err := s.targetFor(*p)
	if err != nil {
		return err
	}
	p.TargetPath = target
	p.BaseHash, err = s.currentHash(*p)
	if err != nil {
		return err
	}
	if p.Kind == ProposalCreate && p.BaseHash != "missing" {
		return fmt.Errorf("skill already exists")
	}
	if p.Kind != ProposalCreate && p.BaseHash == "missing" {
		return fmt.Errorf("skill does not exist")
	}
	return nil
}

func (s *WorkshopStore) BindSkillManager(manager *SkillManager) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skillMgr = manager
	for index := range s.items {
		if s.items[index].Status == ProposalApplying {
			if err := s.publishPlan(index); err != nil {
				return fmt.Errorf("recover proposal %s: %w", s.items[index].ID, err)
			}
		}
	}
	return nil
}

func (s *WorkshopStore) ApplySkill(id string) (*SkillProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.skillMgr == nil {
		return nil, fmt.Errorf("skill manager unavailable")
	}
	for index := range s.items {
		p := &s.items[index]
		if p.ID != id {
			continue
		}
		if p.Status == ProposalApplying {
			if err := s.publishPlan(index); err != nil {
				return nil, err
			}
			copy := cloneProposal(s.items[index])
			return &copy, nil
		}
		if p.Status != ProposalPending {
			return nil, fmt.Errorf("proposal is not pending")
		}
		if p.BaseHash == "" {
			if err := s.captureBase(p); err != nil {
				return nil, err
			}
		}
		target, err := s.targetFor(*p)
		if err != nil || target != p.TargetPath {
			return nil, fmt.Errorf("proposal target changed")
		}
		current, err := s.currentHash(*p)
		if err != nil {
			return nil, err
		}
		if current != p.BaseHash {
			return nil, fmt.Errorf("skill changed since proposal creation")
		}
		plan, err := s.preparePlan(*p)
		if err != nil {
			return nil, err
		}
		p.Plan, p.Status = plan, ProposalApplying
		if err := s.save(); err != nil {
			p.Plan, p.Status = nil, ProposalPending
			return nil, err
		}
		if err := s.publishPlan(index); err != nil {
			return nil, err
		}
		copy := cloneProposal(s.items[index])
		return &copy, nil
	}
	return nil, fmt.Errorf("proposal %q not found", id)
}

func (s *WorkshopStore) preparePlan(p SkillProposal) (*SkillApplyPlan, error) {
	if p.Kind == ProposalDelete {
		if s.skillMgr.usage != nil {
			if record, ok := s.skillMgr.usage.GetRecord(p.SkillName); ok && record.Pinned {
				return nil, fmt.Errorf("pinned skill cannot be deleted")
			}
		}
		return &SkillApplyPlan{PostHash: "missing", Archive: filepath.Join(s.skillMgr.skillsDir, ".archive", p.SkillName+"-"+p.ID)}, nil
	}
	if len(p.Body) > 1<<20 {
		return nil, fmt.Errorf("proposal exceeds size limit")
	}
	fm, body, err := ParseSkillFrontmatter(p.Body)
	if err != nil {
		return nil, err
	}
	if body == "" {
		body = p.Body
	}
	if p.Kind == ProposalUpdate {
		existing, err := os.ReadFile(p.TargetPath)
		if err != nil {
			return nil, err
		}
		fm, _, err = parseSkillFile(string(existing))
		if err != nil {
			return nil, err
		}
	} else if p.Kind == ProposalCreate {
		description := p.Rationale
		if fm != nil && fm.Description != "" {
			description = fm.Description
		}
		if description == "" {
			description = "Skill from proposal " + p.ID
		}
		if len(description) > maxSkillDescriptionLength {
			return nil, fmt.Errorf("description exceeds limit")
		}
		fm = &SkillFrontmatter{Name: p.SkillName, Description: description, Version: "1.0.0"}
	} else {
		return nil, fmt.Errorf("unknown proposal kind")
	}
	metadata, err := yaml.Marshal(fm)
	if err != nil {
		return nil, err
	}
	content := fmt.Sprintf("---\n%s---\n\n%s\n", metadata, body)
	for _, finding := range ScanContent(content, "SKILL.md") {
		if finding.Severity == "critical" || finding.Severity == "high" {
			return nil, fmt.Errorf("skill content blocked: %s", finding.Description)
		}
	}
	return &SkillApplyPlan{Content: content, PostHash: fmt.Sprintf("%x", sha256.Sum256([]byte(content)))}, nil
}

func (s *WorkshopStore) publishPlan(index int) error {
	p := &s.items[index]
	if p.Plan == nil {
		return fmt.Errorf("missing commit plan")
	}
	target, err := s.targetFor(*p)
	if err != nil || target != p.TargetPath {
		return fmt.Errorf("proposal target changed")
	}
	current, err := s.currentHash(*p)
	if err != nil {
		return err
	}
	if current != p.BaseHash && current != p.Plan.PostHash {
		return fmt.Errorf("skill changed; recovery blocked")
	}
	if p.Kind == ProposalDelete {
		expected := filepath.Join(s.skillMgr.skillsDir, ".archive", p.SkillName+"-"+p.ID)
		if p.Plan.Archive != expected || strings.ContainsAny(p.ID, `/\\`) {
			return fmt.Errorf("invalid archive plan")
		}
		if current == "missing" {
			archived, err := workshopPackageHash(expected)
			if err != nil || archived != p.BaseHash {
				return fmt.Errorf("archive does not match commit plan")
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(expected), 0700); err != nil {
				return err
			}
			if _, err := os.Lstat(expected); !os.IsNotExist(err) {
				return fmt.Errorf("archive target exists or unavailable")
			}
			if err := os.Rename(filepath.Dir(p.TargetPath), expected); err != nil {
				return err
			}
		}
	} else if current != p.Plan.PostHash {
		if fmt.Sprintf("%x", sha256.Sum256([]byte(p.Plan.Content))) != p.Plan.PostHash {
			return fmt.Errorf("commit content digest mismatch")
		}
		if err := os.MkdirAll(filepath.Dir(p.TargetPath), 0755); err != nil {
			return err
		}
		if err := atomicWorkshopWrite(p.TargetPath, []byte(p.Plan.Content)); err != nil {
			return err
		}
	}
	if s.skillMgr.usage != nil {
		if p.Kind == ProposalCreate {
			if err := s.skillMgr.usage.RegisterSkillID(p.SkillName, p.SkillName, "agent-created"); err != nil {
				return err
			}
		}
		if p.Kind == ProposalDelete {
			if err := s.skillMgr.usage.RemoveSkill(p.SkillName); err != nil {
				return err
			}
		}
	}
	p.Status, p.UpdatedAt = ProposalApplied, time.Now()
	if err := s.save(); err != nil {
		p.Status = ProposalApplying
		return err
	}
	return nil
}
