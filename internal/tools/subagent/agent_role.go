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
		Name:        "explorer",
		Description: "Read-only code exploration: searching, reading, navigating codebases.",
		Toolsets:    []string{"filesystem", "search", "git"},
	},
	{
		Name:        "coder",
		Description: "Full coding: file read/write, shell, edit, tests.",
		Toolsets:    []string{"filesystem", "shell", "git", "editing"},
	},
	{
		Name:        "reviewer",
		Description: "Code review: read-only inspection, linting, testing.",
		Toolsets:    []string{"filesystem", "shell", "git", "search"},
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
