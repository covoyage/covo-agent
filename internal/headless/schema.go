package headless

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ParseOutputSchema loads a JSON Schema from an inline object or a file path.
// Empty spec returns (nil, nil). The document must decode to a JSON object.
func ParseOutputSchema(spec string) (map[string]any, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}

	raw := spec
	if !strings.HasPrefix(spec, "{") {
		data, err := os.ReadFile(spec)
		if err != nil {
			return nil, fmt.Errorf("read output schema: %w", err)
		}
		raw = string(data)
	}

	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("parse output schema: %w", err)
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("output schema must be a JSON object")
	}
	return obj, nil
}

// ExtractJSONPayload returns the JSON object/array payload from a model
// answer, stripping optional markdown fences.
func ExtractJSONPayload(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return s
	}
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSpace(s)
	if newline := strings.IndexByte(s, '\n'); newline >= 0 {
		lang := strings.TrimSpace(s[:newline])
		if lang == "" || strings.EqualFold(lang, "json") {
			s = s[newline+1:]
		}
	}
	if idx := strings.LastIndex(s, "```"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// NormalizeOutputFormat maps CLI aliases onto the canonical format names.
func NormalizeOutputFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "text":
		return "text"
	case "json":
		return "json"
	case "streaming-json", "stream-json":
		return "streaming-json"
	default:
		return strings.TrimSpace(format)
	}
}
