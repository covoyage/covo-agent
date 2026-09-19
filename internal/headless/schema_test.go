package headless

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseOutputSchemaInline(t *testing.T) {
	schema, err := ParseOutputSchema(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if schema["type"] != "object" {
		t.Fatalf("type = %v", schema["type"])
	}
}

func TestParseOutputSchemaFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(path, []byte(`{"type":"object"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	schema, err := ParseOutputSchema(path)
	if err != nil {
		t.Fatal(err)
	}
	if schema["type"] != "object" {
		t.Fatalf("type = %v", schema["type"])
	}
}

func TestParseOutputSchemaRejectsArray(t *testing.T) {
	if _, err := ParseOutputSchema("[]"); err == nil {
		t.Fatal("expected error")
	}
}

func TestExtractJSONPayloadStripsFence(t *testing.T) {
	got := ExtractJSONPayload("```json\n{\"answer\":\"ok\"}\n```")
	if got != `{"answer":"ok"}` {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeOutputFormat(t *testing.T) {
	if NormalizeOutputFormat("stream-json") != "streaming-json" {
		t.Fatal("alias mapping failed")
	}
	if NormalizeOutputFormat("") != "text" {
		t.Fatal("empty should be text")
	}
}
