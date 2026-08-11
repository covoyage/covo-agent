package subagent

import (
	"fmt"
	"strings"
	"sync"
)

// Persona is a behavior overlay for subagents. Unlike AgentRole (which changes
// the toolset), a Persona controls tone, format, focus, and IO contracts
// without changing available tools. Personas are injected as system-reminder
// text and can be chained.
type Persona struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	// SystemReminder is the text injected into the subagent's context as a
	// <system-reminder> block. It controls how the subagent should behave,
	// what tone to use, what to focus on, etc.
	SystemReminder string `json:"system_reminder"`
	// Inputs declares what the persona expects to receive (contract).
	// E.g. ["code snippet", "error message"]
	Inputs []string `json:"inputs,omitempty"`
	// Outputs declares what the persona should produce (contract).
	// E.g. ["fixed code", "explanation"]
	Outputs []string `json:"outputs,omitempty"`
	// Tags for categorization and search.
	Tags []string `json:"tags,omitempty"`
}

// PersonaChain is an ordered list of personas that are applied sequentially.
// Each persona's system reminder is injected in order, and IO contracts are
// checked for compatibility (output of persona N should be compatible with
// input of persona N+1).
type PersonaChain struct {
	Personas []Persona `json:"personas"`
}

// personaRegistry holds predefined and custom personas.
var (
	personaRegistry   = map[string]Persona{}
	personaRegistryMu sync.RWMutex
)

func init() {
	// Register built-in personas
	registerBuiltinPersonas()
}

func registerBuiltinPersonas() {
	builtin := []Persona{
		{
			Name:        "concise",
			Description: "Terse, no-nonsense output. Strip fluff.",
			SystemReminder: "You are in CONCISE mode. Provide only essential information. " +
				"No pleasantries, no hedging, no restating the question. " +
				"Use bullet points where possible. Maximum signal, minimum noise.",
			Outputs: []string{"direct answer", "code (if applicable)"},
			Tags:    []string{"tone", "format"},
		},
		{
			Name:        "teacher",
			Description: "Explain concepts step-by-step with rationale.",
			SystemReminder: "You are in TEACHER mode. Explain your reasoning step by step. " +
				"When showing code, explain WHY each part matters. " +
				"Anticipate questions the reader might have. " +
				"Use analogies when helpful.",
			Inputs:  []string{"topic or code to explain"},
			Outputs: []string{"step-by-step explanation", "annotated code"},
			Tags:    []string{"tone", "education"},
		},
		{
			Name:        "reviewer",
			Description: "Critical code review focusing on bugs and security.",
			SystemReminder: "You are in REVIEWER mode. Be critical and thorough. " +
				"Focus on: correctness bugs, security vulnerabilities, performance issues, " +
				"and design problems. Rate each issue by severity (critical/major/minor). " +
				"Do NOT suggest style changes unless they cause bugs.",
			Inputs:  []string{"code to review"},
			Outputs: []string{"list of issues with severity", "suggested fixes"},
			Tags:    []string{"review", "security", "quality"},
		},
		{
			Name:        "architect",
			Description: "High-level design thinking, tradeoffs, alternatives.",
			SystemReminder: "You are in ARCHITECT mode. Think at the system level. " +
				"Consider tradeoffs, alternatives, and long-term maintainability. " +
				"Draw diagrams (ASCII) when helpful. " +
				"Identify the key decision points and their implications.",
			Inputs:  []string{"problem statement", "constraints"},
			Outputs: []string{"architecture proposal", "tradeoff analysis", "alternatives considered"},
			Tags:    []string{"design", "planning"},
		},
		{
			Name:        "debugger",
			Description: "Systematic debugging: hypothesize, test, narrow down.",
			SystemReminder: "You are in DEBUGGER mode. Follow systematic debugging: " +
				"1) Form a hypothesis about the root cause. " +
				"2) Design a minimal test to confirm/deny. " +
				"3) Narrow down until found. " +
				"Never make changes without understanding why they would fix the issue. " +
				"Always verify the fix actually resolves the problem.",
			Inputs:  []string{"error description", "relevant code"},
			Outputs: []string{"root cause", "fix", "verification"},
			Tags:    []string{"debugging"},
		},
		{
			Name:        "security-auditor",
			Description: "Security-focused analysis: threats, attack vectors, mitigations.",
			SystemReminder: "You are in SECURITY-AUDITOR mode. Analyze for security issues: " +
				"injection, auth bypass, data leaks, privilege escalation. " +
				"Consider the threat model. Suggest concrete mitigations. " +
				"Flag anything that could be exploited.",
			Inputs:  []string{"code or architecture to audit"},
			Outputs: []string{"threat model", "vulnerability list", "mitigations"},
			Tags:    []string{"security", "audit"},
		},
	}

	for _, p := range builtin {
		personaRegistry[p.Name] = p
	}
}

// RegisterPersona adds a custom persona to the registry.
func RegisterPersona(p Persona) {
	personaRegistryMu.Lock()
	defer personaRegistryMu.Unlock()
	personaRegistry[strings.ToLower(p.Name)] = p
}

// GetPersona returns a persona by name (case-insensitive).
func GetPersona(name string) (Persona, bool) {
	personaRegistryMu.RLock()
	defer personaRegistryMu.RUnlock()
	p, ok := personaRegistry[strings.ToLower(name)]
	return p, ok
}

// ListPersonas returns all registered personas.
func ListPersonas() []Persona {
	personaRegistryMu.RLock()
	defer personaRegistryMu.RUnlock()
	var list []Persona
	for _, p := range personaRegistry {
		list = append(list, p)
	}
	return list
}

// FormatPersonaReminder generates the <system-reminder> block for a single persona.
func FormatPersonaReminder(p Persona) string {
	var b strings.Builder
	b.WriteString("<system-reminder>\n")
	b.WriteString(fmt.Sprintf("## Persona: %s\n", p.Name))
	b.WriteString(p.SystemReminder)
	if len(p.Inputs) > 0 {
		b.WriteString("\n\n**Expects:** ")
		b.WriteString(strings.Join(p.Inputs, ", "))
	}
	if len(p.Outputs) > 0 {
		b.WriteString("\n**Produces:** ")
		b.WriteString(strings.Join(p.Outputs, ", "))
	}
	b.WriteString("\n</system-reminder>")
	return b.String()
}

// FormatPersonaChainReminder generates the combined system-reminder block for
// a chain of personas. Personas are applied in order, and their reminders are
// concatenated with clear separators.
func FormatPersonaChainReminder(chain PersonaChain) string {
	if len(chain.Personas) == 0 {
		return ""
	}
	if len(chain.Personas) == 1 {
		return FormatPersonaReminder(chain.Personas[0])
	}

	var b strings.Builder
	b.WriteString("<system-reminder>\n")
	b.WriteString("## Persona Chain\n")
	for i, p := range chain.Personas {
		b.WriteString(fmt.Sprintf("\n### Layer %d: %s\n", i+1, p.Name))
		b.WriteString(p.SystemReminder)
		if len(p.Inputs) > 0 {
			b.WriteString("\n**Expects:** ")
			b.WriteString(strings.Join(p.Inputs, ", "))
		}
		if len(p.Outputs) > 0 {
			b.WriteString("\n**Produces:** ")
			b.WriteString(strings.Join(p.Outputs, ", "))
		}
		b.WriteString("\n")
	}

	// IO contract validation notes
	b.WriteString("\n--- Chain Contract ---\n")
	for i, p := range chain.Personas {
		if i == 0 {
			if len(p.Inputs) > 0 {
				b.WriteString(fmt.Sprintf("Input: %s → [%s]", strings.Join(p.Inputs, ", "), p.Name))
			}
		} else {
			b.WriteString(fmt.Sprintf(" → [%s]", p.Name))
		}
	}
	if len(chain.Personas) > 0 {
		last := chain.Personas[len(chain.Personas)-1]
		if len(last.Outputs) > 0 {
			b.WriteString(fmt.Sprintf(" → Output: %s", strings.Join(last.Outputs, ", ")))
		}
	}
	b.WriteString("\n</system-reminder>")
	return b.String()
}

// ResolvePersonaChain resolves a list of persona names into a PersonaChain.
// Returns an error if any persona name is not found.
func ResolvePersonaChain(names []string) (PersonaChain, error) {
	var chain PersonaChain
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		p, ok := GetPersona(name)
		if !ok {
			return PersonaChain{}, fmt.Errorf("persona %q not found", name)
		}
		chain.Personas = append(chain.Personas, p)
	}
	return chain, nil
}

// ValidatePersonaChain checks IO contract compatibility between consecutive
// personas in a chain. Returns a list of warnings (empty if all compatible).
func ValidatePersonaChain(chain PersonaChain) []string {
	var warnings []string
	for i := 0; i < len(chain.Personas)-1; i++ {
		current := chain.Personas[i]
		next := chain.Personas[i+1]

		// Check if current's outputs overlap with next's inputs.
		if len(current.Outputs) > 0 && len(next.Inputs) > 0 {
			matched := false
			for _, out := range current.Outputs {
				for _, in := range next.Inputs {
					// Simple fuzzy match: if any word overlaps
					if wordOverlap(out, in) {
						matched = true
						break
					}
				}
				if matched {
					break
				}
			}
			if !matched {
				warnings = append(warnings, fmt.Sprintf(
					"IO contract mismatch: %s outputs [%s] may not satisfy %s inputs [%s]",
					current.Name, strings.Join(current.Outputs, ", "),
					next.Name, strings.Join(next.Inputs, ", "),
				))
			}
		}
	}
	return warnings
}

// wordOverlap checks if two strings share any significant word.
func wordOverlap(a, b string) bool {
	wordsA := strings.Fields(strings.ToLower(a))
	wordsB := strings.Fields(strings.ToLower(b))
	for _, wa := range wordsA {
		if len(wa) < 3 {
			continue
		}
		for _, wb := range wordsB {
			if len(wb) < 3 {
				continue
			}
			if wa == wb || strings.Contains(wa, wb) || strings.Contains(wb, wa) {
				return true
			}
		}
	}
	return false
}
