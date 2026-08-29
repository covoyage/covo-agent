package commands

import (
	"path/filepath"
	"sort"

	"github.com/covoyage/covo-agent/internal/evolution"
	covoskill "github.com/covoyage/covonaut/skill"
	"github.com/covoyage/covonaut/tui/component"
)

func buildSkillCenterData(runtimeSkills []covoskill.Skill, inventory []evolution.SkillInfo, usage *evolution.SkillUsageTracker) ([]component.SkillItem, map[string]string) {
	sort.Slice(inventory, func(i, j int) bool { return inventory[i].ID < inventory[j].ID })
	byDir := make(map[string]evolution.SkillInfo, len(inventory))
	byName := make(map[string][]evolution.SkillInfo)
	for _, info := range inventory {
		byDir[filepath.Clean(info.Dir)] = info
		byName[info.Name] = append(byName[info.Name], info)
	}
	nameCounts := make(map[string]int)
	for _, runtimeSkill := range runtimeSkills {
		nameCounts[runtimeSkill.Name]++
	}

	items := make([]component.SkillItem, 0, len(runtimeSkills))
	paths := make(map[string]string, len(runtimeSkills))
	usedIDs := make(map[string]bool)
	for _, runtimeSkill := range runtimeSkills {
		info, matched := byDir[filepath.Clean(runtimeSkill.BaseDir)]
		if !matched {
			for _, candidate := range byName[runtimeSkill.Name] {
				if !usedIDs[candidate.ID] {
					info = candidate
					matched = true
					break
				}
			}
		}
		id := runtimeSkill.Name
		if matched {
			id = info.ID
			usedIDs[id] = true
		} else if runtimeSkill.FilePath != "" {
			id = "external:" + filepath.Clean(runtimeSkill.FilePath)
		}
		displayName := runtimeSkill.Name
		if nameCounts[runtimeSkill.Name] > 1 && matched {
			displayName = info.ID
		}
		item := component.SkillItem{ID: id, Name: displayName, State: evolution.StateActive, Provenance: "loaded"}
		if usage != nil {
			if record, ok := usage.GetRecord(id); ok {
				item.State = record.State
				item.Provenance = record.Provenance
				item.Pinned = record.Pinned
				item.UseCount = record.UseCount
				item.CreatedAt = record.CreatedAt
				item.LastUsed = record.LastActivityAt
			}
		}
		items = append(items, item)
		paths[id] = runtimeSkill.FilePath
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name == items[j].Name {
			return items[i].ID < items[j].ID
		}
		return items[i].Name < items[j].Name
	})
	return items, paths
}
