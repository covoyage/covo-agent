package cli

type CommandCategory string

const (
	CatSession       CommandCategory = "Session"
	CatConfiguration CommandCategory = "Configuration"
	CatSkills        CommandCategory = "Skills"
	CatMemory        CommandCategory = "Memory"
	CatSystem        CommandCategory = "System"
)

type CommandDef struct {
	Name        string
	Description string
	Category    CommandCategory
	Aliases     []string
	ArgsHint    string
}

var CommandRegistry = []CommandDef{
	{
		Name:        "help",
		Description: "Show available commands and keybindings",
		Category:    CatSystem,
		Aliases:     []string{"?"},
	},
	{
		Name:        "clear",
		Description: "Start a fresh conversation",
		Category:    CatSession,
		Aliases:     []string{"new", "reset"},
	},
	{
		Name:        "sessions",
		Description: "List saved sessions",
		Category:    CatSession,
		Aliases:     []string{"history"},
	},
	{
		Name:        "resume",
		Description: "Resume a previous session by ID",
		Category:    CatSession,
		ArgsHint:    "<session_id>",
	},
	{
		Name:        "save",
		Description: "Save current session state",
		Category:    CatSession,
	},
	{
		Name:        "mode",
		Description: "Switch agent mode (general or code)",
		Category:    CatConfiguration,
		ArgsHint:    "<general|code>",
	},
	{
		Name:        "model",
		Description: "Open interactive provider/model picker",
		Category:    CatConfiguration,
		ArgsHint:    "[model_name]",
	},
	{
		Name:        "provider",
		Description: "Quick switch provider",
		Category:    CatConfiguration,
		ArgsHint:    "<provider>",
	},
	{
		Name:        "memory",
		Description: "View or manage persistent memory",
		Category:    CatMemory,
		ArgsHint:    "[agent|user]",
	},
	{
		Name:        "skills",
		Description: "List available skills",
		Category:    CatSkills,
	},
	{
		Name:        "skill",
		Description: "View a specific skill by name",
		Category:    CatSkills,
		ArgsHint:    "<name>",
	},
	{
		Name:        "quit",
		Description: "Exit the application",
		Category:    CatSystem,
		Aliases:     []string{"exit", "q"},
	},
}

func FindCommand(name string) *CommandDef {
	for i := range CommandRegistry {
		cmd := &CommandRegistry[i]
		if cmd.Name == name {
			return cmd
		}
		for _, alias := range cmd.Aliases {
			if alias == name {
				return cmd
			}
		}
	}
	return nil
}

func CommandsByCategory() map[CommandCategory][]CommandDef {
	result := make(map[CommandCategory][]CommandDef)
	for _, cmd := range CommandRegistry {
		result[cmd.Category] = append(result[cmd.Category], cmd)
	}
	return result
}
