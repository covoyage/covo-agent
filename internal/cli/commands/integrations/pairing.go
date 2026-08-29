package integrations

import (
	"fmt"
	"github.com/covoyage/covo-agent/internal/cli"
	"strings"

	"github.com/covoyage/covo-agent/internal/gateway"
	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covo-agent/internal/plugin/builtin"
	"github.com/spf13/cobra"
)

func NewPairingCommand() *cobra.Command {
	pairingCmd := &cobra.Command{
		Use:   "pairing",
		Short: "Manage gateway pairing",
		Args:  cobra.NoArgs,
	}

	pairingCmd.AddCommand(&cobra.Command{
		Use:   "approve <code>",
		Short: "Approve a pending pairing code",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			code := strings.TrimSpace(strings.ToUpper(args[0]))
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			ps := gateway.NewPairingStore(homeDir)
			platforms := builtin.Names()
			approved := false
			for _, platform := range platforms {
				if userID, ok := ps.ApproveCode(platform, code); ok {
					fmt.Fprintln(cmd.OutOrStdout(), i18n.T("pairing.code_approved", "user", userID, "platform", platform, "code", code))
					approved = true
					break
				}
			}
			if !approved {
				fmt.Fprintln(cmd.ErrOrStderr(), i18n.T("pairing.code_invalid"))
				fmt.Fprintln(cmd.ErrOrStderr(), i18n.T("pairing.code_list_hint"))
			}
			return nil
		},
	})

	pairingCmd.AddCommand(&cobra.Command{
		Use:   "list [platform]",
		Short: "List approved users",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			ps := gateway.NewPairingStore(homeDir)
			platforms := builtin.Names()
			filter := ""
			if len(args) > 0 {
				filter = strings.TrimSpace(strings.ToLower(args[0]))
			}
			w := cmd.OutOrStdout()
			hasContent := false
			for _, platform := range platforms {
				if filter != "" && !strings.Contains(platform, filter) {
					continue
				}
				pending := ps.ListPending(platform)
				approved := ps.ListApproved(platform)
				if len(pending) == 0 && len(approved) == 0 {
					continue
				}
				hasContent = true
				fmt.Fprintf(w, "\n  %s\n", i18n.T("pairing.platform_label", "platform", platform))
				if len(pending) > 0 {
					fmt.Fprintf(w, "    %s\n", i18n.T("pairing.pending_label"))
					for _, c := range pending {
						fmt.Fprintf(w, "      %s  user=%s (%s)  created=%s\n",
							c.Code, c.UserID, c.UserName,
							c.CreatedAt.Format("2006-01-02 15:04"))
					}
				}
				if len(approved) > 0 {
					fmt.Fprintf(w, "    %s\n", i18n.T("pairing.approved_label"))
					for _, uid := range approved {
						fmt.Fprintf(w, "      %s\n", uid)
					}
				}
			}
			if !hasContent {
				fmt.Fprintf(w, "  %s\n", i18n.T("pairing.no_entries"))
			}
			return nil
		},
	})

	pairingCmd.AddCommand(&cobra.Command{
		Use:   "revoke <platform> <user_id>",
		Short: "Revoke a previously approved user",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			platform := strings.TrimSpace(strings.ToLower(args[0]))
			userID := strings.TrimSpace(args[1])
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			ps := gateway.NewPairingStore(homeDir)
			ps.RemoveApproved(platform, userID)
			fmt.Fprintln(cmd.OutOrStdout(), i18n.T("pairing.revoked", "user", userID, "platform", platform))
			return nil
		},
	})

	return pairingCmd
}
