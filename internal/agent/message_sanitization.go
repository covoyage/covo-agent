package agent

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

func SanitizeSurrogates(text string) string {
	var b strings.Builder
	for _, r := range text {
		if r == '\uFFFD' {
			continue
		}
		if 0xD800 <= r && r <= 0xDFFF {
			b.WriteRune('\uFFFD')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func SanitizeStructureSurrogates(payload any) bool {
	found := false

	var walk func(node any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			for key, value := range v {
				switch sv := value.(type) {
				case string:
					if hasSurrogates(sv) {
						v[key] = SanitizeSurrogates(sv)
						found = true
					}
				case map[string]any, []any:
					walk(sv)
				}
			}
		case []any:
			for i, value := range v {
				switch sv := value.(type) {
				case string:
					if hasSurrogates(sv) {
						v[i] = SanitizeSurrogates(sv)
						found = true
					}
				case map[string]any, []any:
					walk(sv)
				}
			}
		}
	}
	walk(payload)
	return found
}

func hasSurrogates(text string) bool {
	for _, r := range text {
		if 0xD800 <= r && r <= 0xDFFF {
			return true
		}
	}
	return false
}

func SanitizeMessagesSurrogates(messages []map[string]any) bool {
	found := false
	skippedKeys := map[string]bool{
		"content":    true,
		"name":       true,
		"tool_calls": true,
		"role":       true,
	}

	for _, msg := range messages {
		content, ok := msg["content"]
		if ok {
			switch c := content.(type) {
			case string:
				if hasSurrogates(c) {
					msg["content"] = SanitizeSurrogates(c)
					found = true
				}
			case []any:
				for _, part := range c {
					if partMap, ok := part.(map[string]any); ok {
						text := partMap["text"]
						if txt, ok := text.(string); ok && hasSurrogates(txt) {
							partMap["text"] = SanitizeSurrogates(txt)
							found = true
						}
					}
				}
			}
		}

		name := msg["name"]
		if txt, ok := name.(string); ok && hasSurrogates(txt) {
			msg["name"] = SanitizeSurrogates(txt)
			found = true
		}

		toolCalls := msg["tool_calls"]
		if tcList, ok := toolCalls.([]any); ok {
			for _, tc := range tcList {
				if tcMap, ok := tc.(map[string]any); ok {
					tcID, _ := tcMap["id"]
					if idStr, ok := tcID.(string); ok && hasSurrogates(idStr) {
						tcMap["id"] = SanitizeSurrogates(idStr)
						found = true
					}
					fn, _ := tcMap["function"]
					if fnMap, ok := fn.(map[string]any); ok {
						fnName, _ := fnMap["name"]
						if nameStr, ok := fnName.(string); ok && hasSurrogates(nameStr) {
							fnMap["name"] = SanitizeSurrogates(nameStr)
							found = true
						}
						fnArgs, _ := fnMap["arguments"]
						if argsStr, ok := fnArgs.(string); ok && hasSurrogates(argsStr) {
							fnMap["arguments"] = SanitizeSurrogates(argsStr)
							found = true
						}
					}
				}
			}
		}

		for key, value := range msg {
			if skippedKeys[key] {
				continue
			}
			switch sv := value.(type) {
			case string:
				if hasSurrogates(sv) {
					msg[key] = SanitizeSurrogates(sv)
					found = true
				}
			case map[string]any, []any:
				if SanitizeStructureSurrogates(sv) {
					found = true
				}
			}
		}
	}
	return found
}

func EscapeInvalidCharsInJSONStrings(raw string) string {
	var out strings.Builder
	inString := false
	s := raw
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inString {
			if ch == '\\' && i+1 < len(s) {
				out.WriteByte(ch)
				out.WriteByte(s[i+1])
				i++
				continue
			}
			if ch == '"' {
				inString = false
				out.WriteByte(ch)
			} else if ch < 0x20 {
				out.WriteString(string([]byte{'\\', 'u', '0', '0', hexDigit(ch >> 4), hexDigit(ch & 0x0F)}))
			} else {
				out.WriteByte(ch)
			}
		} else {
			if ch == '"' {
				inString = true
			}
			out.WriteByte(ch)
		}
	}
	return out.String()
}

func hexDigit(n byte) byte {
	if n < 10 {
		return '0' + n
	}
	return 'a' + (n - 10)
}

func RepairToolCallArguments(rawArgs string, toolName string) string {
	rawStripped := strings.TrimSpace(rawArgs)
	if rawStripped == "" || rawStripped == "None" {
		return "{}"
	}

	var parsed any
	if err := json.Unmarshal([]byte(rawStripped), &parsed); err == nil {
		reserialised, err := json.Marshal(parsed)
		if err == nil {
			result := string(reserialised)
			if result != rawStripped {
				return result
			}
			return rawStripped
		}
	}

	fixed := rawStripped

	fixed = trailingCommaRe.Replace(fixed)

	openCurly := strings.Count(fixed, "{") - strings.Count(fixed, "}")
	openBracket := strings.Count(fixed, "[") - strings.Count(fixed, "]")
	if openCurly > 0 {
		fixed += strings.Repeat("}", openCurly)
	}
	if openBracket > 0 {
		fixed += strings.Repeat("]", openBracket)
	}

	for range 50 {
		if _, err := json.Marshal(json.RawMessage(fixed)); err == nil {
			var test any
			if json.Unmarshal([]byte(fixed), &test) == nil {
				break
			}
		}

		if strings.HasSuffix(fixed, "}") && strings.Count(fixed, "}") > strings.Count(fixed, "{") {
			fixed = fixed[:len(fixed)-1]
		} else if strings.HasSuffix(fixed, "]") && strings.Count(fixed, "]") > strings.Count(fixed, "[") {
			fixed = fixed[:len(fixed)-1]
		} else {
			break
		}
	}

	var test any
	if err := json.Unmarshal([]byte(fixed), &test); err == nil {
		return fixed
	}

	return "{}"
}

var trailingCommaRe = mustCompileTrailingComma()

func mustCompileTrailingComma() *strings.Replacer {
	return strings.NewReplacer(",}", "}", ",]", "]")
}

func SanitizeText(text string) string {
	if text == "" {
		return text
	}

	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "\uFFFD")
	}

	if hasSurrogates(text) {
		text = SanitizeSurrogates(text)
	}

	return text
}
