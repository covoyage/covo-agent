package cli

// CommandRuntime carries the initialized CLI environment shared by all
// subcommands. It is populated once by the root command's PersistentPreRunE.
type CommandRuntime struct {
	Cfg     *Config
	HomeDir string
}
