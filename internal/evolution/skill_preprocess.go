package evolution

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var (
	templateVarRe = regexp.MustCompile(`\$\{(COVO_SKILL_DIR|COVO_SESSION_ID)\}`)
	inlineShellRe = regexp.MustCompile("!`([^`\n]+)`")
)

const maxInlineShellOutput = 4000

type PreprocessConfig struct {
	SkillDir  string
	SessionID string
	WorkDir   string
	Timeout   time.Duration
}

func PreprocessSkillContent(content string, cfg PreprocessConfig) string {
	if content == "" {
		return content
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}

	content = substituteTemplateVars(content, cfg.SkillDir, cfg.SessionID)
	content = evaluateInlineShell(content, cfg.WorkDir, cfg.Timeout)
	return content
}

func substituteTemplateVars(content, skillDir, sessionID string) string {
	return templateVarRe.ReplaceAllStringFunc(content, func(match string) string {
		switch match {
		case "${COVO_SKILL_DIR}":
			if skillDir != "" {
				return skillDir
			}
		case "${COVO_SESSION_ID}":
			if sessionID != "" {
				return sessionID
			}
		}
		return match
	})
}

func evaluateInlineShell(content, workDir string, timeout time.Duration) string {
	return inlineShellRe.ReplaceAllStringFunc(content, func(match string) string {
		cmd := inlineShellRe.FindStringSubmatch(match)
		if len(cmd) < 2 {
			return match
		}

		out, err := runShellCommand(cmd[1], workDir, timeout)
		if err != nil {
			return "[inline-shell error: " + err.Error() + "]"
		}
		if len(out) > maxInlineShellOutput {
			out = out[:maxInlineShellOutput] + "...[truncated]"
		}
		return out
	})
}

func runShellCommand(command, workDir string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = workDir
	cmd.Env = os.Environ()

	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", ctx.Err()
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if stderr != "" {
				return stderr, nil
			}
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
