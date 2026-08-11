package main

import "github.com/spf13/cobra"

func prependArg(head string, args []string) []string {
	out := make([]string, 0, len(args)+1)
	out = append(out, head)
	out = append(out, args...)
	return out
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			cmdVersion()
		},
	}
}

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show configuration status",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			cmdStatus()
		},
	}
}

func newUpdateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update covo-agent",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			cmdUpdate()
		},
	}
}

func newSetupCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Configure covo-agent",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			cmdSetup()
		},
	}
}

func newLSPCommand() *cobra.Command {
	lspCmd := &cobra.Command{
		Use:   "lsp",
		Short: "Manage language servers",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			cmdLSP(nil)
		},
	}

	var jsonOutput bool
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show language server status",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			args := []string{}
			if jsonOutput {
				args = append(args, "--json")
			}
			cmdLSPStatus(args)
		},
	}
	statusCmd.Flags().BoolVar(&jsonOutput, "json", false, "output in JSON format")
	lspCmd.AddCommand(statusCmd)

	lspCmd.AddCommand(&cobra.Command{
		Use:   "install [server...]",
		Short: "Install missing language servers",
		Args:  cobra.ArbitraryArgs,
		Run: func(_ *cobra.Command, args []string) {
			cmdLSPInstall(args)
		},
	})

	return lspCmd
}
