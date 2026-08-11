package sandbox

import (
	"os"
	"regexp"
	"strings"

	ossandbox "github.com/covoyage/covo-agent/internal/sandbox/ossandbox"
)

// EnvInheritMode controls how environment variables are inherited by child
// processes spawned by the sandbox.
type EnvInheritMode string

const (
	// EnvInheritAll passes all parent environment variables through (legacy behavior).
	EnvInheritAll EnvInheritMode = "all"
	// EnvInheritCore passes only essential variables (PATH, HOME, SHELL, USER,
	// LANG, LC_*, TERM, etc.) and strips anything that looks like a secret.
	EnvInheritCore EnvInheritMode = "core"
	// EnvInheritNone passes no inherited variables — only explicitly set ones.
	EnvInheritNone EnvInheritMode = "none"
)

// coreEnvVars is the allowlist of environment variable name patterns that
// are retained when inherit=core. Matching is case-insensitive on the prefix.
var coreEnvPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^PATH$`),
	regexp.MustCompile(`(?i)^HOME$`),
	regexp.MustCompile(`(?i)^SHELL$`),
	regexp.MustCompile(`(?i)^USER$`),
	regexp.MustCompile(`(?i)^LOGNAME$`),
	regexp.MustCompile(`(?i)^LANG$`),
	regexp.MustCompile(`(?i)^LC_\w+$`),
	regexp.MustCompile(`(?i)^TERM$`),
	regexp.MustCompile(`(?i)^TMPDIR$`),
	regexp.MustCompile(`(?i)^PWD$`),
	regexp.MustCompile(`(?i)^OLDPWD$`),
	regexp.MustCompile(`(?i)^SHLVL$`),
	regexp.MustCompile(`(?i)^COVO_\w+$`), // covo-agent internal vars
	regexp.MustCompile(`(?i)^SANDBOX_\w+$`),
	regexp.MustCompile(`(?i)^DOCKER_\w+$`), // docker config (non-secret)
	regexp.MustCompile(`(?i)^SSH_AUTH_SOCK$`),
	regexp.MustCompile(`(?i)^COLORTERM$`),
	regexp.MustCompile(`(?i)^EDITOR$`),
	regexp.MustCompile(`(?i)^VISUAL$`),
	regexp.MustCompile(`(?i)^PAGER$`),
	regexp.MustCompile(`(?i)^GOPATH$`),
	regexp.MustCompile(`(?i)^GOROOT$`),
	regexp.MustCompile(`(?i)^GOPROXY$`),
	regexp.MustCompile(`(?i)^NODE_PATH$`),
	regexp.MustCompile(`(?i)^npm_config_\w+$`),
	regexp.MustCompile(`(?i)^PYTHONPATH$`),
	regexp.MustCompile(`(?i)^VIRTUAL_ENV$`),
	regexp.MustCompile(`(?i)^CONDA_\w+$`),
	regexp.MustCompile(`(?i)^RUST_\w+$`),
	regexp.MustCompile(`(?i)^CARGO_\w+$`),
	regexp.MustCompile(`(?i)^JAVA_HOME$`),
	regexp.MustCompile(`(?i)^MAVEN_\w+$`),
	regexp.MustCompile(`(?i)^GRADLE_\w+$`),
}

// secretPatterns matches environment variable names that look like secrets.
// Variables matching any of these patterns are ALWAYS stripped, regardless
// of inherit mode (unless explicitly listed in include_only).
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(API.?KEY|API.?SECRET)`),
	regexp.MustCompile(`(?i)(SECRET)`),
	regexp.MustCompile(`(?i)(TOKEN)`),
	regexp.MustCompile(`(?i)(PASSWORD|PASSWD|PWD)`), // but not PWD alone (handled separately)
	regexp.MustCompile(`(?i)(CREDENTIAL)`),
	regexp.MustCompile(`(?i)(PRIVATE.?KEY)`),
	regexp.MustCompile(`(?i)(AUTH)`), // AUTH, AUTHORIZATION, etc.
	regexp.MustCompile(`(?i)(ACCESS.?KEY)`),
	regexp.MustCompile(`(?i)(SESSION.?KEY)`),
	regexp.MustCompile(`(?i)(ENCRYPT)`),
	regexp.MustCompile(`(?i)(SIGNING.?KEY)`),
	regexp.MustCompile(`(?i)(OAUTH)`),
	regexp.MustCompile(`(?i)(JWT)`),
	regexp.MustCompile(`(?i)(BEARER)`),
	regexp.MustCompile(`(?i)(AWS_SECRET)`),
	regexp.MustCompile(`(?i)(AWS_SESSION)`),
	regexp.MustCompile(`(?i)(STRIPE)`),
	regexp.MustCompile(`(?i)(SENDGRID)`),
	regexp.MustCompile(`(?i)(TWILIO)`),
	regexp.MustCompile(`(?i)(MAILGUN)`),
	regexp.MustCompile(`(?i)(POSTMARK)`),
	regexp.MustCompile(`(?i)(BRAINTREE)`),
	regexp.MustCompile(`(?i)(DATABASE_URL)`),
	regexp.MustCompile(`(?i)(DB_PASS)`),
	regexp.MustCompile(`(?i)(REDIS_URL)`),
	regexp.MustCompile(`(?i)(MONGO_URI)`),
}

// EnvPolicy controls which environment variables are passed to child processes.
//
// Configuration (via COVO_ENV_INHERIT env var or programmatic):
//   - inherit: "all" (default), "core", or "none"
//   - exclude: comma-separated list of var name patterns to strip (e.g. "AWS_*,GITHUB_*")
//   - include_only: comma-separated allowlist (overrides everything else)
//   - set: key=value pairs to inject
//
// When inherit=core, secret-looking variables (containing KEY, SECRET, TOKEN,
// PASSWORD, etc.) are ALWAYS stripped, even if they match a core pattern.
type EnvPolicy struct {
	Mode        EnvInheritMode
	Exclude     []string // name patterns to exclude (glob, case-insensitive)
	IncludeOnly []string // name patterns to include exclusively (glob, case-insensitive)
	Set         map[string]string // explicit key=value to inject
}

// DefaultEnvPolicy returns the default policy (inherit all, strip secrets).
func DefaultEnvPolicy() EnvPolicy {
	return EnvPolicy{
		Mode: EnvInheritAll,
	}
}

// EnvPolicyFromEnv reads the environment variable policy from the process
// environment. Supported env vars:
//
//	COVO_ENV_INHERIT=all|core|none   (default: all)
//	COVO_ENV_EXCLUDE=AWS_*,GITHUB_*  (comma-separated glob patterns)
//	COVO_ENV_INCLUDE_ONLY=PATH,HOME  (comma-separated, overrides inherit)
//	COVO_ENV_SET_KEY=value           (any env var starting with COVO_ENV_SET_)
func EnvPolicyFromEnv() EnvPolicy {
	policy := DefaultEnvPolicy()

	if v := os.Getenv("COVO_ENV_INHERIT"); v != "" {
		policy.Mode = EnvInheritMode(strings.ToLower(v))
	}

	if v := os.Getenv("COVO_ENV_EXCLUDE"); v != "" {
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				policy.Exclude = append(policy.Exclude, p)
			}
		}
	}

	if v := os.Getenv("COVO_ENV_INCLUDE_ONLY"); v != "" {
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				policy.IncludeOnly = append(policy.IncludeOnly, p)
			}
		}
	}

	// Collect COVO_ENV_SET_* variables
	policy.Set = make(map[string]string)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "COVO_ENV_SET_") {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) == 2 {
				// Strip the COVO_ENV_SET_ prefix to get the actual var name.
				name := strings.TrimPrefix(parts[0], "COVO_ENV_SET_")
				policy.Set[name] = parts[1]
			}
		}
	}

	return policy
}

// FilterEnv applies the policy to the given environment (as returned by os.Environ())
// and returns the filtered list suitable for exec.Cmd.Env.
func (p EnvPolicy) FilterEnv(env []string) []string {
	// Build a map for deduplication and lookup.
	envMap := make(map[string]string)
	for _, kv := range env {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		envMap[parts[0]] = parts[1]
	}

	var result map[string]string

	switch {
	case len(p.IncludeOnly) > 0:
		// include_only overrides everything — only matching vars pass through
		result = make(map[string]string)
		for name, val := range envMap {
			if matchAnyGlob(name, p.IncludeOnly) {
				result[name] = val
			}
		}

	case p.Mode == EnvInheritNone:
		result = make(map[string]string)

	case p.Mode == EnvInheritCore:
		result = make(map[string]string)
		for name, val := range envMap {
			if isCoreEnv(name) {
				result[name] = val
			}
		}

	default: // EnvInheritAll
		result = make(map[string]string)
		for name, val := range envMap {
			result[name] = val
		}
	}

	// Always strip secret-looking variables (unless in include_only mode where
	// the user explicitly asked for them).
	if len(p.IncludeOnly) == 0 {
		for name := range result {
			if isSecretEnv(name) {
				delete(result, name)
			}
		}
	}

	// Apply exclude patterns
	for name := range result {
		if matchAnyGlob(name, p.Exclude) {
			delete(result, name)
		}
	}

	// Apply explicit set values (these override everything)
	for k, v := range p.Set {
		result[k] = v
	}

	// Always strip noisy macOS malloc/debug variables (e.g. MallocStackLogging)
	// that produce stderr warnings in child processes — even if injected via p.Set.
	// Skip when the user explicitly opted in to malloc debugging.
	if os.Getenv("COVO_KEEP_MALLOC_DEBUG") != "1" &&
		!strings.EqualFold(os.Getenv("COVO_KEEP_MALLOC_DEBUG"), "true") {
		for name := range result {
			if isNoisyMallocEnv(name) {
				delete(result, name)
			}
		}
	}

	// Convert back to []string
	filtered := make([]string, 0, len(result))
	for k, v := range result {
		filtered = append(filtered, k+"="+v)
	}
	return filtered
}

// isCoreEnv checks if an environment variable name matches a core pattern.
func isCoreEnv(name string) bool {
	for _, re := range coreEnvPatterns {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

// isSecretEnv checks if an environment variable name looks like a secret.
func isSecretEnv(name string) bool {
	// Don't strip PWD (current working directory) even though it contains "PWD"
	if name == "PWD" || name == "OLDPWD" {
		return false
	}
	for _, re := range secretPatterns {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

// isNoisyMallocEnv reports whether name is a macOS malloc/debug variable that
// produces noisy stderr output in child processes (case-insensitive).
func isNoisyMallocEnv(name string) bool {
	lower := strings.ToLower(name)
	for _, n := range ossandbox.NoisyMallocEnvVars {
		if lower == strings.ToLower(n) {
			return true
		}
	}
	return false
}

// matchAnyGlob checks if a name matches any of the glob patterns.
// Patterns support * as a wildcard. Matching is case-insensitive.
func matchAnyGlob(name string, patterns []string) bool {
	for _, p := range patterns {
		if globMatch(p, name) {
			return true
		}
	}
	return false
}

// globMatch matches a glob pattern (with *) against a string, case-insensitively.
func globMatch(pattern, s string) bool {
	// Convert glob to regex: * → .*
	// Escape regex special chars except *
	var sb strings.Builder
	sb.WriteString("(?i)^")
	for _, r := range pattern {
		switch r {
		case '*':
			sb.WriteString(".*")
		case '.', '+', '?', '(', ')', '[', ']', '{', '}', '^', '$', '|', '\\':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteString("$")
	re, err := regexp.Compile(sb.String())
	if err != nil {
		return false
	}
	return re.MatchString(s)
}
