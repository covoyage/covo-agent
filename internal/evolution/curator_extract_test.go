package evolution

import (
	"strings"
	"testing"
)

// TestAuthoringStandards_ContainsKeyRules verifies that the authoring
// standards injected into ExtractionPrompt cover the essential house-style
// rules distilled from skill-authoring/SKILL.md.
func TestAuthoringStandards_ContainsKeyRules(t *testing.T) {
	tests := []struct {
		name    string
		substr  string
		comment string
	}{
		{"body excludes frontmatter", "Do NOT include frontmatter in the body", "LLM must know body starts with # Title, not ---"},
		{"description style", `"Use when ..."`, "Description must start with trigger phrase"},
		{"description hard limit", "≤1024 chars", "LLM must know the hard limit to avoid rejection"},
		{"name format", "lowercase with hyphens", "Name format rule"},
		{"overview section", "## Overview", "Peer-matched structure: Overview"},
		{"when to use section", "## When to Use", "Peer-matched structure: When to Use"},
		{"pitfalls section", "## Common Pitfalls", "Peer-matched structure: Common Pitfalls"},
		{"verification section", "## Verification Checklist", "Peer-matched structure: Verification Checklist"},
		{"tool framing read_file", `"read_file" not "cat"`, "Tool name framing: read_file"},
		{"tool framing search_files", `"search_files" not "grep"`, "Tool name framing: search_files"},
		{"tool framing edit_block", `"edit_block" not "sed"`, "Tool name framing: edit_block"},
		{"body size target", "8-14k chars", "Body size target range"},
		{"no invention", "never invent", "Quality: prefer verbatim from source"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(authoringStandards, tt.substr) {
				t.Errorf("authoringStandards missing %q\ncomment: %s", tt.substr, tt.comment)
			}
		})
	}
}

// TestExtractionPrompt_InjectsAuthoringStandards verifies that the full
// ExtractionPrompt includes the authoring standards section.
func TestExtractionPrompt_InjectsAuthoringStandards(t *testing.T) {
	if !strings.Contains(ExtractionPrompt, "SKILL.md AUTHORING STANDARDS") {
		t.Error("ExtractionPrompt must contain the authoring standards header")
	}
	if !strings.Contains(ExtractionPrompt, "BODY STRUCTURE") {
		t.Error("ExtractionPrompt must contain the body structure section")
	}
}

// TestExtractionPrompt_JSONExampleReflectsStandards verifies that the JSON
// example in ExtractionPrompt aligns with the authoring standards — the
// description placeholder uses "Use when ..." and the body placeholder
// explicitly says "WITHOUT frontmatter".
func TestExtractionPrompt_JSONExampleReflectsStandards(t *testing.T) {
	if !strings.Contains(ExtractionPrompt, `"description": "Use when <trigger>`) {
		t.Error(`JSON example description field should start with "Use when <trigger>"`)
	}
	if !strings.Contains(ExtractionPrompt, "WITHOUT frontmatter") {
		t.Error(`JSON example body field should say "WITHOUT frontmatter"`)
	}
}
