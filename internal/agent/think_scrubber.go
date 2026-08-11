package agent

import "strings"

var openTagNames = []string{
	"think",
	"thinking",
	"reasoning",
	"thought",
	"REASONING_SCRATCHPAD",
}

var openTags []string
var closeTags []string
var maxTagLen int

func init() {
	for _, name := range openTagNames {
		openTags = append(openTags, "<"+name+">")
		closeTags = append(closeTags, "</"+name+">")
	}
	for _, tag := range openTags {
		if len(tag) > maxTagLen {
			maxTagLen = len(tag)
		}
	}
	for _, tag := range closeTags {
		if len(tag) > maxTagLen {
			maxTagLen = len(tag)
		}
	}
}

type StreamingThinkScrubber struct {
	inBlock                 bool
	buf                     string
	lastEmittedEndedNewline bool
}

func NewStreamingThinkScrubber() *StreamingThinkScrubber {
	return &StreamingThinkScrubber{
		lastEmittedEndedNewline: true,
	}
}

func (s *StreamingThinkScrubber) Reset() {
	s.inBlock = false
	s.buf = ""
	s.lastEmittedEndedNewline = true
}

func (s *StreamingThinkScrubber) Feed(text string) string {
	if text == "" {
		return ""
	}

	buf := s.buf + text
	s.buf = ""
	var out strings.Builder

	for len(buf) > 0 {
		if s.inBlock {
			closeIdx, closeLen := findFirstTag(buf, closeTags)
			if closeIdx == -1 {
				held := maxPartialSuffix(buf, closeTags)
				if held > 0 {
					s.buf = buf[len(buf)-held:]
				}
				return out.String()
			}
			buf = buf[closeIdx+closeLen:]
			s.inBlock = false
		} else {
			pair := findEarliestClosedPair(buf)
			openIdx, openLen := findOpenAtBoundary(buf, &out, s.lastEmittedEndedNewline)

			if pair != nil && (openIdx == -1 || pair[0] <= openIdx) {
				startIdx, endIdx := pair[0], pair[1]
				preceding := buf[:startIdx]
				if preceding != "" {
					preceding = stripOrphanCloseTags(preceding)
					if preceding != "" {
						out.WriteString(preceding)
						s.lastEmittedEndedNewline = strings.HasSuffix(preceding, "\n")
					}
				}
				buf = buf[endIdx:]
				continue
			}

			if openIdx != -1 {
				preceding := buf[:openIdx]
				if preceding != "" {
					preceding = stripOrphanCloseTags(preceding)
					if preceding != "" {
						out.WriteString(preceding)
						s.lastEmittedEndedNewline = strings.HasSuffix(preceding, "\n")
					}
				}
				s.inBlock = true
				buf = buf[openIdx+openLen:]
				continue
			}

			held := maxPartialSuffix(buf, openTags)
			heldClose := maxPartialSuffix(buf, closeTags)
			if heldClose > held {
				held = heldClose
			}
			if held > 0 {
				emitText := buf[:len(buf)-held]
				s.buf = buf[len(buf)-held:]
				if emitText != "" {
					emitText = stripOrphanCloseTags(emitText)
					if emitText != "" {
						out.WriteString(emitText)
						s.lastEmittedEndedNewline = strings.HasSuffix(emitText, "\n")
					}
				}
				return out.String()
			}

			emitText := buf
			s.buf = ""
			emitText = stripOrphanCloseTags(emitText)
			if emitText != "" {
				out.WriteString(emitText)
				s.lastEmittedEndedNewline = strings.HasSuffix(emitText, "\n")
			}
			return out.String()
		}
	}

	return out.String()
}

func (s *StreamingThinkScrubber) Flush() string {
	if s.inBlock {
		s.buf = ""
		s.inBlock = false
		return ""
	}
	tail := s.buf
	s.buf = ""
	if tail == "" {
		return ""
	}
	tail = stripOrphanCloseTags(tail)
	if tail != "" {
		s.lastEmittedEndedNewline = strings.HasSuffix(tail, "\n")
	}
	return tail
}

func findFirstTag(buf string, tags []string) (int, int) {
	bufLower := strings.ToLower(buf)
	bestIdx := -1
	bestLen := 0
	for _, tag := range tags {
		idx := strings.Index(bufLower, strings.ToLower(tag))
		if idx != -1 && (bestIdx == -1 || idx < bestIdx) {
			bestIdx = idx
			bestLen = len(tag)
		}
	}
	return bestIdx, bestLen
}

func findEarliestClosedPair(buf string) []int {
	bufLower := strings.ToLower(buf)
	bestStart := -1
	bestEnd := -1

	for i, openTag := range openTags {
		closeTag := closeTags[i]
		openLower := strings.ToLower(openTag)
		closeLower := strings.ToLower(closeTag)

		startIdx := strings.Index(bufLower, openLower)
		if startIdx == -1 {
			continue
		}

		afterOpen := bufLower[startIdx+len(openLower):]
		endIdx := strings.Index(afterOpen, closeLower)
		if endIdx == -1 {
			continue
		}

		absEnd := startIdx + len(openLower) + endIdx + len(closeLower)
		if bestStart == -1 || startIdx < bestStart {
			bestStart = startIdx
			bestEnd = absEnd
		}
	}

	if bestStart == -1 {
		return nil
	}
	return []int{bestStart, bestEnd}
}

func findOpenAtBoundary(buf string, out *strings.Builder, lastNewline bool) (int, int) {
	bufLower := strings.ToLower(buf)
	bestIdx := -1
	bestLen := 0

	for _, tag := range openTags {
		idx := strings.Index(bufLower, strings.ToLower(tag))
		if idx == -1 {
			continue
		}
		if bestIdx == -1 || idx < bestIdx {
			bestIdx = idx
			bestLen = len(tag)
		}
	}

	if bestIdx == -1 {
		return -1, 0
	}

	if bestIdx == 0 {
		if lastNewline || out.Len() == 0 {
			return bestIdx, bestLen
		}
		emitted := out.String()
		if emitted == "" || strings.TrimSpace(emitted) == "" {
			return bestIdx, bestLen
		}
	}

	if bestIdx > 0 && buf[bestIdx-1] == '\n' {
		afterNewline := buf[bestIdx:]
		if strings.TrimLeft(afterNewline, " \t") == afterNewline || !strings.HasPrefix(strings.TrimLeft(afterNewline, " \t"), strings.ToLower(openTags[0])) {
			return bestIdx, bestLen
		}
	}

	return -1, 0
}

func maxPartialSuffix(buf string, tags []string) int {
	if len(buf) == 0 {
		return 0
	}
	maxHeld := 0
	for _, tag := range tags {
		tagLen := len(tag)
		for i := 1; i < tagLen && i <= len(buf); i++ {
			if strings.EqualFold(buf[len(buf)-i:], tag[:i]) {
				if i > maxHeld {
					maxHeld = i
				}
			}
		}
	}
	return maxHeld
}

func stripOrphanCloseTags(text string) string {
	for _, tag := range closeTags {
		text = strings.ReplaceAll(text, tag, "")
		text = strings.ReplaceAll(text, strings.ToUpper(tag), "")
	}
	return text
}
