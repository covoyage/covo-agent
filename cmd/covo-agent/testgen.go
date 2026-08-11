package main

import (
	"fmt"

	"github.com/covoyage/covo-agent/internal/tools"
	"github.com/spf13/cobra"
)

func newTestgenCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "testgen <file>",
		Short: "Generate tests",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := tools.GenerateTestsForFile(args[0])
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			switch result.Status {
			case "exists":
				fmt.Fprintf(w, "Test file already exists: %s\n", result.TestFile)
			case "generated":
				fmt.Fprintf(w, "Tests generated: %s\n", result.TestFile)
			case "skeleton":
				fmt.Fprintf(w, "Test skeleton created: %s\n", result.TestFile)
				fmt.Fprintln(w, "Edit the file to add actual test cases.")
			case "unsupported":
				return fmt.Errorf("unsupported language: %s", result.Language)
			}
			return nil
		},
	}
}
