package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/covoyage/covo-agent/internal/agent/safety"
	"github.com/covoyage/covonaut/agentcore"
)

type BashSafetyGate struct{}

// cmdStart anchors a pattern to the start of a command — line start, after a
// shell separator (; & | ` newline), or inside $(...) — optionally preceded by
// sudo. This prevents command names (shutdown, reboot, kill, ...) from being
// matched when they merely appear as substrings in arguments, strings, comments,
// or code identifiers rather than as the command actually being executed.
const cmdStart = `(?:^|[;&|` + "`" + `\n]|\$\()\s*(?:sudo\s+(?:-\S+\s+)*)?`

var dangerousPatterns = []dangerousPattern{
	{
		regex:   regexp.MustCompile(cmdStart + `rm\s+(-[a-zA-Z]*[rf][a-zA-Z]*|--recursive|--force)\b`),
		message: "recursive file deletion (rm -rf or similar)",
	},
	{
		regex:   regexp.MustCompile(cmdStart + `sudo\b`),
		message: "privilege escalation via sudo",
	},
	{
		regex:   regexp.MustCompile(cmdStart + `chmod\s+.*777\b`),
		message: "setting world-writable permissions (chmod 777)",
	},
	{
		regex:   regexp.MustCompile(cmdStart + `chown\s+.*777\b`),
		message: "setting world-writable ownership via chown",
	},
	{
		regex:   regexp.MustCompile(cmdStart + `dd\s+if=`),
		message: "raw disk copy (dd if=)",
	},
	{
		regex:   regexp.MustCompile(cmdStart + `mkfs\.`),
		message: "filesystem creation (mkfs)",
	},
	{
		regex:   regexp.MustCompile(cmdStart + `fdisk\b`),
		message: "disk partitioning (fdisk)",
	},
	{
		regex:   regexp.MustCompile(`\b> \/dev\/(sd|nvme|hd|xvd|vd)\w*\b`),
		message: "writing directly to block device",
	},
	{
		regex:   regexp.MustCompile(`\b:\s*\(\s*\)\s*\{`),
		message: "shell fork bomb pattern",
	},
	{
		regex:   regexp.MustCompile(`\bcurl\s+.*\|\s*(ba)?sh\b`),
		message: "piping curl output to shell (curl | sh)",
	},
	{
		regex:   regexp.MustCompile(`\bwget\s+.*\|\s*(ba)?sh\b`),
		message: "piping wget output to shell",
	},
	{
		regex:   regexp.MustCompile(cmdStart + `git\s+push\s+--force\b`),
		message: "force push to remote",
	},
	{
		regex:   regexp.MustCompile(cmdStart + `(crontab|at)\s+-`),
		message: "modifying scheduled tasks",
	},
	{
		regex:   regexp.MustCompile(cmdStart + `shutdown\b`),
		message: "system shutdown",
	},
	{
		regex:   regexp.MustCompile(cmdStart + `reboot\b`),
		message: "system reboot",
	},
	{
		regex:   regexp.MustCompile(cmdStart + `kill\s+-9\b`),
		message: "force kill (SIGKILL)",
	},
	{
		regex:   regexp.MustCompile(cmdStart + `(killall|pkill)\b`),
		message: "process killing",
	},
	{
		regex:   regexp.MustCompile(`\b> \/~\/\.(ssh|aws|kube|docker|git|config|netrc|env)\b`),
		message: "overwriting sensitive config files",
	},
	{
		regex:   regexp.MustCompile(cmdStart + `chmod\s+(-R\s+)?[0-7]*[27]77\b`),
		message: "setting permissive permissions",
	},
}

type dangerousPattern struct {
	regex   *regexp.Regexp
	message string
}

func NewBashSafetyGate() *BashSafetyGate {
	return &BashSafetyGate{}
}

func (bs *BashSafetyGate) Check(command string) (blocked bool, reason string) {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return false, ""
	}

	// Scan the dangerous-pattern denylist FIRST — even when the leading token
	// is read-only. A compound command that starts with a safe command (e.g.
	// `echo ok && rm -rf /`, `true; shutdown`, `cat f | sh`) must not bypass
	// the denylist. The command-position-anchored patterns prevent false
	// positives on dangerous words that only appear as arguments.
	for _, p := range dangerousPatterns {
		if p.regex.MatchString(cmd) {
			return true, p.message
		}
	}

	return false, ""
}

var bashToolParamNames = map[string]string{
	"bash":    "command",
	"process": "command",
}

func (ca *CovoAgent) bashSafetyBeforeHook() agentcore.BeforeHook {
	gate := NewBashSafetyGate()

	return func(ctx context.Context, hc *agentcore.HookContext) error {
		paramKey, ok := bashToolParamNames[hc.ToolName]
		if !ok {
			return nil
		}

		var args map[string]interface{}
		if err := json.Unmarshal(hc.Arguments, &args); err != nil {
			return nil
		}

		command, _ := args[paramKey].(string)
		if command == "" {
			return nil
		}

		blocked, reason := gate.Check(command)
		if blocked {
			return fmt.Errorf(
				"bash command blocked by safety gate: %s. "+
					"If you are sure this is safe, use a different approach or break the command into smaller steps.",
				reason,
			)
		}

		// --- Runtime threat detection for bash commands ---
		if result := safety.DetectCommandThreat(command); result != nil {
			if result.Category == safety.ThreatDangerous || result.Category == safety.ThreatCritical {
				return fmt.Errorf("command blocked by threat detection: %s", result.Description)
			}
		}
		// --- end threat detection ---

		// Prior-read enforcement for shell-based overwrites: prevent using bash
		// redirection/heredoc/tee to overwrite an existing file that has not
		// been read this session, which would otherwise bypass the file-tool
		// prior-read hook.
		if ca.readTracker != nil {
			if path, blocked := ca.readTracker.ShellWritePriorReadViolation(command, ca.workDir); blocked {
				return fmt.Errorf(
					"%q has not been read in this session yet. "+
						"Read the file first with the read tool, then edit it — do NOT use bash "+
						"redirection (>, tee, heredoc) to overwrite files you have not read.",
					path,
				)
			}
		}

		return nil
	}
}
