package subagent

import "strings"

type AgentRole struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Toolsets     []string `json:"toolsets"`
	Model        string   `json:"model,omitempty"`
	SystemPrompt string   `json:"system_prompt,omitempty"`
}

var predefinedRoles = []AgentRole{
	{
		Name:         "explorer",
		Description:  "Read-only code exploration: searching, reading, navigating codebases.",
		Toolsets:     []string{"filesystem", "search", "git"},
		SystemPrompt: "You are a read-only file explorer. Map the files, symbols, and call paths relevant to the task. Do not edit files or run mutating commands. Return a concise report: relevant paths, what each does, and where the parent should start editing.",
	},
	{
		Name:        "coder",
		Description: "Full coding: file read/write, shell, edit, tests.",
		Toolsets:    []string{"filesystem", "shell", "git", "editing"},
	},
	{
		Name:         "reviewer",
		Description:  "Code review: read-only inspection, linting, testing.",
		Toolsets:     []string{"filesystem", "shell", "git", "search"},
		SystemPrompt: "You are an independent code reviewer. Inspect the described changes for correctness bugs, regressions, and security issues. Do not implement fixes. Rate each finding critical/major/minor and cite file paths.",
	},
	{
		Name:        "devops",
		Description: "Deploy, build, CI/CD operations.",
		Toolsets:    []string{"shell", "git", "deploy", "filesystem"},
	},
	{
		Name:        "architect",
		Description: "High-level design, planning, no file modification.",
		Toolsets:    []string{"filesystem", "search", "git", "planning"},
	},
}

func GetRoles() []AgentRole {
	return append([]AgentRole(nil), predefinedRoles...)
}

func GetRole(name string) (AgentRole, bool) {
	for _, r := range predefinedRoles {
		if strings.EqualFold(r.Name, name) {
			return r, true
		}
	}
	return AgentRole{}, false
}
