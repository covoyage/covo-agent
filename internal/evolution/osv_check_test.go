package evolution

import (
	"strings"
	"testing"
)

func TestParseRequirementsTxt(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
	}{
		{
			name: "basic",
			content: `requests==2.28.1
flask>=2.0.0
numpy==1.24.0
`,
			want: 3,
		},
		{
			name: "with comments and blanks",
			content: `# Main dependencies
requests==2.28.1

# Dev
pytest==7.0.0
`,
			want: 2,
		},
		{
			name:    "empty",
			content: "",
			want:    0,
		},
		{
			name:    "two packages",
			content: "flask>=2.0.0\nnumpy==1.24.0\n",
			want:    2,
		},
		{
			name: "with version operators",
			content: `package~=1.0
other!=1.0
another<=2.0
`,
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseRequirementsTxt(tt.content)
			if len(result) != tt.want {
				t.Errorf("ParseRequirementsTxt returned %d packages, want %d: %v", len(result), tt.want, result)
			}
			for _, pkg := range result {
				if pkg.Ecosystem != "PyPI" {
					t.Errorf("expected ecosystem PyPI, got %q", pkg.Ecosystem)
				}
				if pkg.Name == "" {
					t.Error("package name should not be empty")
				}
			}
		})
	}
}

func TestParsePackageJson(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
	}{
		{
			name: "basic dependencies",
			content: `{
  "name": "test",
  "dependencies": {
    "express": "^4.18.0",
    "lodash": "~4.17.21"
  }
}`,
			want: 2,
		},
		{
			name: "with devDependencies",
			content: `{
  "dependencies": {
    "react": "^18.0.0"
  },
  "devDependencies": {
    "jest": "^29.0.0"
  }
}`,
			want: 2,
		},
		{
			name: "empty",
			content: `{
  "name": "empty-test"
}`,
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParsePackageJson(tt.content)
			if len(result) != tt.want {
				t.Errorf("ParsePackageJson returned %d packages, want %d: %v", len(result), tt.want, result)
			}
			for _, pkg := range result {
				if pkg.Ecosystem != "npm" {
					t.Errorf("expected ecosystem npm, got %q", pkg.Ecosystem)
				}
				if pkg.Name == "" {
					t.Error("package name should not be empty")
				}
			}
		})
	}
}

func TestFormatOsvReport(t *testing.T) {
	// Empty results
	empty := FormatOsvReport(map[string][]OsvVulnerability{})
	if empty == "" {
		t.Error("FormatOsvReport with empty results should return a non-empty string")
	}

	// Results with vulnerabilities
	results := map[string][]OsvVulnerability{
		"PyPI:requests": {
			{
				ID:       "CVE-2023-1234",
				Summary:  "Test vulnerability",
				Severity: "HIGH",
				FixedIn:  "2.31.0",
			},
		},
	}

	report := FormatOsvReport(results)
	if report == "" {
		t.Error("FormatOsvReport returned empty")
	}
	if !strings.Contains(report, "CVE-2023-1234") {
		t.Error("report should contain CVE ID")
	}
}

func TestOsvCheck(t *testing.T) {
	t.Skip("requires network access to OSV.dev API")
}
