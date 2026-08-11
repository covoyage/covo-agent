package agent

import (
	"context"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// formatterDef describes a built-in formatter for a language.
type formatterDef struct {
	exts     []string // file extensions (e.g. ".go")
	cmd      string   // executable name
	args     []string // args before file path (e.g. ["-w"])
}

// builtinFormatters are formatters available from standard toolchains.
// Only languages whose formatter ships with the toolchain are included.
var builtinFormatters = []formatterDef{
	{exts: []string{".go"}, cmd: "gofmt", args: []string{"-w"}},
	{exts: []string{".rs"}, cmd: "rustfmt", args: nil},
	{exts: []string{".dart"}, cmd: "dart", args: []string{"format"}},
	{exts: []string{".zig"}, cmd: "zig", args: []string{"fmt"}},
	{exts: []string{".cs"}, cmd: "dotnet", args: []string{"format"}},
}

// autoFormatAfterToolCall returns an AfterToolCall handler that silently
// formats Go (and other toolchain-native) source files after edits.
func (ca *CovoAgent) autoFormatAfterToolCall() func(
	ctx context.Context, tc agentcore.ToolCall, result *agentcore.ToolResult,
) *agentcore.ToolResult {
	return func(ctx context.Context, tc agentcore.ToolCall, result *agentcore.ToolResult) *agentcore.ToolResult {
		if result.Err != nil {
			return nil
		}
		if !lspEditTools[tc.Name] {
			return nil
		}
		filePath := extractFilePathFromArgs(tc.Name, []byte(tc.Arguments))
		if filePath == "" {
			return nil
		}

		lang := formatterForFile(filePath)
		if lang == nil {
			return nil
		}

		// Check if formatter is on PATH
		if _, err := exec.LookPath(lang.cmd); err != nil {
			return nil
		}

		// Run formatter silently
		args := append([]string{}, lang.args...)
		args = append(args, filePath)
		cmd := exec.Command(lang.cmd, args...)
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Run(); err != nil {
			// Log but don't fail the tool call
			if ca.baseCfg.Logger != nil {
				ca.baseCfg.Logger.Debug("auto-format failed",
					slog.String("file", filePath),
					slog.String("formatter", lang.cmd),
					slog.Any("error", err),
				)
			}
		}
		return nil
	}
}

// formatterForFile finds the formatter definition for a given file path.
func formatterForFile(path string) *formatterDef {
	ext := strings.ToLower(filepath.Ext(path))
	for _, f := range builtinFormatters {
		for _, e := range f.exts {
			if ext == e {
				return &f
			}
		}
	}
	return nil
}
