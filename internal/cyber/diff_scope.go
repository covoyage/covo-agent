package cyber

import (
	"bufio"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

var diffHunkPattern = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

func (c *Catalog) SetDiffScope(baseRef, headRef string) (Engagement, error) {
	baseRef = strings.TrimSpace(baseRef)
	headRef = strings.TrimSpace(headRef)
	if baseRef == "" {
		return Engagement{}, fmt.Errorf("diff base_ref is required")
	}
	if headRef == "" {
		headRef = "HEAD"
	}
	workDir := filepath.Dir(filepath.Dir(c.dir))
	out, err := exec.Command("git", "-C", workDir, "diff", "--unified=0", baseRef+"..."+headRef, "--").Output()
	if err != nil {
		out, err = exec.Command("git", "-C", workDir, "diff", "--unified=0", baseRef, headRef, "--").Output()
	}
	if err != nil {
		return Engagement{}, fmt.Errorf("compute diff scope: %w", err)
	}
	ranges := parseDiffRanges(string(out))
	eng, err := c.GetEngagement()
	if err != nil {
		return Engagement{}, err
	}
	eng.ScopeMode = "diff"
	eng.BaseRef = baseRef
	eng.HeadRef = headRef
	eng.ChangedRanges = ranges
	eng.UpdatedAt = time.Now().UTC()
	catalogMu.Lock()
	defer catalogMu.Unlock()
	if err := c.writeJSON(c.engagementPath(), eng); err != nil {
		return Engagement{}, err
	}
	return eng, nil
}

func parseDiffRanges(diff string) map[string][]LineRange {
	result := map[string][]LineRange{}
	file := ""
	scanner := bufio.NewScanner(strings.NewReader(diff))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "+++ b/") {
			file = strings.TrimPrefix(line, "+++ b/")
			continue
		}
		match := diffHunkPattern.FindStringSubmatch(line)
		if file == "" || match == nil {
			continue
		}
		start, _ := strconv.Atoi(match[1])
		count := 1
		if match[2] != "" {
			count, _ = strconv.Atoi(match[2])
		}
		if count > 0 {
			result[file] = append(result[file], LineRange{Start: start, End: start + count - 1})
		}
	}
	return result
}

func (c *Catalog) validateDiffFinding(f Finding) error {
	eng, err := c.GetEngagement()
	if err != nil || eng.ScopeMode != "diff" {
		return err
	}
	file := filepath.ToSlash(strings.TrimPrefix(filepath.Clean(f.File), "./"))
	if file == "" || file == "." || f.Line <= 0 {
		return fmt.Errorf("diff-scope finding requires file and changed line")
	}
	for _, span := range eng.ChangedRanges[file] {
		if f.Line >= span.Start && f.Line <= span.End {
			return nil
		}
	}
	return fmt.Errorf("%s:%d is outside the configured diff scope", file, f.Line)
}
