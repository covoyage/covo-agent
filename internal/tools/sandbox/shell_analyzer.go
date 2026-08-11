package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ============================================================
// Shell Analyzer — parse commands for permission checking
// ============================================================

type ShellRisk int

const (
	RiskSafe   ShellRisk = iota // read-only or no side effects
	RiskWrite  ShellRisk = iota // modifies files
	RiskDanger ShellRisk = iota // destructive or system-wide
)

type ShellAnalysis struct {
	Command    string    `json:"command"`
	Risk       ShellRisk `json:"risk"`
	Files      []string  `json:"files"`      // files the command will touch
	Dirs       []string  `json:"dirs"`       // directories affected
	Suspicious []string  `json:"suspicious"` // dangerous patterns found
	Program    string    `json:"program"`    // first word (the command itself)
}

var dangerPatterns = map[string]ShellRisk{
	"rm": RiskDanger, "rmdir": RiskDanger,
	"mv": RiskWrite, "cp": RiskWrite,
	"chmod": RiskDanger, "chown": RiskDanger,
	"sudo": RiskDanger, "su": RiskDanger,
	"kill": RiskDanger, "killall": RiskDanger,
	"dd": RiskDanger, "mkfs": RiskDanger,
	"shutdown": RiskDanger, "reboot": RiskDanger, "halt": RiskDanger,
	"iptables": RiskDanger, "ip6tables": RiskDanger,
	"mount": RiskDanger, "umount": RiskDanger,
	"fdisk": RiskDanger, "parted": RiskDanger,
	"systemctl": RiskDanger, "service": RiskDanger,
}

var dangerSubstrings = []string{
	"rm -rf", "rm -r", "> /dev/", "mkfs.",
	"chmod 777", "chmod -R 777", "chmod 666",
	"> /etc/", ">> /etc/", "/etc/passwd", "/etc/shadow",
	"fork bomb", ":(){ :|:& };:",
	"dd if=/dev/zero", "dd if=/dev/urandom",
	">> ~/.ssh/authorized_keys", "curl.*|.*sh",
	"wget.*|.*sh", "eval", "$()",
}

var writePrograms = map[string]bool{
	"echo": true, "cat": false, ">": true, ">>": true,
	"touch": true, "mkdir": true, "cp": true, "mv": true,
	"tar": true, "zip": true, "unzip": true,
	"git": true, "npm": true, "pip": true, "cargo": true,
	"go": true, "docker": true, "kubectl": true,
	"sed": true, "awk": true, "tee": true,
}

// AnalyzeShellCommand parses a shell command and returns a safety analysis.
func AnalyzeShellCommand(command string) ShellAnalysis {
	cmd := strings.TrimSpace(command)
	a := ShellAnalysis{Command: command}

	// Extract the base program
	parts := shellSplit(cmd)
	if len(parts) > 0 {
		a.Program = filepath.Base(parts[0])
	}

	// Check danger patterns
	for _, pattern := range dangerSubstrings {
		if strings.Contains(strings.ToLower(cmd), strings.ToLower(pattern)) {
			a.Suspicious = append(a.Suspicious, pattern)
		}
	}

	// Classify risk by program
	if risk, ok := dangerPatterns[a.Program]; ok {
		a.Risk = risk
	} else if writePrograms[a.Program] {
		a.Risk = RiskWrite
	} else {
		a.Risk = RiskSafe
	}

	// Elevate risk if suspicious patterns found
	if len(a.Suspicious) > 0 && a.Risk < RiskDanger {
		a.Risk = RiskDanger
	}

	// Extract file/dir paths from arguments
	for _, part := range parts[1:] {
		if isPathLike(part) {
			abs, err := filepath.Abs(part)
			if err == nil {
				if strings.HasSuffix(part, "/") || isDirArg(a.Program, part) {
					a.Dirs = append(a.Dirs, abs)
				} else {
					a.Files = append(a.Files, abs)
				}
			}
		}
	}

	return a
}

// ShellSafeBash returns an analysis for bash -c wrapped commands.
func ShellSafeBash(cmd string) []ShellAnalysis {
	if !strings.HasPrefix(cmd, "bash -c ") && !strings.HasPrefix(cmd, "sh -c ") {
		return []ShellAnalysis{AnalyzeShellCommand(cmd)}
	}

	inner := strings.TrimPrefix(cmd, "bash -c ")
	inner = strings.TrimPrefix(inner, "sh -c ")
	inner = strings.Trim(inner, "'\"")

	// Split compound commands
	var analyses []ShellAnalysis
	for _, sub := range strings.Split(inner, "&&") {
		for _, sub2 := range strings.Split(sub, "||") {
			for _, sub3 := range strings.Split(sub2, ";") {
				trimmed := strings.TrimSpace(sub3)
				if trimmed != "" {
					analyses = append(analyses, AnalyzeShellCommand(trimmed))
				}
			}
		}
	}
	return analyses
}

// IsExternalPath checks if a path is outside the workspace boundary.
func IsExternalPath(workspace, path string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return true // fail-safe: treat as external
	}
	absWS, err := filepath.Abs(workspace)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absWS, absPath)
	if err != nil {
		return true
	}
	return strings.HasPrefix(rel, "..")
}

func shellSplit(cmd string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]

		if inQuote {
			if ch == quoteChar {
				inQuote = false
			} else {
				current.WriteByte(ch)
			}
			continue
		}

		switch ch {
		case '"', '\'':
			inQuote = true
			quoteChar = ch
		case ' ', '\t':
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}

func isPathLike(s string) bool {
	return strings.Contains(s, "/") ||
		strings.HasPrefix(s, ".") ||
		strings.HasPrefix(s, "~") ||
		strings.HasPrefix(s, "/") ||
		isKnownExt(s)
}

var knownExts = []string{
	".go", ".py", ".js", ".ts", ".rs", ".java", ".rb",
	".json", ".yaml", ".yml", ".toml", ".xml", ".csv",
	".md", ".txt", ".log", ".cfg", ".conf", ".ini",
	".sh", ".bash", ".zsh", ".env", ".gitignore",
	".html", ".css", ".vue", ".jsx", ".tsx",
}

func isKnownExt(s string) bool {
	for _, ext := range knownExts {
		if strings.HasSuffix(s, ext) {
			return true
		}
	}
	return false
}

func isDirArg(program, arg string) bool {
	// Programs where arg typically means directory
	dirPrograms := map[string]bool{
		"cd": true, "mkdir": true, "rmdir": true,
		"ls": true, "cp": true, "mv": true,
	}
	if dirPrograms[program] && (strings.HasSuffix(arg, "/") || !strings.Contains(arg, ".")) {
		return true
	}
	return false
}

// FormatApprovalPrompt generates a human-readable approval message.
func (a *ShellAnalysis) FormatApprovalPrompt() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Command: %s\n", a.Command))
	b.WriteString(fmt.Sprintf("Risk: %s\n", riskLabel(a.Risk)))

	if len(a.Files) > 0 {
		b.WriteString("Files affected:\n")
		for _, f := range a.Files {
			b.WriteString(fmt.Sprintf("  - %s\n", f))
		}
	}

	if len(a.Dirs) > 0 {
		b.WriteString("Directories affected:\n")
		for _, d := range a.Dirs {
			b.WriteString(fmt.Sprintf("  - %s\n", d))
		}
	}

	if len(a.Suspicious) > 0 {
		b.WriteString("⚠️  Suspicious patterns:\n")
		for _, s := range a.Suspicious {
			b.WriteString(fmt.Sprintf("  - %s\n", s))
		}
	}

	return b.String()
}

func riskLabel(r ShellRisk) string {
	switch r {
	case RiskSafe:
		return "safe"
	case RiskWrite:
		return "write"
	case RiskDanger:
		return "⚠️ DANGER"
	default:
		return "unknown"
	}
}
