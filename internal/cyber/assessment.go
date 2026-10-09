package cyber

import (
	"sort"
	"strings"
)

type CoverageCell struct {
	Target    string   `json:"target"`
	RiskClass string   `json:"risk_class"`
	Outcome   string   `json:"outcome"`
	Surfaces  []string `json:"surfaces,omitempty"`
	Notes     []string `json:"notes,omitempty"`
}

type ChainCandidate struct {
	Pattern    string   `json:"pattern"`
	Target     string   `json:"target"`
	FindingIDs []string `json:"finding_ids"`
	Status     string   `json:"status"`
	Reason     string   `json:"reason"`
}

func (c *Catalog) CoverageMatrix() ([]CoverageCell, error) {
	coverage, err := c.ListCoverage()
	if err != nil {
		return nil, err
	}
	assets, err := c.ListAssets()
	if err != nil {
		return nil, err
	}
	observations, err := c.ListAgentObservations()
	if err != nil {
		return nil, err
	}
	targets, classes := map[string]bool{}, map[string]bool{}
	for _, asset := range assets {
		if asset.Value != "" {
			targets[asset.Value] = true
		}
	}
	for _, observation := range observations {
		for _, skill := range observation.Skills {
			if strings.HasPrefix(skill, "security-testing-") {
				classes[strings.TrimPrefix(skill, "security-testing-")] = true
			}
		}
	}
	for _, item := range coverage {
		targets[item.Target] = true
		classes[coverageClass(item)] = true
	}
	if len(classes) == 0 || len(targets) == 0 {
		return nil, nil
	}
	cells := map[string]*CoverageCell{}
	for target := range targets {
		for class := range classes {
			key := target + "\x00" + class
			cells[key] = &CoverageCell{Target: target, RiskClass: class, Outcome: "untested"}
		}
	}
	for _, item := range coverage {
		cell := cells[item.Target+"\x00"+coverageClass(item)]
		cell.Surfaces = append(cell.Surfaces, item.Surface)
		if item.Notes != "" {
			cell.Notes = append(cell.Notes, item.Notes)
		}
		if item.Outcome == "finding" || (cell.Outcome != "finding" && item.Outcome == "blocked") ||
			(cell.Outcome != "finding" && cell.Outcome != "blocked" && item.Outcome == "clean") {
			cell.Outcome = item.Outcome
		}
	}
	result := make([]CoverageCell, 0, len(cells))
	for _, cell := range cells {
		result = append(result, *cell)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Target == result[j].Target {
			return result[i].RiskClass < result[j].RiskClass
		}
		return result[i].Target < result[j].Target
	})
	return result, nil
}

func coverageClass(item Coverage) string {
	if item.RiskClass != "" {
		return item.RiskClass
	}
	return strings.ToLower(item.Surface)
}

func (c *Catalog) ChainCandidates() ([]ChainCandidate, error) {
	findings, err := c.ListFindings()
	if err != nil {
		return nil, err
	}
	patterns := []struct {
		name, first, second, reason string
	}{
		{"authorization_disclosure", "639", "200", "Access-control and disclosure findings share a target; verify whether they form one impact path."},
		{"ssrf_disclosure", "918", "200", "SSRF and disclosure findings share a target; verify actual network reachability and data exposure."},
	}
	var candidates []ChainCandidate
	for i, first := range findings {
		if first.Status != StatusConfirmed || first.DuplicateOf != "" {
			continue
		}
		for _, second := range findings[i+1:] {
			if second.Status != StatusConfirmed || second.DuplicateOf != "" || first.ID == second.ID || first.Target != second.Target {
				continue
			}
			for _, pattern := range patterns {
				if (cweNumber(first.CWE) == pattern.first && cweNumber(second.CWE) == pattern.second) ||
					(cweNumber(second.CWE) == pattern.first && cweNumber(first.CWE) == pattern.second) {
					candidates = append(candidates, ChainCandidate{
						Pattern: pattern.name, Target: first.Target, FindingIDs: []string{first.ID, second.ID},
						Status: "candidate", Reason: pattern.reason,
					})
				}
			}
		}
	}
	return candidates, nil
}

func cweNumber(raw string) string {
	return strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(raw)), "CWE-")
}
