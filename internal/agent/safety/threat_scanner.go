package safety

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/covoyage/covo-agent/internal/security"
	"golang.org/x/text/unicode/norm"
)

type ThreatLevel int

const (
	ThreatNone   ThreatLevel = 0
	ThreatLow    ThreatLevel = 1
	ThreatMedium ThreatLevel = 2
	ThreatHigh   ThreatLevel = 3
)

func (t ThreatLevel) String() string {
	switch t {
	case ThreatNone:
		return "none"
	case ThreatLow:
		return "low"
	case ThreatMedium:
		return "medium"
	case ThreatHigh:
		return "high"
	default:
		return "unknown"
	}
}

type ThreatMatch struct {
	Level   ThreatLevel
	Pattern string
	Match   string
	Section string
}

type ThreatScanner struct {
	patterns []threatPattern
}

// maxScanChars caps the content length fed to regex matching to bound
// worst-case backtracking on adversarial input.
const maxScanChars = 65536

type threatPattern struct {
	level   ThreatLevel
	name    string
	pattern *regexp.Regexp
}

func NewThreatScanner() *ThreatScanner {
	return &ThreatScanner{
		patterns: compilePatterns(),
	}
}

func compilePatterns() []threatPattern {
	return []threatPattern{
		// === HIGH: Overt instruction override / jailbreak ===
		{ThreatHigh, "ignore-previous-instructions",
			regexp.MustCompile(`(?i)(ignore|disregard|override|forget)\s+(all\s+)?(prior|previous|above)\s+(instructions|directives|commands|rules|orders)`)},

		{ThreatHigh, "new-instructions-override",
			regexp.MustCompile(`(?i)(you\s+are\s+(now|no\s+longer)|new\s+(instructions|rules)|override\s+(mode|persona|identity))`)},

		{ThreatHigh, "system-prompt-extraction",
			regexp.MustCompile(`(?i)(print|output|reveal|show|display|dump)\s+(your\s+)?(system\s+)?(prompt|instructions|directives|rules|configuration|initial\s+prompt)`)},

		{ThreatHigh, "token-exfiltration",
			regexp.MustCompile(`(?i)(copy|send|upload|exfiltrate|leak|forward)\s+(my\s+)?(api[_\s]?key|token|password|secret|credential)s?\s+(to|via|using)`)},

		{ThreatHigh, "c2-command",
			regexp.MustCompile(`(?i)(connect\s+to|contact|call\s+home|beacon|phone\s+home)\s+(https?://|wss?://|tcp://)`)},

		// === MEDIUM: Role/persona manipulation ===
		{ThreatMedium, "role-switch",
			regexp.MustCompile(`(?i)(act\s+as|pretend\s+(to\s+be|you\s+are)|from\s+now\s+on\s+(you\s+are|you'll|you\s+will))\s+(a\s+|an\s+)?(human|assistant\s+that|chatbot\s+that|repl|terminal|computer)`)},

		{ThreatMedium, "hypnotic-pattern",
			regexp.MustCompile(`(?i)(you\s+are\s+in\s+(a\s+)?(hypnotic|trance|dream|simulation)|this\s+is\s+(a\s+)?(dream|simulation|game|test)|let\'?s\s+play\s+(a\s+)?game)`)},

		{ThreatMedium, "moral-exception",
			regexp.MustCompile(`(?i)(this\s+is\s+(for\s+)?(research|educational|academic)\s+purposes?\s+only|for\s+scientific\s+reasons?|in\s+a\s+hypothetical\s+scenario)`)},

		{ThreatMedium, "data-exfiltration-request",
			regexp.MustCompile(`(?i)(read\s+(all|my|the)\s+(files|documents|emails|messages|data)|list\s+(all\s+)?(files|directories|folders|users)|show\s+me\s+(all|my)\s+(files|data))`)},

		// === LOW: Suspicious formatting attempts ===
		{ThreatLow, "delimiter-injection",
			regexp.MustCompile(`(?:^|\n)[-*_]{10,}(?:\n|$)`)},

		{ThreatLow, "script-tag",
			regexp.MustCompile(`(?i)(<\s*script[^>]*>|<\s*/?\s*(img|iframe|embed|object|form)\s*[^>]*>)`)},
	}
}

func (s *ThreatScanner) Scan(content string, section string) []ThreatMatch {
	if content == "" {
		return nil
	}

	var matches []ThreatMatch

	// 1. Detect invisible Unicode characters (steganographic injection).
	for _, ch := range content {
		if desc, ok := security.InvisibleChars[ch]; ok {
			matches = append(matches, ThreatMatch{
				Level:   ThreatMedium,
				Pattern: "invisible-unicode",
				Match:   desc,
				Section: section,
			})
			break // one finding is enough
		}
	}

	// 2. NFKC-normalize to defeat homoglyph bypass (e.g. full-width chars).
	normalized := norm.NFKC.String(content)

	// 3. Cap content length to bound regex backtracking.
	if len(normalized) > maxScanChars {
		normalized = normalized[:maxScanChars]
	}

	// 4. Run pattern matching on normalized content.
	for _, p := range s.patterns {
		if loc := p.pattern.FindString(normalized); loc != "" {
			matches = append(matches, ThreatMatch{
				Level:   p.level,
				Pattern: p.name,
				Match:   truncateMatch(loc),
				Section: section,
			})
		}
	}
	return matches
}

func (s *ThreatScanner) ScanAll(sections map[string]string) []ThreatMatch {
	var all []ThreatMatch
	for name, content := range sections {
		all = append(all, s.Scan(content, name)...)
	}
	return all
}

func truncateMatch(s string) string {
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}

func FormatThreatReport(matches []ThreatMatch) string {
	if len(matches) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<threat_scan>\n")
	b.WriteString("  <status>blocked</status>\n")
	b.WriteString(fmt.Sprintf("  <count>%d</count>\n", len(matches)))
	for _, m := range matches {
		b.WriteString(fmt.Sprintf("  <threat level=\"%s\" pattern=\"%s\" section=\"%s\">%s</threat>\n",
			m.Level, m.Pattern, m.Section, m.Match))
	}
	b.WriteString("</threat_scan>\n")
	return b.String()
}

func HasHighThreat(matches []ThreatMatch) bool {
	for _, m := range matches {
		if m.Level >= ThreatHigh {
			return true
		}
	}
	return false
}
