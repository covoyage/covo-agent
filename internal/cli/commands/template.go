package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func NewTemplateCommand() *cobra.Command {
	templateCmd := &cobra.Command{
		Use:   "template",
		Short: "Manage templates",
		Args:  cobra.NoArgs,
	}

	templateCmd.AddCommand(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List available templates",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			tplDir := filepath.Join(homeDir, ".covo-agent", "templates")
			w := cmd.OutOrStdout()
			entries, err := os.ReadDir(tplDir)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Fprintf(w, "No templates directory found. Create one at: %s\n", tplDir)
					return nil
				}
				return fmt.Errorf("read templates: %w", err)
			}
			var names []string
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
					names = append(names, strings.TrimSuffix(e.Name(), ".md"))
				}
			}
			sort.Strings(names)
			if len(names) == 0 {
				fmt.Fprintf(w, "No templates found in %s\n", tplDir)
				return nil
			}
			fmt.Fprintln(w, "Templates:")
			for _, n := range names {
				fmt.Fprintf(w, "  %s\n", n)
			}
			return nil
		},
	})

	templateCmd.AddCommand(&cobra.Command{
		Use:   "show <name>",
		Short: "Show template content",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			path := filepath.Join(homeDir, ".covo-agent", "templates", args[0]+".md")
			data, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("template %q not found", args[0])
				}
				return fmt.Errorf("read template: %w", err)
			}
			fmt.Fprint(cmd.OutOrStdout(), string(data))
			return nil
		},
	})

	templateCmd.AddCommand(&cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a template",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			path := filepath.Join(homeDir, ".covo-agent", "templates", args[0]+".md")
			if err := os.Remove(path); err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("template %q not found", args[0])
				}
				return fmt.Errorf("remove template: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed template %q\n", args[0])
			return nil
		},
	})

	templateCmd.AddCommand(&cobra.Command{
		Use:   "edit <name>",
		Short: "Edit or create a template",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			tplDir := filepath.Join(homeDir, ".covo-agent", "templates")
			path := filepath.Join(tplDir, args[0]+".md")
			_ = os.MkdirAll(tplDir, 0755)

			editorCmd := os.Getenv("EDITOR")
			if editorCmd == "" {
				editorCmd = os.Getenv("VISUAL")
			}
			if editorCmd == "" {
				for _, c := range []string{"nvim", "vim", "vi", "nano"} {
					if _, err := exec.LookPath(c); err == nil {
						editorCmd = c
						break
					}
				}
			}
			if editorCmd == "" {
				return fmt.Errorf("no editor found. Set $EDITOR or $VISUAL")
			}

			c := exec.Command(editorCmd, path)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		},
	})

	return templateCmd
}

func templateDir(homeDir string) string {
	return filepath.Join(homeDir, ".covo-agent", "templates")
}

func readTemplate(homeDir, name string) (string, error) {
	path := filepath.Join(templateDir(homeDir), name+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func templateList(homeDir string) string {
	entries, err := os.ReadDir(templateDir(homeDir))
	if err != nil {
		return "(none)"
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

func expandTemplateArgs(template string, args []string) string {
	if len(args) == 0 {
		return template
	}
	all := strings.Join(args, " ")
	template = strings.ReplaceAll(template, "$@", all)
	template = strings.ReplaceAll(template, "$ARGUMENTS", all)
	template = strings.ReplaceAll(template, "${@}", all)
	template = strings.ReplaceAll(template, "${ARGUMENTS}", all)
	repl := func(s string) string {
		if strings.HasPrefix(s, "${@:") && strings.HasSuffix(s, "}") {
			inner := s[4 : len(s)-1]
			parts := strings.SplitN(inner, ":", 2)
			if len(parts) == 1 {
				if n, err := strconv.Atoi(parts[0]); err == nil && n >= 1 && n <= len(args) {
					return args[n-1]
				}
			} else if len(parts) == 2 {
				start, err1 := strconv.Atoi(parts[0])
				count, err2 := strconv.Atoi(parts[1])
				if err1 == nil && err2 == nil && start >= 1 && count >= 0 {
					end := start - 1 + count
					if end > len(args) {
						end = len(args)
					}
					if start-1 < end {
						return strings.Join(args[start-1:end], " ")
					}
				}
			}
		}
		return s
	}
	var result strings.Builder
	remain := template
	for {
		idx := strings.Index(remain, "${@:")
		if idx < 0 {
			result.WriteString(remain)
			break
		}
		result.WriteString(remain[:idx])
		remain = remain[idx:]
		end := strings.Index(remain, "}")
		if end < 0 {
			result.WriteString(remain)
			break
		}
		result.WriteString(repl(remain[:end+1]))
		remain = remain[end+1:]
	}
	template = result.String()
	for i := len(args); i >= 1; i-- {
		old := fmt.Sprintf("$%d", i)
		template = strings.ReplaceAll(template, old, args[i-1])
	}
	return template
}
