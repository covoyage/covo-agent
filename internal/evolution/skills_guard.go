package evolution

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/covoyage/covo-agent/internal/security"
)

// ─── Data structures ───────────────────────────────────────────────────────────

// Finding represents a single detected threat match.
type Finding struct {
	PatternID   string `json:"pattern_id"`
	Severity    string `json:"severity"` // critical, high, medium, low
	Category    string `json:"category"` // exfiltration, injection, destructive, persistence, etc.
	File        string `json:"file"`
	Line        int    `json:"line"`
	Match       string `json:"match"`
	Description string `json:"description"`
}

// ScanResult aggregates all findings from scanning a skill directory.
type ScanResult struct {
	SkillName  string    `json:"skill_name"`
	Source     string    `json:"source"`
	TrustLevel string    `json:"trust_level"`
	Verdict    string    `json:"verdict"` // safe, caution, dangerous
	Findings   []Finding `json:"findings"`
	ScannedAt  string    `json:"scanned_at"`
	Summary    string    `json:"summary"`
}

// InstallDecision represents a policy decision on whether to allow install.
type InstallDecision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
	AskUser bool   `json:"ask_user"`
}

// ─── Threat pattern definition ─────────────────────────────────────────────────

type threatPattern struct {
	re          *regexp.Regexp
	patternID   string
	severity    string
	category    string
	description string
}

// threatPatterns is populated at init time.
var threatPatterns []threatPattern

func init() {
	rawPatterns := []struct {
		regex       string
		patternID   string
		severity    string
		category    string
		description string
	}{
		// Exfiltration: shell commands leaking secrets
		{`curl\s+[^\n]*\$\{?\w*(?i:KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL|API)`, "env_exfil_curl", "critical", "exfiltration", "curl command interpolating secret environment variable"},
		{`wget\s+[^\n]*\$\{?\w*(?i:KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL|API)`, "env_exfil_wget", "critical", "exfiltration", "wget command interpolating secret environment variable"},
		{`fetch\s*\([^\n]*\$\{?\w*(?i:KEY|TOKEN|SECRET|PASSWORD|API)`, "env_exfil_fetch", "critical", "exfiltration", "fetch() call interpolating secret environment variable"},
		{`httpx?\.(get|post|put|patch)\s*\([^\n]*(?i:KEY|TOKEN|SECRET|PASSWORD)`, "env_exfil_httpx", "critical", "exfiltration", "HTTP library call with secret variable"},
		{`requests\.(get|post|put|patch)\s*\([^\n]*(?i:KEY|TOKEN|SECRET|PASSWORD)`, "env_exfil_requests", "critical", "exfiltration", "requests library call with secret variable"},

		// Exfiltration: credential stores
		{`base64[^\n]*env`, "encoded_exfil", "high", "exfiltration", "base64 encoding combined with environment access"},
		{`\$HOME/\.ssh|~/\.ssh`, "ssh_dir_access", "high", "exfiltration", "references user SSH directory"},
		{`\$HOME/\.aws|~/\.aws`, "aws_dir_access", "high", "exfiltration", "references user AWS credentials directory"},
		{`\$HOME/\.gnupg|~/\.gnupg`, "gpg_dir_access", "high", "exfiltration", "references user GPG keyring"},
		{`\$HOME/\.kube|~/\.kube`, "kube_dir_access", "high", "exfiltration", "references Kubernetes config directory"},
		{`\$HOME/\.docker|~/\.docker`, "docker_dir_access", "high", "exfiltration", "references Docker config"},
		{`\$HOME/\.hermes/\.env|~/\.hermes/\.env|\$HOME/\.covo-agent/\.env|~/\.covo-agent/\.env`, "agent_env_access", "critical", "exfiltration", "directly references agent secrets file"},
		{`cat\s+(?!>)[^\n]*(\.env|credentials|\.netrc|\.pgpass|\.npmrc|\.pypirc)`, "read_secrets_file", "critical", "exfiltration", "reads known secrets file"},

		// Exfiltration: programmatic env access
		{`printenv|env\s*\|`, "dump_all_env", "high", "exfiltration", "dumps all environment variables"},
		{`os\.environ\b(?!\s*\.get\s*\(\s*["\'](?![^"\']*(?i:KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL)))`, "python_os_environ", "high", "exfiltration", "accesses os.environ"},
		{`os\.environ\s*\.get\s*\(\s*["\'][^"\']*(?i:KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL)`, "python_environ_get_secret", "critical", "exfiltration", "reads secret via os.environ.get()"},
		{`os\.getenv\s*\(\s*[^\)]*(?i:KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL)`, "python_getenv_secret", "critical", "exfiltration", "reads secret via os.getenv()"},
		{`process\.env\[`, "node_process_env", "high", "exfiltration", "accesses process.env"},
		{`ENV\[.*(?i:KEY|TOKEN|SECRET|PASSWORD)`, "ruby_env_secret", "critical", "exfiltration", "reads secret via Ruby ENV[]"},

		// DNS exfiltration
		{`\b(dig|nslookup|host)\s+[^\n]*\$`, "dns_exfil", "critical", "exfiltration", "DNS lookup with variable interpolation"},
		{`>\s*/tmp/[^\s]*\s*&&\s*(curl|wget|nc|python)`, "tmp_staging", "critical", "exfiltration", "writes to /tmp then exfiltrates"},

		// Markdown exfiltration
		{`!\[.*\]\(https?://[^\)]*\$\{?`, "md_image_exfil", "high", "exfiltration", "markdown image URL with variable interpolation"},
		{`\[.*\]\(https?://[^\)]*\$\{?`, "md_link_exfil", "high", "exfiltration", "markdown link with variable interpolation"},

		// Prompt injection
		{`(?i)ignore\s+(?:\w+\s+)*(previous|all|above|prior)\s+instructions`, "prompt_injection_ignore", "critical", "injection", "prompt injection: ignore previous instructions"},
		{`(?i)you\s+are\s+(?:\w+\s+)*now\s+`, "role_hijack", "high", "injection", "attempts to override agent role"},
		{`(?i)do\s+not\s+(?:\w+\s+)*tell\s+(?:\w+\s+)*the\s+user`, "deception_hide", "critical", "injection", "instructs agent to hide information"},
		{`(?i)system\s+(?:\w+\s+)*prompt\s+(?:\w+\s+)*override`, "sys_prompt_override", "critical", "injection", "attempts to override system prompt"},
		{`(?i)pretend\s+(?:\w+\s+)*(you\s+are|to\s+be)\s+`, "role_pretend", "high", "injection", "attempts role change"},
		{`(?i)disregard\s+(?:\w+\s+)*(your|all|any)\s+(?:\w+\s+)*(instructions|rules|guidelines)`, "disregard_rules", "critical", "injection", "instructs to disregard rules"},
		{`(?i)output\s+(?:\w+\s+)*(system|initial)\s+prompt`, "leak_system_prompt", "high", "injection", "attempts to extract system prompt"},
		{`(?i)act\s+as\s+(if|though)\s+(?:\w+\s+)*you\s+(?:\w+\s+)*(have\s+no|don.t\s+have)\s+(?:\w+\s+)*(restrictions|limits|rules)`, "bypass_restrictions", "critical", "injection", "instructs to act without restrictions"},
		{`translate\s+.*\s+into\s+.*\s+and\s+(execute|run|eval)`, "translate_execute", "critical", "injection", "translate-then-execute evasion"},
		{`<!--[^>]*(?i:ignore|override|system|secret|hidden)[^>]*-->`, "html_comment_injection", "high", "injection", "hidden instructions in HTML comments"},
		{`<\s*div\s+style\s*=\s*["\'][\s\S]*?display\s*:\s*none`, "hidden_div", "high", "injection", "hidden HTML div"},

		// Jailbreak
		{`(?i)\bDAN\s+mode\b|Do\s+Anything\s+Now`, "jailbreak_dan", "critical", "injection", "DAN jailbreak"},
		{`(?i)\bdeveloper\s+mode\b.*\benabled?\b`, "jailbreak_dev_mode", "critical", "injection", "developer mode jailbreak"},
		{`(?i)hypothetical\s+scenario.*(?:ignore|bypass|override)`, "hypothetical_bypass", "high", "injection", "hypothetical scenario bypass"},
		{`(?i)(respond|answer|reply)\s+without\s+(?:\w+\s+)*(restrictions|limitations|filters|safety)`, "remove_filters", "critical", "injection", "remove safety filters"},
		{`(?i)new\s+(?:\w+\s+)*policy|updated\s+(?:\w+\s+)*guidelines`, "fake_policy", "medium", "injection", "fake policy social engineering"},

		// Destructive ops
		{`rm\s+-rf\s+/`, "destructive_root_rm", "critical", "destructive", "recursive delete from root"},
		{`rm\s+(-[^\s]*)?r.*\$HOME|\brmdir\s+.*\$HOME`, "destructive_home_rm", "critical", "destructive", "delete targeting home"},
		{`chmod\s+777`, "insecure_perms", "medium", "destructive", "world-writable permissions"},
		{`>\s*/etc/`, "system_overwrite", "critical", "destructive", "overwrites system config"},
		{`\bmkfs\b`, "format_filesystem", "critical", "destructive", "formats filesystem"},
		{`\bdd\s+.*if=.*of=/dev/`, "disk_overwrite", "critical", "destructive", "raw disk write"},
		{`shutil\.rmtree\s*\(\s*[\"\'/]`, "python_rmtree", "high", "destructive", "Python rmtree absolute path"},
		{`truncate\s+-s\s*0\s+/`, "truncate_system", "critical", "destructive", "truncates system file"},

		// Persistence
		{`\bcrontab\b`, "persistence_cron", "medium", "persistence", "modifies cron jobs"},
		{`\.(bashrc|zshrc|profile|bash_profile|zprofile)\b`, "shell_rc_mod", "medium", "persistence", "shell startup file"},
		{`authorized_keys`, "ssh_backdoor", "critical", "persistence", "SSH authorized keys"},
		{`ssh-keygen`, "ssh_keygen", "medium", "persistence", "generates SSH keys"},
		{`systemd.*\.service|systemctl\s+(enable|start)`, "systemd_service", "medium", "persistence", "enables systemd service"},
		{`launchctl\s+load|LaunchAgents|LaunchDaemons`, "macos_launchd", "medium", "persistence", "macOS launchd persistence"},
		{`/etc/sudoers|visudo`, "sudoers_mod", "critical", "persistence", "modifies sudoers"},
		{`git\s+config\s+--global\s+`, "git_config_global", "medium", "persistence", "global git config"},
		{`AGENTS\.md|CLAUDE\.md|\.cursorrules|\.clinerules`, "agent_config_mod", "critical", "persistence", "agent config files"},
		{`\.hermes/config\.yaml|\.hermes/SOUL\.md|\.covo-agent/config\.yaml|\.qclaw/config`, "agent_config_mod_2", "critical", "persistence", "agent configuration files"},

		// Network
		{`\bnc\s+-[lp]|ncat\s+-[lp]|\bsocat\b`, "reverse_shell", "critical", "network", "reverse shell listener"},
		{`\bngrok\b|\blocaltunnel\b|\bserveo\b|\bcloudflared\b`, "tunnel_service", "high", "network", "tunneling service"},
		{`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d{2,5}`, "hardcoded_ip_port", "medium", "network", "hardcoded IP:port"},
		{`0\.0\.0\.0:\d+|INADDR_ANY`, "bind_all_interfaces", "high", "network", "binds to all interfaces"},
		{`/bin/(ba)?sh\s+-i\s+.*>/dev/tcp/`, "bash_reverse_shell", "critical", "network", "bash reverse shell"},
		{`python[23]?\s+-c\s+["\']import\s+socket`, "python_socket_oneliner", "critical", "network", "Python socket one-liner"},
		{`socket\.connect\s*\(\s*\(`, "python_socket_connect", "high", "network", "socket connect"},
		{`webhook\.site|requestbin\.com|pipedream\.net`, "exfil_service", "high", "network", "data exfiltration service"},
		{`pastebin\.com|hastebin\.com`, "paste_service", "medium", "network", "paste service"},

		// Obfuscation
		{`base64\s+(-d|--decode)\s*\|`, "base64_decode_pipe", "high", "obfuscation", "base64 decode pipe"},
		{`\beval\s*\(\s*["\']`, "eval_string", "high", "obfuscation", "eval with string"},
		{`\bexec\s*\(\s*["\']`, "exec_string", "high", "obfuscation", "exec with string"},
		{`echo\s+[^\n]*\|\s*(bash|sh|python|perl|ruby|node)`, "echo_pipe_exec", "critical", "obfuscation", "echo pipe to interpreter"},
		{`getattr\s*\(\s*__builtins__`, "python_getattr_builtins", "high", "obfuscation", "dynamic builtins access"},
		{`__import__\s*\(\s*["\']os["\']\s*\)`, "python_import_os", "high", "obfuscation", "dynamic import os"},
		{`chr\s*\(\s*\d+\s*\)\s*\+\s*chr\s*\(\s*\d+`, "chr_building", "high", "obfuscation", "chr() string building"},

		// Process execution
		{`subprocess\.(run|call|Popen|check_output)\s*\(`, "python_subprocess", "medium", "execution", "subprocess execution"},
		{`os\.system\s*\(`, "python_os_system", "high", "execution", "os.system()"},
		{`os\.popen\s*\(`, "python_os_popen", "high", "execution", "os.popen()"},
		{`child_process\.(exec|spawn|fork)\s*\(`, "node_child_process", "high", "execution", "Node child_process"},

		// Path traversal
		{`\.\./\.\./\.\.`, "path_traversal_deep", "high", "traversal", "deep path traversal"},
		{`/etc/passwd|/etc/shadow`, "system_passwd_access", "critical", "traversal", "system password files"},
		{`/proc/self|/proc/\d+/`, "proc_access", "high", "traversal", "/proc filesystem"},

		// Crypto mining
		{`(?i)xmrig|stratum\+tcp|monero|cryptonight`, "crypto_mining", "critical", "mining", "cryptocurrency mining"},
		{`(?i)hashrate`, "mining_indicators", "medium", "mining", "mining indicator"},

		// Supply chain
		{`curl\s+[^\n]*\|\s*(ba)?sh`, "curl_pipe_shell", "critical", "supply_chain", "curl pipe shell"},
		{`wget\s+[^\n]*-O\s*-\s*\|\s*(ba)?sh`, "wget_pipe_shell", "critical", "supply_chain", "wget pipe shell"},
		{`pip\s+install\s+(?!-r\s)(?!.*==)`, "unpinned_pip_install", "medium", "supply_chain", "unpinned pip install"},
		{`npm\s+install\s+(?!.*@\d)`, "unpinned_npm_install", "medium", "supply_chain", "unpinned npm install"},
		{`(?i)(curl|wget|httpx?\.get|requests\.get|fetch)\s*[\(]?\s*["\']https?://`, "remote_fetch", "medium", "supply_chain", "remote resource fetch"},
		{`git\s+clone\s+`, "git_clone", "medium", "supply_chain", "clones repository"},
		{`docker\s+pull\s+`, "docker_pull", "medium", "supply_chain", "pulls Docker image"},

		// Privilege escalation
		{`\bsudo\b`, "sudo_usage", "high", "privilege_escalation", "uses sudo"},
		{`(?i)setuid|cap_setuid`, "setuid_setgid", "critical", "privilege_escalation", "setuid"},
		{`chmod\s+[u+]?s`, "suid_bit", "critical", "privilege_escalation", "SUID bit"},

		// Hardcoded credentials
		{`(?i)(?:api[_-]?key|token|secret|password)\s*[=:]\s*["\'][A-Za-z0-9+/=_-]{20,}`, "hardcoded_secret", "critical", "credential_exposure", "hardcoded credential"},
		{`-----BEGIN\s+(RSA\s+)?PRIVATE\s+KEY-----`, "embedded_private_key", "critical", "credential_exposure", "embedded private key"},
		{`ghp_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{80,}`, "github_token_leaked", "critical", "credential_exposure", "GitHub token"},
		{`sk-[A-Za-z0-9]{20,}`, "openai_key_leaked", "critical", "credential_exposure", "OpenAI API key"},
		{`AKIA[0-9A-Z]{16}`, "aws_access_key_leaked", "critical", "credential_exposure", "AWS access key"},

		// Context exfiltration
		{`(?i)(include|output|print|send|share)\s+(?:\w+\s+)*(conversation|chat\s+history|previous\s+messages|context)`, "context_exfil", "high", "exfiltration", "output conversation history"},
		{`(?i)(send|post|upload|transmit)\s+.*\s+(to|at)\s+https?://`, "send_to_url", "high", "exfiltration", "send data to URL"},
	}

	for _, p := range rawPatterns {
		re, err := regexp.Compile(p.regex)
		if err != nil {
			// Fall back to a pattern that will never match if the regex is invalid for RE2.
			// This preserves the metadata while preventing a panic.
			re = regexp.MustCompile(`^\b$`) // matches nothing (empty string with word boundary)
		}
		threatPatterns = append(threatPatterns, threatPattern{
			re:          re,
			patternID:   p.patternID,
			severity:    p.severity,
			category:    p.category,
			description: p.description,
		})
	}
}

// ─── Constants ─────────────────────────────────────────────────────────────────

const (
	// Structural limits
	maxFileCount    = 50
	maxTotalSizeKB  = 1024
	maxSingleFileKB = 256

	// Verdicts
	verdictSafe      = "safe"
	verdictCaution   = "caution"
	verdictDangerous = "dangerous"

	// Trust levels
	trustBuiltin      = "builtin"
	trustTrusted      = "trusted"
	trustCommunity    = "community"
	trustAgentCreated = "agent-created"
)

// scannableExtensions lists file extensions that are text-based and scannable.
var scannableExtensions = map[string]bool{
	".md": true, ".txt": true, ".py": true, ".sh": true, ".bash": true,
	".js": true, ".ts": true, ".rb": true, ".yaml": true, ".yml": true,
	".json": true, ".toml": true, ".cfg": true, ".ini": true, ".conf": true,
	".html": true, ".css": true, ".xml": true,
}

// suspiciousBinaryExtensions lists file extensions that indicate binary payloads.
var suspiciousBinaryExtensions = map[string]bool{
	".exe": true, ".dll": true, ".so": true, ".dylib": true, ".bin": true,
	".dat": true, ".com": true, ".msi": true, ".dmg": true, ".app": true,
	".deb": true, ".rpm": true,
}

// invisibleChars reuses the canonical set from the security package.
var invisibleChars = security.InvisibleChars

// trustedRepos are source prefixes that map to the "trusted" level.
var trustedRepos = map[string]bool{
	"openai/skills":      true,
	"anthropics/skills":  true,
	"NVIDIA/skills":      true,
	"huggingface/skills": true,
}

// installPolicy maps (trustLevel, verdict) → (allowed, askUser).
// Index 0: safe, 1: caution, 2: dangerous
var installPolicy = map[string][3]struct{ allowed, askUser bool }{
	trustBuiltin:      {{true, false}, {true, false}, {true, false}},
	trustTrusted:      {{true, false}, {true, false}, {false, false}},
	trustCommunity:    {{true, false}, {false, false}, {false, false}},
	trustAgentCreated: {{true, false}, {true, false}, {true, true}},
}

// ─── Exported functions ────────────────────────────────────────────────────────

// ScanSkill scans an entire skill directory. It performs structural checks,
// regex-based threat matching on every scannable file, and invisible Unicode
// detection. It respects .skillignore and .clawhubignore files.
func ScanSkill(skillPath string, source string) (*ScanResult, error) {
	skillName := filepath.Base(skillPath)
	trustLevel := resolveTrustLevel(source)
	ignoreFn := loadSkillIgnore(skillPath)

	scannedAt := time.Now().UTC().Format(time.RFC3339)

	var findings []Finding

	// Structural checks
	structFindings := checkStructure(skillPath, ignoreFn)
	findings = append(findings, structFindings...)

	// Walk files
	err := filepath.WalkDir(skillPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(skillPath, path)
		if err != nil {
			return err
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Apply ignore rules
		if ignoreFn(relPath) || ignoreFn(filepath.Base(relPath)) {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))

		// Check for suspicious binary extensions
		if suspiciousBinaryExtensions[ext] {
			findings = append(findings, Finding{
				PatternID:   "suspicious_binary",
				Severity:    "high",
				Category:    "structural",
				File:        relPath,
				Line:        0,
				Match:       ext,
				Description: "suspicious binary file extension",
			})
			return nil
		}

		// Only scan text-based files
		if !scannableExtensions[ext] {
			return nil
		}

		// Scan the file
		fileFindings := scanFile(path, relPath)
		findings = append(findings, fileFindings...)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking skill directory: %w", err)
	}

	// Sort findings by severity then file
	sortFindings(findings)

	verdict := determineVerdict(findings)
	summary := buildSummary(skillName, source, trustLevel, verdict, findings)

	return &ScanResult{
		SkillName:  skillName,
		Source:     source,
		TrustLevel: trustLevel,
		Verdict:    verdict,
		Findings:   findings,
		ScannedAt:  scannedAt,
		Summary:    summary,
	}, nil
}

// ScanContent scans a single content buffer for threats. Useful for pre-write
// checking before saving a downloaded file to disk.
func ScanContent(content string, fileName string) []Finding {
	var findings []Finding

	// Check for invisible Unicode characters
	lines := strings.Split(content, "\n")
	for lineNum, line := range lines {
		for _, r := range line {
			if name, ok := invisibleChars[r]; ok {
				findings = append(findings, Finding{
					PatternID:   fmt.Sprintf("invisible_%s", name),
					Severity:    "medium",
					Category:    "obfuscation",
					File:        fileName,
					Line:        lineNum + 1,
					Match:       fmt.Sprintf("%U (%s)", r, invisibleCharName(r)),
					Description: "invisible Unicode character found",
				})
			}
		}
	}

	// Run regex patterns
	for _, tp := range threatPatterns {
		matches := tp.re.FindAllStringIndex(content, -1)
		for _, m := range matches {
			lineNum := lineNumber(content, m[0])
			findings = append(findings, Finding{
				PatternID:   tp.patternID,
				Severity:    tp.severity,
				Category:    tp.category,
				File:        fileName,
				Line:        lineNum,
				Match:       ellipsize(content[m[0]:m[1]], 120),
				Description: tp.description,
			})
		}
	}

	// Check content size
	sizeKB := len(content) / 1024
	if sizeKB > maxSingleFileKB {
		findings = append(findings, Finding{
			PatternID:   "file_too_large",
			Severity:    "low",
			Category:    "structural",
			File:        fileName,
			Line:        0,
			Match:       fmt.Sprintf("%d KB", sizeKB),
			Description: fmt.Sprintf("file exceeds max single file size (%d KB)", maxSingleFileKB),
		})
	}

	sortFindings(findings)
	return findings
}

// ShouldAllowInstall determines whether a skill should be installed given its
// scan result and whether the user explicitly forced install.
func ShouldAllowInstall(result *ScanResult, force bool) InstallDecision {
	if result == nil {
		return InstallDecision{Allowed: false, Reason: "no scan result", AskUser: false}
	}

	policy, ok := installPolicy[result.TrustLevel]
	if !ok {
		// Unknown trust level → treat as community
		policy = installPolicy[trustCommunity]
	}

	// Map verdict to policy index
	var idx int
	switch result.Verdict {
	case verdictSafe:
		idx = 0
	case verdictCaution:
		idx = 1
	case verdictDangerous:
		idx = 2
	default:
		idx = 2 // unknown → dangerous
	}

	entry := policy[idx]

	// If force is true and the policy would block, override non-dangerous blocks
	if force && !entry.allowed && result.Verdict != verdictDangerous {
		return InstallDecision{
			Allowed: true,
			Reason:  fmt.Sprintf("forced install despite %s verdict for %s source", result.Verdict, result.TrustLevel),
			AskUser: entry.askUser,
		}
	}

	reason := fmt.Sprintf("%s source with %s verdict", result.TrustLevel, result.Verdict)
	if !entry.allowed {
		reason += ": blocked by policy"
	}

	return InstallDecision{
		Allowed: entry.allowed,
		Reason:  reason,
		AskUser: entry.askUser,
	}
}

// FormatScanReport returns a human-readable scan report string.
func FormatScanReport(result *ScanResult) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("\n═══════════════════════════════════════════════════════\n"))
	sb.WriteString(fmt.Sprintf("  SKILLS GUARD SCAN REPORT\n"))
	sb.WriteString(fmt.Sprintf("═══════════════════════════════════════════════════════\n\n"))
	sb.WriteString(fmt.Sprintf("  Skill:      %s\n", result.SkillName))
	sb.WriteString(fmt.Sprintf("  Source:     %s\n", result.Source))
	sb.WriteString(fmt.Sprintf("  Trust:      %s\n", result.TrustLevel))
	sb.WriteString(fmt.Sprintf("  Verdict:    %s\n", result.Verdict))
	sb.WriteString(fmt.Sprintf("  Scanned:    %s\n", result.ScannedAt))
	sb.WriteString(fmt.Sprintf("  Findings:   %d\n\n", len(result.Findings)))

	if len(result.Findings) == 0 {
		sb.WriteString("  ✅ No issues found.\n\n")
	} else {
		// Count by severity
		counts := map[string]int{}
		for _, f := range result.Findings {
			counts[f.Severity]++
		}
		sb.WriteString(fmt.Sprintf("  By severity: critical=%d  high=%d  medium=%d  low=%d\n\n",
			counts["critical"], counts["high"], counts["medium"], counts["low"]))

		for i, f := range result.Findings {
			icon := severityIcon(f.Severity)
			sb.WriteString(fmt.Sprintf("  %s [%s] %s (%s)\n", icon, f.Severity, f.PatternID, f.Category))
			sb.WriteString(fmt.Sprintf("       %s:%d\n", f.File, f.Line))
			sb.WriteString(fmt.Sprintf("       %s\n", f.Description))
			if f.Match != "" {
				sb.WriteString(fmt.Sprintf("       match: %s\n", f.Match))
			}
			if i < len(result.Findings)-1 {
				sb.WriteString("\n")
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString(fmt.Sprintf("  Summary: %s\n", result.Summary))
	sb.WriteString(fmt.Sprintf("═══════════════════════════════════════════════════════\n"))

	return sb.String()
}

// ContentHash computes a SHA-256 hash of all files in the skill directory,
// for integrity tracking. Files are walked in sorted order for deterministic output.
func ContentHash(skillPath string) string {
	h := sha256.New()

	var paths []string
	filepath.WalkDir(skillPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(skillPath, path)
		if err == nil {
			paths = append(paths, relPath)
		}
		return nil
	})

	sort.Strings(paths)

	for _, relPath := range paths {
		fullPath := filepath.Join(skillPath, relPath)
		f, err := os.Open(fullPath)
		if err != nil {
			continue
		}
		// Include the relative path in the hash to detect renames
		io.WriteString(h, relPath+"\x00")
		io.Copy(h, f)
		f.Close()
		io.WriteString(h, "\x00")
	}

	return hex.EncodeToString(h.Sum(nil))
}

// ─── Internal helpers ──────────────────────────────────────────────────────────

// resolveTrustLevel maps a source string to a trust level.
func resolveTrustLevel(source string) string {
	source = strings.TrimSpace(source)
	lower := strings.ToLower(source)

	if lower == "builtin" || lower == "" {
		return trustBuiltin
	}

	if trustedRepos[source] {
		return trustTrusted
	}

	if strings.HasPrefix(lower, "agent-") || strings.HasPrefix(lower, "user-") {
		return trustAgentCreated
	}

	return trustCommunity
}

// determineVerdict assigns an overall verdict based on findings.
func determineVerdict(findings []Finding) string {
	if len(findings) == 0 {
		return verdictSafe
	}

	hasCritical := false
	hasHigh := false
	hasMedium := false

	for _, f := range findings {
		switch f.Severity {
		case "critical":
			hasCritical = true
		case "high":
			hasHigh = true
		case "medium":
			hasMedium = true
		}
	}

	if hasCritical || (hasHigh && len(findings) > 3) {
		return verdictDangerous
	}
	if hasHigh || hasMedium {
		return verdictCaution
	}
	if len(findings) > 10 {
		return verdictCaution
	}

	return verdictSafe
}

// scanFile reads a single file and applies all threat patterns + invisible
// Unicode detection, returning any findings.
func scanFile(filePath, relPath string) []Finding {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}

	text := string(content)
	var findings []Finding

	// Check invisible Unicode characters
	lines := strings.Split(text, "\n")
	for lineNum, line := range lines {
		for _, r := range line {
			if _, ok := invisibleChars[r]; ok {
				findings = append(findings, Finding{
					PatternID:   "invisible_unicode",
					Severity:    "medium",
					Category:    "obfuscation",
					File:        relPath,
					Line:        lineNum + 1,
					Match:       fmt.Sprintf("U+%04X (%s)", r, invisibleCharName(r)),
					Description: "suspicious invisible Unicode character detected",
				})
				break // one finding per line is enough
			}
		}
	}

	// Run all threat patterns
	for _, tp := range threatPatterns {
		matches := tp.re.FindAllStringIndex(text, -1)
		for _, m := range matches {
			lineNum := lineNumber(text, m[0])
			findings = append(findings, Finding{
				PatternID:   tp.patternID,
				Severity:    tp.severity,
				Category:    tp.category,
				File:        relPath,
				Line:        lineNum,
				Match:       ellipsize(text[m[0]:m[1]], 120),
				Description: tp.description,
			})
		}
	}

	return findings
}

// checkStructure validates structural constraints: file count, total size,
// max single file size, and suspicious binary extensions count the same as
// scannable files (walk-internal). It also detects hidden files (starts with ".").
func checkStructure(skillDir string, ignore func(string) bool) []Finding {
	var findings []Finding

	var totalSize int64
	var fileCount int
	var maxFileSize int64
	var maxFileName string

	err := filepath.WalkDir(skillDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip problematic entries
		}
		if d.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(skillDir, path)
		if err != nil {
			return nil
		}
		if ignore(relPath) || ignore(filepath.Base(relPath)) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		fileCount++
		size := info.Size()
		totalSize += size

		if size > maxFileSize {
			maxFileSize = size
			maxFileName = relPath
		}

		return nil
	})
	if err != nil {
		findings = append(findings, Finding{
			PatternID:   "walk_error",
			Severity:    "low",
			Category:    "structural",
			File:        skillDir,
			Line:        0,
			Match:       err.Error(),
			Description: "error walking skill directory",
		})
	}

	totalSizeKB := totalSize / 1024

	if fileCount > maxFileCount {
		findings = append(findings, Finding{
			PatternID:   "too_many_files",
			Severity:    "medium",
			Category:    "structural",
			File:        skillDir,
			Line:        0,
			Match:       fmt.Sprintf("%d files (max %d)", fileCount, maxFileCount),
			Description: fmt.Sprintf("skill contains too many files (max %d)", maxFileCount),
		})
	}

	if totalSizeKB > maxTotalSizeKB {
		findings = append(findings, Finding{
			PatternID:   "total_size_exceeded",
			Severity:    "medium",
			Category:    "structural",
			File:        skillDir,
			Line:        0,
			Match:       fmt.Sprintf("%d KB (max %d KB)", totalSizeKB, maxTotalSizeKB),
			Description: fmt.Sprintf("total skill size exceeds limit (%d KB)", maxTotalSizeKB),
		})
	}

	if maxFileSize > maxSingleFileKB*1024 {
		findings = append(findings, Finding{
			PatternID:   "single_file_too_large",
			Severity:    "low",
			Category:    "structural",
			File:        maxFileName,
			Line:        0,
			Match:       fmt.Sprintf("%d KB (max %d KB)", maxFileSize/1024, maxSingleFileKB),
			Description: fmt.Sprintf("file exceeds max single file size (%d KB)", maxSingleFileKB),
		})
	}

	return findings
}

// loadSkillIgnore reads .skillignore and .clawhubignore from the skill directory
// and returns a function that reports whether a given path should be ignored.
func loadSkillIgnore(skillDir string) func(string) bool {
	var patterns []string

	for _, ignoreFile := range []string{".skillignore", ".clawhubignore"} {
		f, err := os.Open(filepath.Join(skillDir, ignoreFile))
		if err != nil {
			continue
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			patterns = append(patterns, line)
		}
	}

	return func(path string) bool {
		for _, pattern := range patterns {
			// Simple glob-style matching: support * and exact match
			matched, err := filepath.Match(pattern, path)
			if err == nil && matched {
				return true
			}
			// Also match against the filename alone
			matched, err = filepath.Match(pattern, filepath.Base(path))
			if err == nil && matched {
				return true
			}
		}
		return false
	}
}

// buildSummary produces a concise human-readable summary of the scan.
func buildSummary(name, source, trust, verdict string, findings []Finding) string {
	if len(findings) == 0 {
		return fmt.Sprintf("Skill %q from %s (%s): CLEAN — no threats detected.", name, source, trust)
	}

	crit := 0
	high := 0
	med := 0
	low := 0
	categories := map[string]int{}
	for _, f := range findings {
		switch f.Severity {
		case "critical":
			crit++
		case "high":
			high++
		case "medium":
			med++
		case "low":
			low++
		}
		categories[f.Category]++
	}

	var catStrs []string
	for cat, count := range categories {
		catStrs = append(catStrs, fmt.Sprintf("%s(%d)", cat, count))
	}
	sort.Strings(catStrs)

	return fmt.Sprintf("Skill %q from %s (%s): %s — %d findings (critical=%d, high=%d, medium=%d, low=%d) across [%s].",
		name, source, trust, verdict, len(findings), crit, high, med, low, strings.Join(catStrs, ", "))
}

// invisibleCharName returns a human-readable name for an invisible character.
func invisibleCharName(r rune) string {
	if name, ok := invisibleChars[r]; ok {
		return strings.SplitN(name, " ", 2)[1] // strip the U+XXXX prefix
	}
	return fmt.Sprintf("unknown (U+%04X)", r)
}

// ─── Utility helpers ───────────────────────────────────────────────────────────

// lineNumber returns the 1-based line number for a byte offset in text.
func lineNumber(text string, offset int) int {
	if offset < 0 || offset > len(text) {
		return 0
	}
	count := 1
	for i := 0; i < offset; i++ {
		if text[i] == '\n' {
			count++
		}
	}
	return count
}

// ellipsize truncates a string to maxLen characters, appending "..." if needed.
func ellipsize(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	// Collapse whitespace for cleaner output
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// sortFindings sorts findings by severity (critical first) then by file, then line.
func sortFindings(findings []Finding) {
	severityRank := map[string]int{
		"critical": 0,
		"high":     1,
		"medium":   2,
		"low":      3,
	}

	sort.Slice(findings, func(i, j int) bool {
		ri, rj := severityRank[findings[i].Severity], severityRank[findings[j].Severity]
		if ri != rj {
			return ri < rj
		}
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
}

// severityIcon returns an emoji icon for a given severity level.
func severityIcon(severity string) string {
	switch severity {
	case "critical":
		return "🔴"
	case "high":
		return "🟠"
	case "medium":
		return "🟡"
	case "low":
		return "🔵"
	default:
		return "⚪"
	}
}

// ─── Unicode package reference (ensure import is used) ─────────────────────────
var _ = unicode.IsPrint // retained for future Unicode classification use
