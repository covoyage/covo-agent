package messageutil

import (
	"strings"
	"unicode/utf8"
)

// SplitLongText splits text into UTF-8-safe chunks no larger than maxBytes.
// Newline boundaries are preferred when one occurs within the current chunk.
func SplitLongText(text string, maxBytes int) []string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return []string{text}
	}

	chunks := make([]string, 0, len(text)/maxBytes+1)
	for len(text) > maxBytes {
		cut := maxBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		if cut == 0 {
			_, size := utf8.DecodeRuneInString(text)
			cut = size
		}
		if newline := strings.LastIndexByte(text[:cut], '\n'); newline >= 0 {
			cut = newline + 1
		}
		chunks = append(chunks, text[:cut])
		text = text[cut:]
	}
	if text != "" {
		chunks = append(chunks, text)
	}
	return chunks
}
