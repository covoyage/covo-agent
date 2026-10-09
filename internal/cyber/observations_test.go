package cyber

import (
	"strings"
	"testing"
)

func TestMachineCoverageMarksIncompleteRunsAndMissingSkillRows(t *testing.T) {
	coverage := []Coverage{{Surface: "IDOR", Target: "/users", Outcome: "clean"}}
	observations := []AgentObservation{
		{ID: "sub-1", Role: "verifier", Status: "completed", Skills: []string{"security-testing-idor", "security-testing-ssrf"}, Tools: []string{"exchange_replay"}},
		{ID: "sub-2", Status: "failed"},
	}
	gaps := machineCoverageGaps(coverage, observations)
	if len(gaps) != 1 || !strings.Contains(gaps[0], "ssrf") {
		t.Fatalf("gaps = %#v", gaps)
	}
	if observationsComplete(observations) {
		t.Fatal("failed observation must make the run incomplete")
	}
}
