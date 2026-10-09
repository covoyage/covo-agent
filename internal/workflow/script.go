package workflow

import (
	"fmt"
	"regexp"
	"strings"
)

// Script is a compact, checkable workflow program. It is not a general
// programming language: only phase(), ask(), and spawn() statements are
// accepted so the graph can be recovered statically.
type Script struct {
	Source string
	Name   string
}

type Diagnostic struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

type Site struct {
	Kind  string `json:"kind"` // phase, ask, spawn
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Line  int    `json:"line"`
}

type Analysis struct {
	Name        string       `json:"name,omitempty"`
	Sites       []Site       `json:"sites"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Mermaid     string       `json:"mermaid"`
	Phases      []Phase      `json:"phases"`
	AskCount    int          `json:"ask_count"`
	SpawnCount  int          `json:"spawn_count"`
}

var (
	stmtPhase = regexp.MustCompile(`(?i)^\s*phase\s*\(\s*"([^"]+)"\s*(?:,\s*"([^"]*)")?\s*\)\s*$`)
	stmtAsk   = regexp.MustCompile(`(?i)^\s*ask\s*\(\s*"([^"]+)"\s*\)\s*$`)
	stmtSpawn = regexp.MustCompile(`(?i)^\s*spawn\s*\(\s*"([^"]+)"\s*(?:,\s*"([^"]*)")?\s*\)\s*$`)
	stmtName  = regexp.MustCompile(`(?i)^\s*name\s*\(\s*"([^"]+)"\s*\)\s*$`)
)

// Analyze type-checks a workflow script and builds the site graph.
func Analyze(src string) Analysis {
	a := Analysis{}
	lines := strings.Split(src, "\n")
	seen := map[string]int{}
	var mermaid strings.Builder
	mermaid.WriteString("flowchart TD\n")
	prev := ""
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		n := i + 1
		switch {
		case stmtName.MatchString(line):
			m := stmtName.FindStringSubmatch(line)
			a.Name = m[1]
		case stmtPhase.MatchString(line):
			m := stmtPhase.FindStringSubmatch(line)
			id, prompt := m[1], m[2]
			if prompt == "" {
				prompt = id
			}
			if prevID, ok := seen[id]; ok {
				a.Diagnostics = append(a.Diagnostics, Diagnostic{Line: n, Message: fmt.Sprintf("duplicate site %q (first at line %d)", id, prevID)})
				continue
			}
			seen[id] = n
			a.Sites = append(a.Sites, Site{Kind: "phase", ID: id, Label: prompt, Line: n})
			a.Phases = append(a.Phases, Phase{ID: id, Name: id, Prompt: prompt})
			node := sanitizeID(id)
			fmt.Fprintf(&mermaid, "  %s[\"phase %s\"]\n", node, escapeLabel(id))
			if prev != "" {
				fmt.Fprintf(&mermaid, "  %s --> %s\n", prev, node)
			}
			prev = node
		case stmtAsk.MatchString(line):
			m := stmtAsk.FindStringSubmatch(line)
			id := m[1]
			if prevID, ok := seen["ask:"+id]; ok {
				a.Diagnostics = append(a.Diagnostics, Diagnostic{Line: n, Message: fmt.Sprintf("duplicate ask %q (first at line %d)", id, prevID)})
				continue
			}
			seen["ask:"+id] = n
			a.Sites = append(a.Sites, Site{Kind: "ask", ID: id, Line: n})
			a.AskCount++
			node := sanitizeID("ask_" + id)
			fmt.Fprintf(&mermaid, "  %s{{\"ask %s\"}}\n", node, escapeLabel(id))
			if prev != "" {
				fmt.Fprintf(&mermaid, "  %s --> %s\n", prev, node)
			}
			prev = node
		case stmtSpawn.MatchString(line):
			m := stmtSpawn.FindStringSubmatch(line)
			id, role := m[1], m[2]
			if role == "" {
				role = "leaf"
			}
			key := "spawn:" + id
			if prevID, ok := seen[key]; ok {
				a.Diagnostics = append(a.Diagnostics, Diagnostic{Line: n, Message: fmt.Sprintf("duplicate spawn %q (first at line %d)", id, prevID)})
				continue
			}
			seen[key] = n
			a.Sites = append(a.Sites, Site{Kind: "spawn", ID: id, Label: role, Line: n})
			a.SpawnCount++
			a.Phases = append(a.Phases, Phase{ID: id, Name: id, Prompt: id, Role: role})
			node := sanitizeID("spawn_" + id)
			fmt.Fprintf(&mermaid, "  %s[\"spawn %s\"]\n", node, escapeLabel(id))
			if prev != "" {
				fmt.Fprintf(&mermaid, "  %s --> %s\n", prev, node)
			}
			prev = node
		default:
			a.Diagnostics = append(a.Diagnostics, Diagnostic{
				Line:    n,
				Message: "unsupported statement; only name(), phase(), ask(), spawn() are allowed",
			})
		}
	}
	if len(a.Sites) == 0 && len(a.Diagnostics) == 0 {
		a.Diagnostics = append(a.Diagnostics, Diagnostic{Line: 1, Message: "empty workflow script"})
	}
	a.Mermaid = mermaid.String()
	return a
}

func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "n"
	}
	return out
}

func escapeLabel(s string) string {
	return strings.ReplaceAll(s, `"`, `'`)
}
