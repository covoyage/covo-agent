package main

import (
	"testing"

	"github.com/covoyage/covo-agent/internal/cli"
)

func TestFilterProviderModelsSearchesIDNameAndDescription(t *testing.T) {
	models := []cli.ProviderModel{
		{ID: "openai/gpt-5.6", Name: "GPT-5.6", Description: "Current OpenAI model"},
		{ID: "anthropic/claude-sonnet-4", Name: "Claude Sonnet 4", Description: "Strong coding model"},
		{ID: "google/gemini-2.5-flash", Name: "Gemini Flash", Description: "Low latency"},
	}

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "id", query: "claude", want: []string{"anthropic/claude-sonnet-4"}},
		{name: "name", query: "flash", want: []string{"google/gemini-2.5-flash"}},
		{name: "description", query: "coding", want: []string{"anthropic/claude-sonnet-4"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterProviderModels(models, tt.query, 10)
			if len(got) != len(tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("expected %v, got %v", tt.want, got)
				}
			}
		})
	}
}

func TestFilterProviderModelsLimit(t *testing.T) {
	models := []cli.ProviderModel{
		{ID: "a/model"},
		{ID: "b/model"},
		{ID: "c/model"},
	}

	got := filterProviderModels(models, "", 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 models, got %d: %v", len(got), got)
	}
}
