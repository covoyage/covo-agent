package prefs

import (
	"fmt"
	"github.com/covoyage/covo-agent/internal/cli"

	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/spf13/cobra"
)

func NewLanguageCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "language [code]",
		Aliases: []string{"lang"},
		Short:   "Set the display language",
		Args:    cobra.MaximumNArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			var codes []string
			for _, l := range i18n.SupportedLanguages() {
				codes = append(codes, string(l))
			}
			return codes, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			if len(args) == 0 || args[0] == "list" {
				fmt.Fprintln(w, i18n.T("cli.language_available"))
				for _, l := range i18n.SupportedLanguages() {
					fmt.Fprintf(w, "  %-8s %s\n", string(l), i18n.DisplayName(l))
				}
				current := i18n.DetectSystemLanguage()
				if current != "" {
					fmt.Fprintf(w, "\n%s\n", i18n.T("system.language_current", "lang", string(current), "name", i18n.DisplayName(current)))
				}
				return nil
			}

			code := args[0]
			if !i18n.IsSupported(code) {
				return fmt.Errorf("%s", i18n.T("system.language_unknown", "code", code))
			}

			lang, _ := i18n.ParseLanguage(code)
			i18n.SetLanguage(lang)

			cfg, err := cli.LoadConfig()
			if err == nil {
				if cfg.Display == nil {
					cfg.Display = &cli.DisplayConfig{}
				}
				cfg.Display.Language = string(lang)
				cli.SaveConfig(cfg)
			}

			fmt.Fprintln(w, i18n.T("system.language_switched", "name", i18n.DisplayName(lang)))
			return nil
		},
	}
}
