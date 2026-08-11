package evolution

import (
	"strings"
	"testing"
)

func TestIsTyposquatting(t *testing.T) {
	tests := []struct {
		name         string
		pkgName      string
		wantTarget   string
		wantDistZero bool
	}{
		{
			name:         "requsts → requests",
			pkgName:      "requsts",
			wantTarget:   "requests",
			wantDistZero: false,
		},
		{
			name:         "nmpy → numpy",
			pkgName:      "nmpy",
			wantTarget:   "numpy",
			wantDistZero: false,
		},
		{
			name:         "flask safe",
			pkgName:      "flask",
			wantTarget:   "",
			wantDistZero: true,
		},
		{
			name:         "empty name",
			pkgName:      "",
			wantTarget:   "",
			wantDistZero: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, distance := IsTyposquatting(tt.pkgName)
			if tt.wantDistZero && distance != 0 {
				t.Errorf("IsTyposquatting(%q) distance=%d want 0", tt.pkgName, distance)
			}
			if !tt.wantDistZero && distance == 0 {
				t.Errorf("IsTyposquatting(%q) distance=0 want >0", tt.pkgName)
			}
			if target != tt.wantTarget {
				t.Errorf("IsTyposquatting(%q) target=%q want=%q", tt.pkgName, target, tt.wantTarget)
			}
		})
	}
}

func TestScanRequirementsTxt(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantRisky bool
	}{
		{
			name:      "clean requirements",
			content:   "requests==2.28.1\nflask>=2.0.0\npytest==7.0.0\n",
			wantRisky: false,
		},
		{
			name:      "empty",
			content:   "",
			wantRisky: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := ScanRequirementsTxt(tt.content)
			if report == nil {
				t.Fatal("ScanRequirementsTxt returned nil")
			}
			hasRisks := len(report.RiskyPackages) > 0
			if hasRisks != tt.wantRisky {
				t.Errorf("ScanRequirementsTxt risks=%v want=%v, risks: %v", hasRisks, tt.wantRisky, report.RiskyPackages)
			}
		})
	}
}

func TestScanPackageJson(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantRisky bool
	}{
		{
			name: "clean",
			content: `{
  "dependencies": {
    "express": "^4.18.0",
    "lodash": "~4.17.21"
  }
}`,
			wantRisky: false,
		},
		{
			name: "empty",
			content: `{
  "name": "empty"
}`,
			wantRisky: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := ScanPackageJson(tt.content)
			if report == nil {
				t.Fatal("ScanPackageJson returned nil")
			}
			hasRisks := len(report.RiskyPackages) > 0
			if hasRisks != tt.wantRisky {
				t.Errorf("ScanPackageJson risks=%v want=%v, risks: %v", hasRisks, tt.wantRisky, report.RiskyPackages)
			}
		})
	}
}

func TestFormatPkgSecurityReport(t *testing.T) {
	report := &PkgSecurityReport{
		Verdict: "caution",
		RiskyPackages: []PkgSecurityRisk{
			{
				PackageName: "test-pkg",
				Ecosystem:   "pypi",
				RiskType:    "typosquatting",
				RiskLevel:   "high",
				Description: "Similar to requests (distance 1)",
			},
		},
	}

	output := FormatPkgSecurityReport(report)
	if output == "" {
		t.Error("FormatPkgSecurityReport returned empty string")
	}
	if !strings.Contains(output, "test-pkg") {
		t.Error("report should contain package name")
	}
	if !strings.Contains(output, "typosquatting") {
		t.Error("report should contain risk type")
	}

	// Empty report
	emptyReport := &PkgSecurityReport{Verdict: "safe"}
	emptyOut := FormatPkgSecurityReport(emptyReport)
	if emptyOut == "" {
		t.Error("empty report should still produce output")
	}
}
