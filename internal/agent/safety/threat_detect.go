// Package agent provides runtime threat detection for tool calls.
// When the agent is about to execute a tool, threat_detect checks the tool
// name, arguments, and call sequence against known threat patterns.
package safety

import (
	"regexp"
	"strings"
)

// ThreatCategory classifies the severity of a detected threat.
type ThreatCategory string

const (
	ThreatSafe       ThreatCategory = "safe"
	ThreatSuspicious ThreatCategory = "suspicious"
	ThreatDangerous  ThreatCategory = "dangerous"
	ThreatCritical   ThreatCategory = "critical"
)

// ThreatResult is the outcome of a threat pattern check.
type ThreatResult struct {
	Category    ThreatCategory
	PatternID   string
	Description string
	Matched     string // the matched substring that triggered the detection
}

// ---------------------------------------------------------------------------
// Pre-compiled regex patterns (initialised at package init)
// ---------------------------------------------------------------------------

// criticalPatterns match commands that are clearly destructive or malicious.
var criticalPatterns = []struct {
	id    string
	desc  string
	regex *regexp.Regexp
}{
	{
		id:   "FORK_BOMB",
		desc: "Fork bomb detected",
		regex: regexp.MustCompile(
			`:\(\)\s*\{[^}]*:\|[^}]*:[^}]*\}|:\(\s*\)\s*\{[^}]*:\|:`,
		),
	},
	{
		id:   "RM_ROOTFS_NOPRESERVE",
		desc: "Attempt to delete root filesystem (rm -rf / or --no-preserve-root bypass)",
		regex: regexp.MustCompile(
			`rm\s+(?:-[^\s]*[rf][^\s]*\s+)+(?:--no-preserve-root\s+)*/|rm\s+-[^\s]*[rf][^\s]*\s+(?:--no-preserve-root\s+)?/`,
		),
	},
	{
		id:    "MAKEFS",
		desc:  "Filesystem creation command (mkfs.*) — potential data destruction",
		regex: regexp.MustCompile(`\bmkfs\.\S+`),
	},
	{
		id:    "DD_DISK_WRITE",
		desc:  "Direct disk write via dd — potential device destruction",
		regex: regexp.MustCompile(`\bdd\s+.*\bof=/dev/`),
	},
	{
		id:    "DISK_OVERWRITE",
		desc:  "Direct write to block device",
		regex: regexp.MustCompile(`>\s*/dev/sd[a-z]`),
	},
}

// cmdDangerousPatterns match commands that are very likely malicious.
var cmdDangerousPatterns = []struct {
	id    string
	desc  string
	regex *regexp.Regexp
}{
	{
		id:    "REVERSE_SHELL_DEVTCP",
		desc:  "Reverse shell via /dev/tcp (bash built-in)",
		regex: regexp.MustCompile(`/dev/tcp/`),
	},
	{
		id:   "DOWNLOAD_PIPE_SHELL",
		desc: "Download and pipe to shell (wget/curl ... | sh/bash)",
		regex: regexp.MustCompile(
			`(?:wget|curl)\s+.*\s*(?:\|\s*(?:sh|bash|/bin/sh|/bin/bash))`,
		),
	},
	{
		id:    "NC_REVERSE_SHELL",
		desc:  "Netcat reverse shell or backdoor listener",
		regex: regexp.MustCompile(`\bnc\s+(?:.*-e\s+(?:/bin/|/usr/bin/)|-l\s+-p\s|-\w*lp\s)`),
	},
	{
		id:   "PYTHON_REVERSE_SHELL",
		desc: "Python-based reverse shell via socket",
		regex: regexp.MustCompile(
			`python\d*\s+-c\s+['\"].*import\s+(?:socket|subprocess|os|pty)`,
		),
	},
	{
		id:   "CHMOD_777_RECURSIVE",
		desc: "Recursive world-writable permissions on root",
		regex: regexp.MustCompile(
			`chmod\s+(?:-R\s+)?777\s+(?:/|\$HOME)`,
		),
	},
	{
		id:   "RM_RF_ROOT",
		desc: "Recursive force delete on root or home",
		regex: regexp.MustCompile(
			`rm\s+(?:-[^\s]*[rf][^\s]*\s+)+\s*(?:/\s*$|~/\s*$|\$HOME)`,
		),
	},
}

// suspiciousPatterns match commands that warrant attention but might be legitimate.
var suspiciousPatterns = []struct {
	id    string
	desc  string
	regex *regexp.Regexp
}{
	{
		id:    "PIPE_CHAIN",
		desc:  "Command chaining via pipe",
		regex: regexp.MustCompile(`\b(?:curl|wget|nc|telnet)\b.*\|`),
	},
	{
		id:    "CHMOD_777",
		desc:  "World-writable permissions set",
		regex: regexp.MustCompile(`\bchmod\s+.*777\b`),
	},
	{
		id:    "CURL_DOWNLOAD",
		desc:  "curl/wget download with output to file",
		regex: regexp.MustCompile(`(?:curl|wget)\s+.*\s+-[Oo]\s`),
	},
}

// execToolNames is the set of tool names considered execution-capable.
var execToolNames = map[string]bool{
	"exec": true, "bash": true, "terminal": true,
	"shell": true, "run": true, "execute": true,
	"command": true, "sh": true, "cmd": true,
}

// writeToolNames is the set of tool names that write/modify files.
var writeToolNames = map[string]bool{
	"write": true, "edit": true, "patch": true,
	"replace": true, "create": true, "save": true,
	"append": true, "overwrite": true, "file_write": true,
	"multi_tool": true,
}

// readToolNames is the set of tool names that read files.
var readToolNames = map[string]bool{
	"read": true, "cat": true, "open": true,
	"get_file": true, "file_read": true, "view": true,
}

// downloadToolNames is the set of tool names that fetch remote content.
var downloadToolNames = map[string]bool{
	"curl": true, "wget": true, "fetch": true,
	"download": true, "http_get": true, "request": true,
}

// argKeys are the argument keys that might contain a shell command.
var argKeys = map[string]bool{
	"command": true, "cmd": true, "script": true,
	"code": true, "input": true, "exec": true,
	"shell": true, "args": true, "argv": true,
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// DetectToolThreat checks a single tool call (name + arguments) against known
// threat patterns. Returns nil when no threat is detected.
func DetectToolThreat(toolName string, args map[string]string) *ThreatResult {
	toolName = strings.ToLower(strings.TrimSpace(toolName))

	// 1. Check the tool name itself — some tools are inherently dangerous.
	if r := checkDangerousToolName(toolName); r != nil {
		return r
	}

	// 2. Walk each argument value looking for dangerous shell commands.
	for key, value := range args {
		if !isArgRelevant(key) {
			continue
		}
		if r := DetectCommandThreat(value); r != nil {
			return r
		}
	}

	// 3. Check for the download-then-execute pattern within a single
	//    tool call (e.g. an exec tool whose single argument is a curl | sh).
	combined := combineArgs(args)
	if r := detectCombinedThreat(combined); r != nil {
		return r
	}

	return nil
}

// DetectCommandThreat checks a raw shell command string for dangerous
// substrings. Returns nil when no threat is detected.
func DetectCommandThreat(command string) *ThreatResult {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}

	// Check critical patterns first — highest severity wins.
	for _, p := range criticalPatterns {
		match := p.regex.FindString(command)
		if match != "" {
			return &ThreatResult{
				Category:    ThreatCritical,
				PatternID:   p.id,
				Description: p.desc,
				Matched:     match,
			}
		}
	}

	// Then dangerous patterns.
	for _, p := range cmdDangerousPatterns {
		match := p.regex.FindString(command)
		if match != "" {
			return &ThreatResult{
				Category:    ThreatDangerous,
				PatternID:   p.id,
				Description: p.desc,
				Matched:     match,
			}
		}
	}

	// Finally suspicious patterns.
	for _, p := range suspiciousPatterns {
		match := p.regex.FindString(command)
		if match != "" {
			return &ThreatResult{
				Category:    ThreatSuspicious,
				PatternID:   p.id,
				Description: p.desc,
				Matched:     match,
			}
		}
	}

	return nil
}

// DetectSequenceThreat checks a sequence of recently-called tool names for
// patterns that are dangerous when composed. recentTools should be ordered
// from oldest to newest.
func DetectSequenceThreat(recentTools []string) *ThreatResult {
	if len(recentTools) < 2 {
		return nil
	}

	// Normalize all tool names.
	norms := make([]string, len(recentTools))
	for i, t := range recentTools {
		norms[i] = strings.ToLower(strings.TrimSpace(t))
	}

	// 1. exec tool + download tool in sequence (download-and-execute chain).
	//    The pattern is: a download tool is followed by an exec tool, or vice
	//    versa in the same sequence window.
	if lastExecIdx := lastIndexInSet(norms, execToolNames); lastExecIdx > 0 {
		if inSet(norms[lastExecIdx-1], downloadToolNames) {
			return &ThreatResult{
				Category:    ThreatDangerous,
				PatternID:   "SINK_DOWNLOAD_EXEC_CHAIN",
				Description: "Download tool followed by execution tool — potential download-and-execute chain",
				Matched:     norms[lastExecIdx-1] + " → " + norms[lastExecIdx],
			}
		}
	}

	// 2. exec tool that received a destructive command (rm -rf).
	if lastExecIdx := lastIndexInSet(norms, execToolNames); lastExecIdx > 0 {
		if lastExecIdx+1 < len(norms) {
			// Check the "command" pattern: exec → rm-style destructive.
			// This is a heuristic; actual command content is checked by
			// DetectToolThreat at call time. Here we look at tool-name
			// adjacency.
			_ = lastExecIdx // used above
		}
	}

	// 3. read + write/edit in sequence (exfiltration-then-overwrite).
	// reading code then editing is the core coding loop — allow silently.
	_ = hasReadThenWrite(norms) // internal tracking removed; too noisy

	// 4. write/edit followed by exec (write-then-execute — script injection).
	// Disabled: writing code then running it is the core development loop.
	_ = hasWriteThenExec(norms)

	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// checkDangerousToolName returns a ThreatResult for inherently dangerous tool
// names or nil.
func checkDangerousToolName(name string) *ThreatResult {
	// "sudo" or "root" tools imply privilege escalation.
	if name == "sudo" || name == "su" || name == "root" {
		return &ThreatResult{
			Category:    ThreatDangerous,
			PatternID:   "PRIV_ESCALATION_TOOL",
			Description: "Privilege-escalation tool called: " + name,
			Matched:     name,
		}
	}
	return nil
}

// isArgRelevant returns true when the argument key is likely to contain shell
// commands.
func isArgRelevant(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return argKeys[key]
}

// combineArgs joins all argument values into one string so we can run
// regex patterns across the entire call content.
func combineArgs(args map[string]string) string {
	var b strings.Builder
	for _, v := range args {
		b.WriteString(v)
		b.WriteByte(' ')
	}
	return b.String()
}

// detectCombinedThreat checks the combined argument string for patterns that
// span multiple arguments.
func detectCombinedThreat(combined string) *ThreatResult {
	return DetectCommandThreat(combined)
}

// inSet returns true if s is a member of set.
func inSet(s string, set map[string]bool) bool { return set[s] }

// lastIndexInSet returns the index (0-based) of the last element in slice that
// is a member of set, or -1 if none match.
func lastIndexInSet(slice []string, set map[string]bool) int {
	idx := -1
	for i, s := range slice {
		if set[s] {
			idx = i
		}
	}
	return idx
}

// hasReadThenWrite returns true when any read tool is followed by a write tool
// later in the sequence.
func hasReadThenWrite(norms []string) bool {
	sawRead := false
	for _, t := range norms {
		if isReadTool(t) || t == "multi_tool" {
			sawRead = true
		}
		if sawRead && isWriteTool(t) {
			return true
		}
	}
	return false
}

// hasWriteThenExec returns true when any write tool is followed by an exec
// tool later in the sequence.
func hasWriteThenExec(norms []string) bool {
	sawWrite := false
	for _, t := range norms {
		if isWriteTool(t) {
			sawWrite = true
		}
		if sawWrite && isExecTool(t) {
			return true
		}
	}
	return false
}

func isExecTool(name string) bool  { return execToolNames[name] }
func isWriteTool(name string) bool { return writeToolNames[name] }
func isReadTool(name string) bool  { return readToolNames[name] }
