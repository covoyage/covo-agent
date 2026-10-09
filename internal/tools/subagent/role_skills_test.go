package subagent

import (
	"strings"
	"testing"
)

func TestLoadRoleSkillsInjectsAndSkipsMissing(t *testing.T) {
	SetSkillBodyLoader(func(name string) (string, error) {
		switch name {
		case "attack-surface-recon":
			return "recon body", nil
		default:
			return "", nil
		}
	})
	t.Cleanup(func() { SetSkillBodyLoader(nil) })

	body := loadRoleSkills("recon", "map the application")
	if !strings.Contains(body, "recon body") {
		t.Fatalf("missing skill body: %q", body)
	}
	if strings.Contains(body, "pentest-verification") {
		t.Fatalf("missing skills should be skipped: %q", body)
	}
	long := strings.Repeat("x", 7000)
	SetSkillBodyLoader(func(string) (string, error) { return long, nil })
	truncated := loadRoleSkills("recon", "map the application")
	if len([]rune(truncated)) > 5*6000+400 {
		t.Fatalf("skill bodies were not truncated: %d", len([]rune(truncated)))
	}
}

func TestRoleSkillNamesForTaskAddsVulnerabilityPlaybooks(t *testing.T) {
	names := roleSkillNamesForTask("verifier", "Verify the IDOR lead and possible SSRF callback")
	joined := strings.Join(names, ",")
	for _, want := range []string{"security-testing-idor", "security-testing-ssrf"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %v", want, names)
		}
	}
	if len(names) > maxInjectedRoleSkills {
		t.Fatalf("injected %d skills, want at most %d", len(names), maxInjectedRoleSkills)
	}
	if reporter := strings.Join(roleSkillNamesForTask("reporter", "Report the IDOR finding"), ","); strings.Contains(reporter, "security-testing-idor") {
		t.Fatalf("reporter should not receive hands-on playbooks: %s", reporter)
	}
}
