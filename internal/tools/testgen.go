package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// GenerateTestsResult holds the outcome of test generation.
type GenerateTestsResult struct {
	TestFile string `json:"test_file,omitempty"`
	Language string `json:"language"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

// GenerateTestsForFile generates a test file for the given source file.
// It detects the language, finds the appropriate test framework, and creates
// a test file skeleton or runs a test generation tool.
func GenerateTestsForFile(srcPath string) (*GenerateTestsResult, error) {
	info, err := os.Stat(srcPath)
	if err != nil {
		return nil, fmt.Errorf("source file: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%q is a directory, not a file", srcPath)
	}

	ext := strings.ToLower(filepath.Ext(srcPath))
	dir := filepath.Dir(srcPath)
	base := strings.TrimSuffix(filepath.Base(srcPath), ext)

	switch ext {
	case ".go":
		testFile := filepath.Join(dir, base+"_test.go")
		if _, err := os.Stat(testFile); err == nil {
			return &GenerateTestsResult{
				TestFile: testFile,
				Language: "go",
				Status:   "exists",
			}, nil
		}
		if cmd, err := exec.LookPath("gotests"); err == nil {
			out, err := exec.Command(cmd, "-all", "-w", srcPath).CombinedOutput()
			if err == nil {
				return &GenerateTestsResult{
					TestFile: testFile,
					Language: "go",
					Status:   "generated",
				}, nil
			}
			_ = out
		}
		return &GenerateTestsResult{
			TestFile: testFile,
			Language: "go",
			Status:   "suggested",
			Error:    "no test file yet; write real tests to " + testFile + " (gotests not available)",
		}, nil

	case ".py":
		testFile := filepath.Join(dir, "test_"+base+".py")
		if _, err := os.Stat(testFile); err == nil {
			return &GenerateTestsResult{
				TestFile: testFile,
				Language: "python",
				Status:   "exists",
			}, nil
		}
		return &GenerateTestsResult{
			TestFile: testFile,
			Language: "python",
			Status:   "suggested",
			Error:    "no test file yet; write real tests to " + testFile,
		}, nil

	case ".js", ".jsx", ".ts", ".tsx":
		testSuffix := ".test" + ext
		testFile := filepath.Join(dir, base+testSuffix)
		if _, err := os.Stat(testFile); err == nil {
			return &GenerateTestsResult{
				TestFile: testFile,
				Language: "javascript",
				Status:   "exists",
			}, nil
		}
		return &GenerateTestsResult{
			TestFile: testFile,
			Language: "javascript",
			Status:   "suggested",
			Error:    "no test file yet; write real tests to " + testFile,
		}, nil

	default:
		return &GenerateTestsResult{
			Language: ext,
			Status:   "unsupported",
			Error:    fmt.Sprintf("unsupported file extension: %s", ext),
		}, nil
	}
}

func buildGenerateTestsTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "generate_tests",
		Description: "Suggest the conventional test file path for a source file. Runs gotests for Go when available; otherwise reports the path without writing a placeholder skeleton. Does not invent test bodies.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file": map[string]any{
					"type":        "string",
					"description": "Path to the source file to generate tests for",
				},
			},
			"required": []string{"file"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				File string `json:"file"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if params.File == "" {
				return nil, fmt.Errorf("file is required")
			}
			return GenerateTestsForFile(params.File)
		},
	}
}

// --- helpers ---

func detectGoPackage(dir string) string {
	// Read the package declaration from any .go file in the directory
	entries, err := os.ReadDir(dir)
	if err != nil {
		return filepath.Base(dir)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "package ") {
				return strings.TrimSpace(strings.TrimPrefix(line, "package "))
			}
		}
	}
	return filepath.Base(dir)
}

func toTitle(s string) string {
	if s == "" {
		return s
	}
	// Handle snake_case: split on underscore, capitalize each part
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}
