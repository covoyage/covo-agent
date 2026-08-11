package agent

import (
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
)

var redactEnabled bool

func init() {
	v := os.Getenv("COVO_REDACT_SECRETS")
	redactEnabled = v == "" || v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes") || strings.EqualFold(v, "on")
}

var sensitiveQueryParams = map[string]bool{
	"access_token":    true,
	"refresh_token":   true,
	"id_token":        true,
	"token":           true,
	"api_key":         true,
	"apikey":          true,
	"client_secret":   true,
	"password":        true,
	"auth":            true,
	"jwt":             true,
	"session":         true,
	"secret":          true,
	"key":             true,
	"code":            true,
	"signature":       true,
	"x-amz-signature": true,
}

var sensitiveBodyKeys = map[string]bool{
	"access_token":  true,
	"refresh_token": true,
	"id_token":      true,
	"token":         true,
	"api_key":       true,
	"apikey":        true,
	"client_secret": true,
	"password":      true,
	"auth":          true,
	"jwt":           true,
	"secret":        true,
	"private_key":   true,
	"authorization": true,
	"key":           true,
}

var prefixPatterns = []string{
	`sk-[A-Za-z0-9_-]{10,}`,
	`ghp_[A-Za-z0-9]{10,}`,
	`github_pat_[A-Za-z0-9_]{10,}`,
	`gho_[A-Za-z0-9]{10,}`,
	`ghu_[A-Za-z0-9]{10,}`,
	`ghs_[A-Za-z0-9]{10,}`,
	`ghr_[A-Za-z0-9]{10,}`,
	`xox[baprs]-[A-Za-z0-9-]{10,}`,
	`AIza[A-Za-z0-9_-]{30,}`,
	`pplx-[A-Za-z0-9]{10,}`,
	`fal_[A-Za-z0-9_-]{10,}`,
	`fc-[A-Za-z0-9]{10,}`,
	`bb_live_[A-Za-z0-9_-]{10,}`,
	`gAAAA[A-Za-z0-9_=-]{20,}`,
	`AKIA[A-Z0-9]{16}`,
	`sk_live_[A-Za-z0-9]{10,}`,
	`sk_test_[A-Za-z0-9]{10,}`,
	`rk_live_[A-Za-z0-9]{10,}`,
	`SG\.[A-Za-z0-9_-]{10,}`,
	`hf_[A-Za-z0-9]{10,}`,
	`r8_[A-Za-z0-9]{10,}`,
	`npm_[A-Za-z0-9]{10,}`,
	`pypi-[A-Za-z0-9_-]{10,}`,
	`dop_v1_[A-Za-z0-9]{10,}`,
	`doo_v1_[A-Za-z0-9]{10,}`,
	`am_[A-Za-z0-9_-]{10,}`,
	`sk_[A-Za-z0-9_]{10,}`,
	`tvly-[A-Za-z0-9]{10,}`,
	`exa_[A-Za-z0-9]{10,}`,
	`gsk_[A-Za-z0-9]{10,}`,
	`syt_[A-Za-z0-9]{10,}`,
	`retaindb_[A-Za-z0-9]{10,}`,
	`hsk-[A-Za-z0-9]{10,}`,
	`mem0_[A-Za-z0-9]{10,}`,
	`brv_[A-Za-z0-9]{10,}`,
	`xai-[A-Za-z0-9]{30,}`,
}

var (
	prefixRe         = regexp.MustCompile(`\b(` + strings.Join(prefixPatterns, "|") + `)\b`)
	envAssignRe      = regexp.MustCompile(`([A-Z0-9_]{0,50}(?:API_?KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|AUTH)[A-Z0-9_]{0,50})\s*=\s*(\S+)`)
	jsonFieldRe      = regexp.MustCompile(`(?i)("(?:api_?[Kk]ey|token|secret|password|access_token|refresh_token|auth_token|bearer|secret_value|raw_secret|secret_input|key_material)")\s*:\s*"([^"]+)"`)
	authHeaderRe     = regexp.MustCompile(`(?i)(Authorization:\s*Bearer\s+)(\S+)`)
	telegramRe       = regexp.MustCompile(`(bot)?(\d{8,}):([-A-Za-z0-9_]{30,})`)
	privateKeyRe     = regexp.MustCompile(`(?s)-----BEGIN[A-Z ]*PRIVATE KEY-----.*?-----END[A-Z ]*PRIVATE KEY-----`)
	dbConnstrRe      = regexp.MustCompile(`(?i)((?:postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis|amqp)://[^:]+:)([^@]+)(@)`)
	jwtRe            = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}(?:\.[A-Za-z0-9_=-]{4,}){0,2}`)
	discordMentionRe = regexp.MustCompile(`<@!?(\d{17,20})>`)
	signalPhoneRe    = regexp.MustCompile(`(\+[1-9]\d{6,14})\b`)
	urlUserinfoRe    = regexp.MustCompile(`(https?|wss?|ftp)://([^/\s:@]+):([^/\s@]+)@`)
	formBodyRe       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*=[^&\s]*(?:&[A-Za-z_][A-Za-z0-9_.-]*=[^&\s]*)*$`)
)

var prefixSubstrings []string

func init() {
	for _, p := range prefixPatterns {
		prefixSubstrings = append(prefixSubstrings, extractLiteralPrefix(p))
	}
}

func extractLiteralPrefix(pattern string) string {
	meta := `[(\\.?*+|{^$`
	for i, ch := range pattern {
		if strings.ContainsRune(meta, ch) {
			return pattern[:i]
		}
	}
	return pattern
}

func hasKnownPrefixSubstring(text string) bool {
	for _, p := range prefixSubstrings {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

func MaskSecret(value string, head, tail, floor int, placeholder, empty string) string {
	if value == "" {
		return empty
	}
	if len(value) < floor {
		return placeholder
	}
	return value[:head] + "..." + value[len(value)-tail:]
}

func maskToken(token string) string {
	if token == "" {
		return "***"
	}
	return MaskSecret(token, 6, 4, 18, "***", "***")
}

func redactQueryString(query string) string {
	if query == "" {
		return query
	}
	parts := strings.Split(query, "&")
	for i, pair := range parts {
		if !strings.Contains(pair, "=") {
			continue
		}
		eqIdx := strings.Index(pair, "=")
		key := pair[:eqIdx]
		if sensitiveQueryParams[strings.ToLower(key)] {
			parts[i] = key + "=***"
		}
	}
	return strings.Join(parts, "&")
}

func redactFormBody(text string) string {
	if text == "" || strings.Contains(text, "\n") || !strings.Contains(text, "=") {
		return text
	}
	trimmed := strings.TrimSpace(text)
	if !formBodyRe.MatchString(trimmed) {
		return text
	}
	return redactQueryString(trimmed)
}

func redactJSONBody(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return text
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return text
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return text
	}
	if !redactJSONValue(value) {
		return text
	}
	redacted, err := json.Marshal(value)
	if err != nil {
		return text
	}
	return string(redacted)
}

func redactJSONValue(value any) bool {
	changed := false
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if sensitiveBodyKeys[strings.ToLower(key)] {
				typed[key] = "***"
				changed = true
				continue
			}
			changed = redactJSONValue(child) || changed
		}
	case []any:
		for _, child := range typed {
			changed = redactJSONValue(child) || changed
		}
	}
	return changed
}

func RedactSensitiveText(text string, force bool, codeFile bool) string {
	if text == "" {
		return text
	}
	if !force && !redactEnabled {
		return text
	}

	if hasKnownPrefixSubstring(text) {
		text = prefixRe.ReplaceAllStringFunc(text, func(m string) string {
			return maskToken(m)
		})
	}

	if !codeFile {
		text = redactJSONBody(text)

		if strings.Contains(text, "=") {
			text = envAssignRe.ReplaceAllStringFunc(text, func(m string) string {
				parts := envAssignRe.FindStringSubmatch(m)
				if len(parts) < 3 {
					return m
				}
				name, value := parts[1], parts[2]
				clean := strings.Trim(value, `"'`)
				return name + "=" + strings.Replace(value, clean, maskToken(clean), 1)
			})
		}

		if strings.Contains(text, ":") && strings.Contains(text, `"`) {
			text = jsonFieldRe.ReplaceAllStringFunc(text, func(m string) string {
				parts := jsonFieldRe.FindStringSubmatch(m)
				if len(parts) < 3 {
					return m
				}
				key, value := parts[1], parts[2]
				return key + `: "` + maskToken(value) + `"`
			})
		}
	}

	if strings.Contains(text, "uthorization") || strings.Contains(text, "UTHORIZATION") {
		text = authHeaderRe.ReplaceAllStringFunc(text, func(m string) string {
			parts := authHeaderRe.FindStringSubmatch(m)
			if len(parts) < 3 {
				return m
			}
			return parts[1] + maskToken(parts[2])
		})
	}

	if strings.Contains(text, ":") {
		text = telegramRe.ReplaceAllStringFunc(text, func(m string) string {
			parts := telegramRe.FindStringSubmatch(m)
			if len(parts) < 3 {
				return m
			}
			prefix := parts[1]
			digits := parts[2]
			return prefix + digits + ":***"
		})
	}

	if strings.Contains(text, "BEGIN") && strings.Contains(text, "-----") {
		text = privateKeyRe.ReplaceAllString(text, "[REDACTED PRIVATE KEY]")
	}

	if strings.Contains(text, "://") {
		text = urlUserinfoRe.ReplaceAllString(text, `$1://$2:***@`)
		text = dbConnstrRe.ReplaceAllStringFunc(text, func(m string) string {
			parts := dbConnstrRe.FindStringSubmatch(m)
			if len(parts) < 4 {
				return m
			}
			return parts[1] + "***" + parts[3]
		})
	}

	if strings.Contains(text, "eyJ") {
		text = jwtRe.ReplaceAllStringFunc(text, func(m string) string {
			return maskToken(m)
		})
	}

	if strings.Contains(text, "=") {
		text = redactFormBody(text)
	}

	if strings.Contains(text, "<@") {
		text = discordMentionRe.ReplaceAllStringFunc(text, func(m string) string {
			excl := ""
			if strings.Contains(m, "!") {
				excl = "!"
			}
			return "<@" + excl + "***>"
		})
	}

	if strings.Contains(text, "+") {
		text = signalPhoneRe.ReplaceAllStringFunc(text, func(m string) string {
			phone := signalPhoneRe.FindStringSubmatch(m)
			if len(phone) < 2 {
				return m
			}
			p := phone[1]
			if len(p) <= 8 {
				return p[:2] + "****" + p[len(p)-2:]
			}
			return p[:4] + "****" + p[len(p)-4:]
		})
	}

	return text
}

func RedactSensitiveTextForce(text string) string {
	return RedactSensitiveText(text, true, false)
}
