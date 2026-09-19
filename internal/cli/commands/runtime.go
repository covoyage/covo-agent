package commands

// RunOptions carries the resolved root-level flags that drive the interactive
// TUI and one-shot execution paths. The root command builds it from parsed
// Cobra flags so the commands package does not depend on the CLI flag layer.
type RunOptions struct {
	Mode               string
	Provider           string
	Model              string
	Yolo               bool
	Permission         string
	AddDir             []string
	Print              string
	JSON               bool
	OutputFormat       string
	OutputSchema       string
	SystemPrompt       string
	AppendSystemPrompt string
	SessionID          string
	Continue           bool
	ShowSessionPicker  bool
	InitialPrompt      string
}
