package setup

import "github.com/spf13/cobra"

// Run starts the interactive provider/model setup wizard.
func Run() {
	cmdSetup()
}

// NewSetupCommand builds the `setup` subcommand for interactive configuration.
func NewSetupCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Configure covo-agent",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			Run()
		},
	}
}
