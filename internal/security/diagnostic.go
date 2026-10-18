package security

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

var diagnosticURL = regexp.MustCompile(`(?i)\b(?:https?|wss?|postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis|amqp)://[^\s<>"']+`)
var diagnosticHeader = regexp.MustCompile(`(?im)\b((?:authorization|proxy-authorization|cookie|set-cookie)\s*:\s*)[^\r\n]+`)
var diagnosticBearer = regexp.MustCompile(`(?i)\b(?:Bearer|Basic)\s+[A-Za-z0-9+/._~=-]+`)
var diagnosticField = regexp.MustCompile(`(?i)(["']?\b[\w.-]*(?:password|passwd|token|secret|api[_-]?key|private[_-]?key|credential|auth|cookie)[\w.-]*["']?\s*[:=]\s*)(?:"(?:\\.|[^"\\])*"|'[^']*'|[^\s,;&]+)`)
var diagnosticToken = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{10,}|gh[pousr]_[A-Za-z0-9]{10,}|github_pat_[A-Za-z0-9_]{10,}|xox[baprs]-[A-Za-z0-9-]{10,}|AIza[A-Za-z0-9_-]{30,}|AKIA[A-Z0-9]{16}|eyJ[A-Za-z0-9_-]{10,}(?:\.[A-Za-z0-9_=-]{4,}){0,2})\b`)
var diagnosticPrivateKey = regexp.MustCompile(`(?s)-----BEGIN[A-Z ]*PRIVATE KEY-----.*?-----END[A-Z ]*PRIVATE KEY-----`)

func ErrorSummary(err error) string {
	if err == nil {
		return ""
	}
	return RedactDiagnostic(err.Error())
}

func RedactDiagnostic(text string) string {
	text = diagnosticPrivateKey.ReplaceAllString(text, "[REDACTED PRIVATE KEY]")
	text = diagnosticURL.ReplaceAllStringFunc(text, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" {
			return "[REDACTED URL]"
		}
		parsed.User = nil
		if parsed.RawQuery != "" {
			parsed.RawQuery = "redacted"
		}
		parsed.Fragment, parsed.RawFragment = "", ""
		return parsed.String()
	})
	text = diagnosticHeader.ReplaceAllString(text, "${1}[REDACTED]")
	text = diagnosticBearer.ReplaceAllString(text, "[REDACTED AUTH]")
	text = diagnosticField.ReplaceAllString(text, `${1}"[REDACTED]"`)
	text = diagnosticToken.ReplaceAllString(text, "[REDACTED TOKEN]")
	text = strings.Map(func(value rune) rune {
		if unicode.IsControl(value) || unicode.Is(unicode.Cf, value) {
			return ' '
		}
		return value
	}, text)
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > 4096 {
		return string(runes[:4096]) + "..."
	}
	return string(runes)
}
