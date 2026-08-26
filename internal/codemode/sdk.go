package codemode

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

//go:embed sdk_helpers.go.txt
var sdkHelpersSrc string

// ToolInfo describes a tool available to generated code.
type ToolInfo struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// ToolsFromDefinitions extracts ToolInfo from agentcore tool definitions.
func ToolsFromDefinitions(defs []agentcore.ToolDefinition) []ToolInfo {
	tools := make([]ToolInfo, len(defs))
	for i, d := range defs {
		tools[i] = ToolInfo{
			Name:        d.Name,
			Description: d.Description,
			Parameters:  d.Parameters,
		}
	}
	return tools
}

// GenerateSDK produces a Go source file containing tool SDK functions
// and the main() entry point for the generated code.
func GenerateSDK(tools []ToolInfo, userCode string) string {
	var b strings.Builder

	b.WriteString("package main\n\n")
	b.WriteString("import (\n")
	b.WriteString("\t\"bufio\"\n")
	b.WriteString("\t\"encoding/json\"\n")
	b.WriteString("\t\"fmt\"\n")
	b.WriteString("\t\"os\"\n")
	b.WriteString(")\n\n")

	// SDK helper functions (from embedded file)
	b.WriteString(sdkHelpersSrc)
	b.WriteString("\n")

	// Generate tool-specific functions
	for _, t := range tools {
		b.WriteString(generateToolFunc(t))
		b.WriteString("\n")
	}

	// Escape backticks in user code
	escaped := strings.ReplaceAll(userCode, "`", "` + \"`\" + `")

	b.WriteString("// --- user code ---\n")
	b.WriteString("func main() {\n")
	b.WriteString("\t_scanner = bufio.NewScanner(os.Stdin)\n")
	b.WriteString("\t_scanner.Buffer(make([]byte, 1024*1024), 1024*1024)\n")
	b.WriteString(escaped)
	b.WriteString("\n}\n")

	return b.String()
}

// generateToolFunc produces a Go function for one tool.
func generateToolFunc(t ToolInfo) string {
	funcName := toolNameToGoFunc(t.Name)
	paramsJSON := summarizeParams(t.Parameters)
	desc := cleanComment(t.Description)

	return fmt.Sprintf(`// %s — %s
// Parameters: %s
func %s(args map[string]any) (any, error) {
	return ToolCall(%q, args)
}
`, funcName, desc, paramsJSON, funcName, t.Name)
}

// toolNameToGoFunc converts a tool name to a valid Go identifier.
func toolNameToGoFunc(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '-'
	})
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}

// summarizeParams produces a compact parameter summary for the doc comment.
func summarizeParams(params map[string]any) string {
	props, _ := params["properties"].(map[string]any)
	required, _ := params["required"].([]any)
	reqSet := make(map[string]bool)
	for _, r := range required {
		if s, ok := r.(string); ok {
			reqSet[s] = true
		}
	}
	var parts []string
	for name, prop := range props {
		pm, _ := prop.(map[string]any)
		typ, _ := pm["type"].(string)
		marker := ""
		if reqSet[name] {
			marker = "*"
		}
		parts = append(parts, fmt.Sprintf("%s%s:%s", name, marker, typ))
	}
	return strings.Join(parts, ", ")
}

// cleanComment truncates a description for use in a Go comment.
func cleanComment(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return s
}
