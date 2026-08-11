package approval

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// --- Command normalization ---

// normalizeCommand strips ANSI escape sequences, removes null bytes,
// and normalizes Unicode NFKC to defeat obfuscation attempts.
func normalizeCommand(command string) string {
	command = stripAnsi(command)
	command = strings.ReplaceAll(command, "\x00", "")
	command = norm.NFKC.String(command)
	return command
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\].*?(?:\x07|\x1b\\)|\x1b[PX^_].*?\x1b\\|\x1b[@-Z\\-\^_]`)

func stripAnsi(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// --- Pre-compiled patterns ---

type dangerousPattern struct {
	re          *regexp.Regexp
	description string
}

func mustCompile(pattern string) *regexp.Regexp {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		panic(fmt.Sprintf("approval: invalid pattern %q: %v", pattern, err))
	}
	return re
}

// _cmdp returns the command-position anchor pattern fragment.
func _cmdp() string {
	return `(?:^|[;&|` + "`" + `\n]|\$\()\s*(?:sudo\s+(?:-[^\s]+\s+)*)?\s*`
}

// --- Hardline patterns ---

var hardlinePatterns = []dangerousPattern{
	{mustCompile(`\brm\s+(-[^\s]*\s+)*(/|/\*|/ \*)(\s|$)`), "recursive delete of root filesystem"},
	{mustCompile(`\brm\s+(-[^\s]*\s+)*(/home|/home/\*|/root|/root/\*|/etc|/etc/\*|/usr|/usr/\*|/var|/var/\*|/bin|/bin/\*|/sbin|/sbin/\*|/boot|/boot/\*|/lib|/lib/\*)(\s|$)`), "recursive delete of system directory"},
	{mustCompile(`\brm\s+(-[^\s]*\s+)*(~|\$HOME)(/?|/\*)?(\s|$)`), "recursive delete of home directory"},
	{mustCompile(`\bmkfs(\.[a-z0-9]+)?\b`), "format filesystem (mkfs)"},
	{mustCompile(`\bdd\b[^\n]*\bof=/dev/(sd|nvme|hd|mmcblk|vd|xvd)[a-z0-9]*`), "dd to raw block device"},
	{mustCompile(`>\s*/dev/(sd|nvme|hd|mmcblk|vd|xvd)[a-z0-9]*\b`), "redirect to raw block device"},
	{mustCompile(`:\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`), "fork bomb"},
	{mustCompile(`\bkill\s+(-[^\s]+\s+)*-1\b`), "kill all processes"},
	{mustCompile(_cmdp() + `(shutdown|reboot|halt|poweroff)\b`), "system shutdown/reboot"},
	{mustCompile(_cmdp() + `init\s+[06]\b`), "init 0/6 (shutdown/reboot)"},
	{mustCompile(_cmdp() + `systemctl\s+(poweroff|reboot|halt|kexec)\b`), "systemctl poweroff/reboot"},
	{mustCompile(_cmdp() + `telinit\s+[06]\b`), "telinit 0/6 (shutdown/reboot)"},
}

func DetectHardline(command string) (bool, string) {
	normalized := strings.ToLower(normalizeCommand(command))
	for _, p := range hardlinePatterns {
		if p.re.MatchString(normalized) {
			return true, p.description
		}
	}
	return false, ""
}

// --- Sudo stdin guard ---

var sudoStdinRe = mustCompile(`(?:^|[;&|` + "`" + `\n]|&&|\|\||\$\()\s*sudo\s+-S\b`)

func CheckSudoStdin(command string) (bool, string) {
	normalized := strings.ToLower(normalizeCommand(command))
	if sudoStdinRe.MatchString(normalized) {
		return true, "sudo password guessing via stdin (sudo -S)"
	}
	return false, ""
}

// --- Dangerous patterns ---

var dangerousPatterns = []dangerousPattern{
	{mustCompile(`\brm\s+(-[^\s]*\s+)*/`), "delete in root path"},
	{mustCompile(`\brm\s+-[^\s]*r`), "recursive delete"},
	{mustCompile(`\brm\s+--recursive\b`), "recursive delete (long flag)"},
	{mustCompile(`\bchmod\s+(-[^\s]*\s+)*(777|666|o\+[rwx]*w|a\+[rwx]*w)\b`), "world/other-writable permissions"},
	{mustCompile(`\bchmod\s+--recursive\b`), "recursive chmod (long flag)"},
	{mustCompile(`\bchown\s+(-[^\s]*)?R\s+root`), "recursive chown to root"},
	{mustCompile(`\bchown\s+--recursive\b`), "recursive chown (long flag)"},
	{mustCompile(`\bmkfs\b`), "format filesystem"},
	{mustCompile(`\bdd\s+.*if=`), "disk copy"},
	{mustCompile(`>\s*/dev/sd`), "write to block device"},
	{mustCompile(`\bDROP\s+(TABLE|DATABASE)\b`), "SQL DROP"},
	{mustCompile(`\bDELETE\s+FROM\b`), "SQL DELETE"}, // see also checkDeleteWithoutWhere
	{mustCompile(`\bTRUNCATE\s+(TABLE)?\s*\w`), "SQL TRUNCATE"},
	{mustCompile(`\bsystemctl\s+(-[^\s]+\s+)*(stop|restart|disable|mask)\b`), "stop/restart system service"},
	{mustCompile(`\bkill\s+-9\s+-1\b`), "kill all processes"},
	{mustCompile(`\bpkill\s+-9\b`), "force kill processes"},
	{mustCompile(`\bkillall\s+(-[^\s]*\s+)*-(9|KILL|SIGKILL)\b`), "force kill processes (killall -KILL)"},
	{mustCompile(`\bkillall\s+(-[^\s]*\s+)*-s\s+(KILL|SIGKILL|9)\b`), "force kill processes (killall -s KILL)"},
	{mustCompile(`\bkillall\s+(-[^\s]*\s+)*-r\b`), "kill processes by regex (killall -r)"},
	{mustCompile(`:\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`), "fork bomb"},
	{mustCompile(`\b(bash|sh|zsh|ksh)\s+-[^\s]*c(\s+|$)`), "shell command via -c flag"},
	{mustCompile(`\b(python[23]?|perl|ruby|node)\s+-[ec]\s+`), "script execution via -e/-c flag"},
	{mustCompile(`\b(curl|wget)\b.*\|\s*(?:[/\w]*/)?(?:ba)?sh(?:\s|$|-c)`), "pipe remote content to shell"},
	{mustCompile(`\b(bash|sh|zsh|ksh)\s+<\s*<?\s*\(\s*(curl|wget)\b`), "execute remote script via process substitution"},
	{mustCompile(`\bxargs\s+.*\brm\b`), "xargs with rm"},
	{mustCompile(`\bfind\b.*-exec(?:dir)?\s+(/\S*/)?rm\b`), "find -exec/-execdir rm"},
	{mustCompile(`\bfind\b.*-delete\b`), "find -delete"},
	{mustCompile(`\bdocker\s+compose\s+(restart|stop|kill|down)\b`), "docker compose restart/stop/kill/down"},
	{mustCompile(`\bdocker\s+(restart|stop|kill)\b`), "docker restart/stop/kill"},
	{mustCompile(`\b(pkill|killall)\b`), "process kill (pkill/killall)"},
	{mustCompile(`\bkill\b.*\$\(\s*pgrep\b`), "kill process via pgrep expansion"},
	{mustCompile(`\bkill\b.*` + "`" + `\s*pgrep\b`), "kill process via backtick pgrep expansion"},
	{mustCompile(`\b(cp|mv|install)\b`), "file copy/move into system path"}, // see also checkSystemPathWrite
	{mustCompile(`\bsed\s+-[^\s]*i`), "in-place edit"},                      // see also checkSystemPathEdit
	{mustCompile(`\btee\b`), "file write via tee"},                          // see also checkSystemPathWrite
	{mustCompile(`\b(python[23]?|perl|ruby|node)\s+<<`), "script execution via heredoc"},
	{mustCompile(`\bgit\s+reset\s+--hard\b`), "git reset --hard"},
	{mustCompile(`\bgit\s+push\b.*--force\b`), "git force push"},
	{mustCompile(`\bgit\s+push\b.*-f\b`), "git force push short flag"},
	{mustCompile(`\bgit\s+clean\s+-[^\s]*f`), "git clean with force"},
	{mustCompile(`\bgit\s+branch\s+-D\b`), "git branch force delete"},
	{mustCompile(`\bchmod\s+\+x\b.*[;&|]+\s*\./`), "chmod +x followed by immediate execution"},
	{mustCompile(`\bsudo\b[^;|&\n]*?\s+(?:-s\b|--stdin\b|-a\b|--askpass\b)`), "sudo with privilege flag"},
	{mustCompile(`\bsudo\b[^;|&\n]*?\s+-[a-z]*[sa][a-z]*\b`), "sudo with combined-flag privilege escalation"},
}

// systemPaths are sensitive paths that should trigger warnings when written to.
var systemPathRE = mustCompile(`(?:/etc/|/private/etc/|/usr/local/etc/|\.ssh/|\.hermes/)`)

// DetectDangerous returns (isDangerous, patternKey, description).
func DetectDangerous(command string) (bool, string, string) {
	normalized := strings.ToLower(normalizeCommand(command))

	for _, p := range dangerousPatterns {
		if p.re.MatchString(normalized) {
			desc := p.description

			// Secondary checks for patterns that need context
			switch p.description {
			case "SQL DELETE":
				if !checkDeleteWithoutWhere(normalized) {
					continue // has WHERE clause, not dangerous
				}
				desc = "SQL DELETE without WHERE"
			case "file copy/move into system path":
				if !checkSystemPathWrite(normalized) {
					continue
				}
				desc = "copy/move file into system config path"
			case "file write via tee":
				if !checkSystemPathWrite(normalized) {
					continue
				}
				desc = "overwrite file via tee"
			case "in-place edit":
				if !checkSystemPathEdit(normalized) {
					continue
				}
				desc = "in-place edit of system config"
			}

			return true, desc, desc
		}
	}
	return false, "", ""
}

// checkDeleteWithoutWhere checks if a DELETE FROM statement lacks a WHERE clause.
// Go's RE2 doesn't support lookahead, so we do a two-step check.
func checkDeleteWithoutWhere(command string) bool {
	idx := regexp.MustCompile(`(?i)\bDELETE\s+FROM\b`).FindStringIndex(command)
	if idx == nil {
		return false
	}
	// Check until end of statement (semicolon) or end of string
	rest := command[idx[1]:]
	if semiIdx := strings.IndexByte(rest, ';'); semiIdx >= 0 {
		rest = rest[:semiIdx]
	}
	// WHERE may appear on same or subsequent line within the statement
	return !regexp.MustCompile(`(?i)\bWHERE\b`).MatchString(rest)
}

// checkSystemPathWrite checks if a cp/mv/install/tee/redirect targets a system path.
func checkSystemPathWrite(command string) bool {
	return systemPathRE.MatchString(command)
}

// checkSystemPathEdit checks if a sed -i targets a system path.
func checkSystemPathEdit(command string) bool {
	// sed -i ... /etc/... or sed -i ... ~/.ssh/...
	return systemPathRE.MatchString(command)
}
