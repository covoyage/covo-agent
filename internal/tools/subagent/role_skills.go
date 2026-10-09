package subagent

import (
	"strings"
	"sync"

	"github.com/covoyage/covo-agent/internal/cyber"
)

const roleSkillBodyRunes = 6000
const maxInjectedRoleSkills = 5

type vulnerabilitySkillRule struct {
	name     string
	keywords []string
}

var vulnerabilitySkillRules = []vulnerabilitySkillRule{
	{name: "security-testing-idor", keywords: []string{"idor", "bola", "object authorization", "object-level authorization", "cross-tenant"}},
	{name: "security-testing-sql-injection", keywords: []string{"sql injection", "sqli"}},
	{name: "security-testing-nosql-injection", keywords: []string{"nosql injection", "mongodb injection"}},
	{name: "security-testing-xss", keywords: []string{"cross-site scripting", "xss"}},
	{name: "security-testing-ssrf", keywords: []string{"server-side request forgery", "ssrf"}},
	{name: "security-testing-jwt", keywords: []string{"jwt", "json web token"}},
	{name: "security-testing-request-smuggling", keywords: []string{"request smuggling", "cl.te", "te.cl"}},
	{name: "security-testing-file-upload", keywords: []string{"file upload", "upload bypass"}},
	{name: "security-testing-path-traversal", keywords: []string{"path traversal", "directory traversal", "lfi", "rfi"}},
	{name: "security-testing-deserialization", keywords: []string{"deserialization", "unserialize", "pickle"}},
	{name: "security-testing-ssti", keywords: []string{"server-side template injection", "ssti"}},
	{name: "security-testing-race-conditions", keywords: []string{"race condition", "toctou", "double spend"}},
	{name: "security-testing-business-logic", keywords: []string{"business logic", "workflow bypass", "price manipulation"}},
	{name: "security-testing-xxe", keywords: []string{"xml external entity", "xxe"}},
	{name: "security-testing-csrf", keywords: []string{"cross-site request forgery", "csrf"}},
	{name: "security-testing-rce", keywords: []string{"remote code execution", "command injection", "rce"}},
	{name: "security-testing-open-redirect", keywords: []string{"open redirect", "url redirect"}},
	{name: "security-testing-prototype-pollution", keywords: []string{"prototype pollution", "__proto__"}},
	{name: "security-testing-header-injection", keywords: []string{"header injection", "response splitting", "crlf"}},
	{name: "security-testing-mass-assignment", keywords: []string{"mass assignment", "overposting"}},
	{name: "security-testing-subdomain-takeover", keywords: []string{"subdomain takeover", "dangling dns"}},
	{name: "security-testing-llm-prompt-injection", keywords: []string{"prompt injection", "tool injection", "agentic system"}},
	{name: "security-testing-authentication", keywords: []string{"authentication bypass", "session fixation", "account recovery", "mfa bypass"}},
	{name: "security-testing-browser-security", keywords: []string{"cors", "postmessage", "clickjacking", "browser security"}},
	{name: "security-testing-information-disclosure", keywords: []string{"information disclosure", "sensitive data exposure", "debug endpoint"}},
	{name: "security-testing-weak-passwords", keywords: []string{"weak password", "password policy", "credential stuffing", "password spray"}},
}

// SkillBodyLoader returns a skill body by name. Missing skills should return an error.
type SkillBodyLoader func(name string) (string, error)

var (
	skillLoaderMu sync.RWMutex
	skillLoader   SkillBodyLoader
	observerMu    sync.RWMutex
	observer      func(AgentObservation)
)

type AgentObservation struct {
	ID     string
	Role   string
	Task   string
	Skills []string
	Status string
	Error  string
}

// SetSkillBodyLoader installs the loader used when a role is spawned.
// A nil loader disables injection.
func SetSkillBodyLoader(fn SkillBodyLoader) {
	skillLoaderMu.Lock()
	skillLoader = fn
	skillLoaderMu.Unlock()
}

func SetAgentObserver(fn func(AgentObservation)) {
	observerMu.Lock()
	observer = fn
	observerMu.Unlock()
}

func observeAgent(event AgentObservation) {
	observerMu.RLock()
	fn := observer
	observerMu.RUnlock()
	if fn != nil {
		fn(event)
	}
}

func RoleSkillNamesForTask(role, task string) []string {
	return append([]string(nil), roleSkillNamesForTask(role, task)...)
}

func loadRoleSkills(role, task string) string {
	names := roleSkillNamesForTask(role, task)
	if len(names) == 0 {
		return ""
	}
	skillLoaderMu.RLock()
	loader := skillLoader
	skillLoaderMu.RUnlock()
	if loader == nil {
		return ""
	}
	var b strings.Builder
	for _, name := range names {
		body, err := loader(name)
		body = strings.TrimSpace(body)
		if err != nil || body == "" {
			continue
		}
		body = truncateRunes(body, roleSkillBodyRunes)
		if b.Len() == 0 {
			b.WriteString("## Role skills\n")
		}
		b.WriteString("\n### ")
		b.WriteString(name)
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func roleSkillNamesForTask(role, task string) []string {
	names := append([]string(nil), cyber.RoleSkillNames(role)...)
	if !cyber.HandsOnRoles[strings.ToLower(strings.TrimSpace(role))] {
		return names
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		seen[name] = true
	}
	lowerTask := strings.ToLower(task)
	for _, rule := range vulnerabilitySkillRules {
		if len(names) >= maxInjectedRoleSkills {
			break
		}
		if seen[rule.name] || !containsAny(lowerTask, rule.keywords) {
			continue
		}
		names = append(names, rule.name)
		seen[rule.name] = true
	}
	if len(names) > maxInjectedRoleSkills {
		names = names[:maxInjectedRoleSkills]
	}
	return names
}

func containsAny(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}
