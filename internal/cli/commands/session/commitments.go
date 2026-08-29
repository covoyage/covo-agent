package session

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	toolscommitments "github.com/covoyage/covo-agent/internal/tools/commitments"
	"github.com/spf13/cobra"
)

func NewCommitmentsCommand() *cobra.Command {
	commitmentsCmd := &cobra.Command{
		Use:   "commitments",
		Short: "Manage commitments",
		Args:  cobra.NoArgs,
	}

	commitmentsCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List commitments",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			store := toolscommitments.NewCommitmentStore(filepath.Join(homeDir, ".covo-agent"))
			w := cmd.OutOrStdout()
			commitments := store.List()
			if len(commitments) == 0 {
				fmt.Fprintln(w, "No commitments found.")
				return nil
			}
			for _, c := range commitments {
				mark := " "
				if c.Status == toolscommitments.CommitmentDismissed {
					mark = "x"
				} else if c.Status == toolscommitments.CommitmentCompleted {
					mark = "v"
				}
				fmt.Fprintf(w, "[%s] %s (%s)\n", mark, c.Description, c.CreatedAt.Format(time.RFC3339))
				fmt.Fprintf(w, "      id=%s status=%s source=%s\n", c.ID, c.Status, c.Source)
			}
			return nil
		},
	})

	commitmentsCmd.AddCommand(&cobra.Command{
		Use:   "dismiss <id>",
		Short: "Dismiss a commitment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			store := toolscommitments.NewCommitmentStore(filepath.Join(homeDir, ".covo-agent"))
			if err := store.Dismiss(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Commitment %q dismissed.\n", args[0])
			return nil
		},
	})

	commitmentsCmd.AddCommand(&cobra.Command{
		Use:   "complete <id>",
		Short: "Complete a commitment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			store := toolscommitments.NewCommitmentStore(filepath.Join(homeDir, ".covo-agent"))
			if err := store.Complete(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Commitment %q completed.\n", args[0])
			return nil
		},
	})

	return commitmentsCmd
}
