package rollout

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// PassFailCriterion defines how to judge whether a replay passed.
type PassFailCriterion int

const (
	// CriterionExactMatch requires identical tool calls and finish reasons.
	CriterionExactMatch PassFailCriterion = iota
	// CriterionToolMatch requires tool call names to match (content may differ).
	CriterionToolMatch
	// CriterionNoErrors requires no errors in the replay.
	CriterionNoErrors
)

func (c PassFailCriterion) String() string {
	switch c {
	case CriterionExactMatch:
		return "exact_match"
	case CriterionToolMatch:
		return "tool_match"
	case CriterionNoErrors:
		return "no_errors"
	default:
		return "unknown"
	}
}

// TestCase defines a single regression test case.
type TestCase struct {
	Name     string            `json:"name"`
	Rollout  *Rollout          `json:"rollout,omitempty"`
	FilePath string            `json:"file_path,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Criterion PassFailCriterion `json:"criterion"`
}

// TestResult is the outcome of running one test case.
type TestResult struct {
	Name      string        `json:"name"`
	Passed    bool          `json:"passed"`
	Reason    string        `json:"reason,omitempty"`
	Replay    *ReplayResult `json:"replay,omitempty"`
	Duration  time.Duration `json:"duration"`
}

// TestSuite is a collection of test cases and their results.
type TestSuite struct {
	Results   []TestResult `json:"results"`
	Passed    int          `json:"passed"`
	Failed    int          `json:"failed"`
	Total     int          `json:"total"`
	Duration  time.Duration `json:"duration"`
}

// TestConfig configures a batch regression test run.
type TestConfig struct {
	Provider agentcore.Provider
	ToolExec ToolExecutor
	Logger   *slog.Logger
	Model    string
	Strategy ToolStrategy
}

// RunTestSuite executes all test cases and returns the results.
func RunTestSuite(ctx context.Context, cfg TestConfig, cases []TestCase) (*TestSuite, error) {
	if cfg.Provider == nil {
		return nil, fmt.Errorf("no provider configured for test suite")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	suite := &TestSuite{
		Results: make([]TestResult, len(cases)),
	}

	startTime := time.Now()

	for i, tc := range cases {
		logger.Info("running test case",
			"index", i+1,
			"name", tc.Name,
			"criterion", tc.Criterion)

		result := runTestCase(ctx, cfg, tc)
		suite.Results[i] = result

		if result.Passed {
			suite.Passed++
		} else {
			suite.Failed++
		}
	}

	suite.Total = len(cases)
	suite.Duration = time.Since(startTime)
	return suite, nil
}

func runTestCase(ctx context.Context, cfg TestConfig, tc TestCase) TestResult {
	start := time.Now()
	result := TestResult{Name: tc.Name}

	// Load rollout from file if not provided inline.
	rollout := tc.Rollout
	if rollout == nil && tc.FilePath != "" {
		data, err := os.ReadFile(tc.FilePath)
		if err != nil {
			result.Passed = false
			result.Reason = fmt.Sprintf("failed to read file: %v", err)
			result.Duration = time.Since(start)
			return result
		}
		_, r, err := ParseBundleOrRollout(data)
		if err != nil {
			result.Passed = false
			result.Reason = fmt.Sprintf("failed to parse rollout: %v", err)
			result.Duration = time.Since(start)
			return result
		}
		rollout = r
	}

	if rollout == nil {
		result.Passed = false
		result.Reason = "no rollout provided"
		result.Duration = time.Since(start)
		return result
	}

	// Apply test-specific env vars.
	if len(tc.Env) > 0 {
		for k, v := range tc.Env {
			os.Setenv(k, v)
		}
		defer func() {
			for k := range tc.Env {
				os.Unsetenv(k)
			}
		}()
	}

	// Run the replay.
	engine := NewReplayEngine(ReplayConfig{
		Model:    cfg.Model,
		Provider: cfg.Provider,
		ToolExec: cfg.ToolExec,
		Mode:     ReplayModeDeterministic,
		Strategy: cfg.Strategy,
		Logger:   cfg.Logger,
	})

	replayResult, err := engine.Replay(ctx, rollout)
	if err != nil {
		result.Passed = false
		result.Reason = fmt.Sprintf("replay failed: %v", err)
		result.Duration = time.Since(start)
		return result
	}
	result.Replay = replayResult

	// Apply pass/fail criterion.
	passed, reason := judgePassFail(rollout, replayResult, tc.Criterion)
	result.Passed = passed
	result.Reason = reason
	result.Duration = time.Since(start)
	return result
}

func judgePassFail(original *Rollout, replayed *ReplayResult, criterion PassFailCriterion) (bool, string) {
	switch criterion {
	case CriterionExactMatch:
		diff := DiffRollouts(original, replayed.Rollout)
		if diff.Identical {
			return true, ""
		}
		return false, fmt.Sprintf("%d differences found", len(diff.Items))

	case CriterionToolMatch:
		if len(original.Turns) != len(replayed.Rollout.Turns) {
			return false, fmt.Sprintf("turn count mismatch: %d vs %d",
				len(original.Turns), len(replayed.Rollout.Turns))
		}
		for i := range original.Turns {
			ot := original.Turns[i]
			rt := replayed.Rollout.Turns[i]
			if len(ot.ToolCalls) != len(rt.ToolCalls) {
				return false, fmt.Sprintf("turn %d: tool call count %d vs %d",
					ot.Number, len(ot.ToolCalls), len(rt.ToolCalls))
			}
			for j := range ot.ToolCalls {
				if ot.ToolCalls[j].Name != rt.ToolCalls[j].Name {
					return false, fmt.Sprintf("turn %d tool %d: name %q vs %q",
						ot.Number, j, ot.ToolCalls[j].Name, rt.ToolCalls[j].Name)
				}
			}
		}
		return true, ""

	case CriterionNoErrors:
		for _, e := range replayed.Errors {
			return false, e
		}
		return true, ""

	default:
		return false, "unknown criterion"
	}
}

// LoadTestCasesFromDir scans a directory for .json rollout files and
// returns them as test cases with the no_errors criterion.
func LoadTestCasesFromDir(dir string) ([]TestCase, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	var cases []TestCase
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		cases = append(cases, TestCase{
			Name:      strings.TrimSuffix(entry.Name(), ".json"),
			FilePath:  filepath.Join(dir, entry.Name()),
			Criterion: CriterionNoErrors,
		})
	}
	return cases, nil
}

// FormatTestSuite returns a human-readable summary of test results.
func FormatTestSuite(suite *TestSuite) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Test Suite: %d total, %d passed, %d failed (%s)\n\n",
		suite.Total, suite.Passed, suite.Failed, suite.Duration.Round(time.Millisecond))

	for _, r := range suite.Results {
		status := "PASS"
		if !r.Passed {
			status = "FAIL"
		}
		line := fmt.Sprintf("  [%s] %s", status, r.Name)
		if r.Reason != "" {
			line += fmt.Sprintf(" — %s", r.Reason)
		}
		if r.Replay != nil {
			line += fmt.Sprintf(" (%s)", r.Replay.Duration.Round(time.Millisecond))
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// WriteTestReport writes the test suite results as JSON to a file.
func WriteTestReport(suite *TestSuite, path string) error {
	data, err := json.MarshalIndent(suite, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal test report: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}
