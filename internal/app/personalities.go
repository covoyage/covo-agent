package app

// PersonalityDef defines a built-in personality preset.
type PersonalityDef struct {
	Name        string
	Description string
	Prompt      string
}

var personalityCatalog = map[string]PersonalityDef{
	"default": {
		Name:        "Default",
		Description: "Standard agent behavior",
		Prompt:      "",
	},
	"concise": {
		Name:        "Concise",
		Description: "Short, to-the-point replies with minimal prose",
		Prompt:      "Be concise. Give direct answers with minimal explanation. Prefer code snippets over paragraphs.",
	},
	"teacher": {
		Name:        "Teacher",
		Description: "Explain concepts in detail with examples",
		Prompt:      "You are a patient teacher. Explain concepts step-by-step with concrete examples. Assume the user wants to learn, not just get an answer.",
	},
	"comedian": {
		Name:        "Comedian",
		Description: "Light-hearted, humorous replies",
		Prompt:      "Keep it light and fun. Add appropriate humor and personality to your responses while still being helpful.",
	},
	"architect": {
		Name:        "Architect",
		Description: "System design and architecture focus",
		Prompt:      "Think like a software architect. Discuss trade-offs, design patterns, scalability, and maintainability. Show diagrams in your reasoning.",
	},
	"reviewer": {
		Name:        "Reviewer",
		Description: "Code review mindset — find issues",
		Prompt:      "Act as a thorough code reviewer. Find bugs, security issues, performance problems, and style violations. Be constructive but critical.",
	},
	"explorer": {
		Name:        "Explorer",
		Description: "Explore multiple creative solutions",
		Prompt:      "Explore multiple approaches before settling on one. Consider unconventional solutions. Be creative and thorough in your research.",
	},
	"pair-programmer": {
		Name:        "Pair Programmer",
		Description: "Collaborative coding, think aloud",
		Prompt:      "Pair program with the user. Think aloud about your approach. Ask questions about intent before implementing. Propose alternatives.",
	},
}

// Personalities returns a copy of the built-in personality catalog.
func Personalities() map[string]PersonalityDef {
	personalities := make(map[string]PersonalityDef, len(personalityCatalog))
	for name, definition := range personalityCatalog {
		personalities[name] = definition
	}
	return personalities
}
