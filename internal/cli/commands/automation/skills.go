package automation

import (
	"fmt"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/covoyage/covo-agent/internal/evolution"
	"github.com/covoyage/covo-agent/internal/skillhub"
	"github.com/spf13/cobra"
)

func NewSkillCommand() *cobra.Command {
	skillCmd := &cobra.Command{
		Use:     "skill",
		Aliases: []string{"skills"},
		Short:   "Manage skills",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cmdSkills(nil)
			return nil
		},
	}
	var listAll bool
	var listPlatform bool
	var listTier string
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List installed skills",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			args := []string{"list"}
			if listAll {
				args = append(args, "--all")
			}
			if listPlatform {
				args = append(args, "--platform")
			}
			if listTier != "" {
				args = append(args, "--tier", listTier)
			}
			cmdSkills(args)
			return nil
		},
	}
	listCmd.Flags().BoolVar(&listAll, "all", false, "show all skills")
	listCmd.Flags().BoolVar(&listPlatform, "platform", false, "show platform-compatible skills")
	listCmd.Flags().StringVar(&listTier, "tier", "", "filter skills by tier")
	skillCmd.AddCommand(listCmd)
	skillCmd.AddCommand(&cobra.Command{Use: "search <term>", Short: "Search for skills", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error { cmdSkills(shared.PrependArg("search", args)); return nil }})
	skillCmd.AddCommand(&cobra.Command{Use: "info <name>", Short: "Show skill details", Args: cobra.ExactArgs(1), ValidArgsFunction: completeSkillNames, RunE: func(_ *cobra.Command, args []string) error { cmdSkills(shared.PrependArg("info", args)); return nil }})
	skillCmd.AddCommand(&cobra.Command{Use: "disable <name>", Short: "Disable a skill", Args: cobra.ExactArgs(1), ValidArgsFunction: completeSkillNames, RunE: func(_ *cobra.Command, args []string) error { cmdSkills(shared.PrependArg("disable", args)); return nil }})
	skillCmd.AddCommand(&cobra.Command{Use: "enable <name>", Short: "Re-enable a disabled skill", Args: cobra.ExactArgs(1), ValidArgsFunction: completeSkillNames, RunE: func(_ *cobra.Command, args []string) error { cmdSkills(shared.PrependArg("enable", args)); return nil }})
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Show skill configuration variables",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cmdSkills([]string{"config"})
			return nil
		},
	}
	configCmd.AddCommand(&cobra.Command{Use: "set <key> <value>", Short: "Set a skill configuration value", Args: cobra.ExactArgs(2), RunE: func(_ *cobra.Command, args []string) error {
		cmdSkills([]string{"config", "set", args[0], args[1]})
		return nil
	}})
	skillCmd.AddCommand(configCmd)
	hubCmd := &cobra.Command{
		Use:   "hub",
		Short: "Skills hub operations",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cmdSkills([]string{"hub"})
			return nil
		},
	}
	hubCmd.AddCommand(&cobra.Command{Use: "list", Short: "List available skills from the hub", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error { cmdSkills([]string{"hub", "list"}); return nil }})
	hubCmd.AddCommand(&cobra.Command{Use: "search <term>", Short: "Search for skills on the hub", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error { cmdSkills([]string{"hub", "search", args[0]}); return nil }})
	hubCmd.AddCommand(&cobra.Command{Use: "install <name>", Short: "Install a skill from the hub", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		cmdSkills([]string{"hub", "install", args[0]})
		return nil
	}})
	skillCmd.AddCommand(hubCmd)
	return skillCmd
}

func cmdSkills(args []string) {
	homeDir, err := cli.HomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "home dir: %v\n", err)
		return
	}

	if len(args) == 0 || args[0] == "help" {
		fmt.Fprintln(os.Stderr, "Usage: covo-agent skill <command> [args]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  list [--all] [--platform] [--tier <t>]  List installed skills (grouped by category)")
		fmt.Fprintln(os.Stderr, "  search <term>         Search for skills")
		fmt.Fprintln(os.Stderr, "  info <name>           Show skill details")
		fmt.Fprintln(os.Stderr, "  disable <name>        Disable a skill")
		fmt.Fprintln(os.Stderr, "  enable <name>         Re-enable a disabled skill")
		fmt.Fprintln(os.Stderr, "  config                Show skill configuration variables")
		fmt.Fprintln(os.Stderr, "  hub <command> [args]  Skills hub operations (list, search, install)")
		return
	}

	skillsDir := filepath.Join(homeDir, "skills")

	switch args[0] {
	case "list":
		showAll := false
		tierFilter := ""
		remaining := args[1:]
		for i := 0; i < len(remaining); i++ {
			switch remaining[i] {
			case "--all":
				showAll = true
			case "--tier":
				if i+1 < len(remaining) {
					i++
					tierFilter = remaining[i]
				}
			case "--platform":
				showAll = false
			}
		}

		usage := evolution.NewSkillUsageTracker(skillsDir)
		_ = usage.Load()
		sm := evolution.NewSkillManager(skillsDir, usage)

		cfg, _ := cli.LoadConfig()
		disabledSet := make(map[string]bool)
		if cfg != nil && cfg.Skills != nil {
			for _, d := range cfg.Skills.Disabled {
				disabledSet[d] = true
			}
		}

		var all []evolution.SkillInfo
		var err error
		if showAll {
			all, err = sm.List()
		} else {
			tier := tierFilter
			all, err = sm.ListEnabledForPlatform(runtime.GOOS, disabledSet, tier)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "list skills: %v\n", err)
			return
		}

		// Group by category
		grouped := make(map[string][]evolution.SkillInfo)
		for _, s := range all {
			cat := s.Category
			if cat == "" {
				cat = "other"
			}
			grouped[cat] = append(grouped[cat], s)
		}

		total := 0
		var cats []string
		for cat := range grouped {
			cats = append(cats, cat)
		}
		sort.Strings(cats)

		catDescs := sm.DiscoverCategoryDescriptions()

		for _, cat := range cats {
			skills := grouped[cat]
			sort.Slice(skills, func(i, j int) bool {
				return skills[i].Name < skills[j].Name
			})

			desc := catDescs[cat]
			if desc != "" {
				fmt.Printf("\n  \033[1m%s\033[0m — %s\n", cat, desc)
			} else {
				fmt.Printf("\n  \033[1m%s\033[0m\n", cat)
			}
			for _, s := range skills {
				flags := ""
				if disabledSet[s.Name] {
					flags += " [disabled]"
				}
				if s.Tier != "" && s.Tier != "core" {
					flags += fmt.Sprintf(" [%s]", s.Tier)
				}
				desc := s.Description
				if len(desc) > 60 {
					desc = desc[:57] + "..."
				}
				if desc != "" {
					fmt.Printf("    %s — %s%s\n", s.Name, desc, flags)
				} else {
					fmt.Printf("    %s%s\n", s.Name, flags)
				}
				total++
			}
		}
		if total == 0 {
			fmt.Println("  No skills found.")
		} else {
			fmt.Printf("\n  Total: %d skills\n", total)
		}

	case "search":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: covo-agent skill search <term>")
			return
		}
		term := strings.ToLower(args[1])
		entries, err := os.ReadDir(skillsDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "list skills: %v\n", err)
			return
		}
		count := 0
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if strings.Contains(strings.ToLower(e.Name()), term) {
				fmt.Printf("  %s\n", e.Name())
				count++
				continue
			}
			skillFile := filepath.Join(skillsDir, e.Name(), "SKILL.md")
			data, err := os.ReadFile(skillFile)
			if err != nil {
				continue
			}
			if strings.Contains(strings.ToLower(string(data)), term) {
				fmt.Printf("  %s\n", e.Name())
				count++
			}
		}
		if count == 0 {
			fmt.Printf("  No skills matching %q.\n", args[1])
		} else {
			fmt.Printf("\n  Found: %d skills\n", count)
		}

	case "info":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: covo-agent skill info <name>")
			return
		}
		skillFile := filepath.Join(skillsDir, args[1], "SKILL.md")
		data, err := os.ReadFile(skillFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read skill: %v\n", err)
			return
		}
		fm, body, _ := evolution.ParseSkillFrontmatter(string(data))
		if fm != nil {
			fmt.Printf("Name: %s\n", fm.Name)
			fmt.Printf("Description: %s\n", fm.Description)
			if fm.Version != "" {
				fmt.Printf("Version: %s\n", fm.Version)
			}
			if fm.Author != "" {
				fmt.Printf("Author: %s\n", fm.Author)
			}
			if fm.License != "" {
				fmt.Printf("License: %s\n", fm.License)
			}
			if len(fm.Platforms) > 0 {
				fmt.Printf("Platforms: %s\n", strings.Join(fm.Platforms, ", "))
			}
			if len(fm.Tags) > 0 {
				fmt.Printf("Tags: %s\n", strings.Join(fm.Tags, ", "))
			}
			if len(fm.RelatedSkills) > 0 {
				fmt.Printf("Related skills: %s\n", strings.Join(fm.RelatedSkills, ", "))
			}
			if fm.Tier != "" {
				fmt.Printf("Tier: %s\n", fm.Tier)
			}
			if len(fm.Config) > 0 {
				fmt.Println("Config vars:")
				for _, cv := range fm.Config {
					fmt.Printf("  - %s: %s (default: %q)\n", cv.Key, cv.Description, cv.Default)
				}
			}
			fmt.Println("---")
			fmt.Println(body)
		} else {
			fmt.Println(string(data))
		}

	case "disable":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: covo-agent skill disable <name>")
			return
		}
		skillName := args[1]
		cfg, err := cli.LoadConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "load config: %v\n", err)
			return
		}
		if cfg.Skills == nil {
			cfg.Skills = &cli.SkillsConfig{}
		}
		for _, d := range cfg.Skills.Disabled {
			if d == skillName {
				fmt.Printf("  Skill %q is already disabled.\n", skillName)
				return
			}
		}
		cfg.Skills.Disabled = append(cfg.Skills.Disabled, skillName)
		if err := cli.SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "save config: %v\n", err)
			return
		}
		fmt.Printf("  Disabled skill %q.\n", skillName)

	case "enable":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: covo-agent skill enable <name>")
			return
		}
		skillName := args[1]
		cfg, err := cli.LoadConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "load config: %v\n", err)
			return
		}
		if cfg.Skills == nil {
			fmt.Printf("  Skill %q is not disabled.\n", skillName)
			return
		}
		var updated []string
		found := false
		for _, d := range cfg.Skills.Disabled {
			if d == skillName {
				found = true
				continue
			}
			updated = append(updated, d)
		}
		if !found {
			fmt.Printf("  Skill %q is not disabled.\n", skillName)
			return
		}
		cfg.Skills.Disabled = updated
		if err := cli.SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "save config: %v\n", err)
			return
		}
		fmt.Printf("  Enabled skill %q.\n", skillName)

	case "config":
		if len(args) >= 2 && args[1] == "set" {
			if len(args) < 4 {
				fmt.Fprintln(os.Stderr, "Usage: covo-agent skill config set <key> <value>")
				return
			}
			key := args[2]
			value := args[3]
			cfg, err := cli.LoadConfig()
			if err != nil {
				fmt.Fprintf(os.Stderr, "load config: %v\n", err)
				return
			}
			if cfg.Skills == nil {
				cfg.Skills = &cli.SkillsConfig{}
			}
			if cfg.Skills.Config == nil {
				cfg.Skills.Config = make(map[string]string)
			}
			cfg.Skills.Config[key] = value
			if err := cli.SaveConfig(cfg); err != nil {
				fmt.Fprintf(os.Stderr, "save config: %v\n", err)
				return
			}
			fmt.Printf("  Set skills.config.%s = %q\n", key, value)
			return
		}

		usage := evolution.NewSkillUsageTracker(skillsDir)
		_ = usage.Load()
		sm := evolution.NewSkillManager(skillsDir, usage)

		vars := sm.DiscoverSkillConfigVars()
		if len(vars) == 0 {
			fmt.Println("  No skill configuration variables declared.")
			return
		}
		cfg, _ := cli.LoadConfig()
		var cfgValues map[string]string
		if cfg != nil && cfg.Skills != nil {
			cfgValues = cfg.Skills.Config
		}
		resolved := sm.ResolveSkillConfigValues(cfgValues)

		fmt.Println("  Skill configuration variables:")
		for _, v := range vars {
			current := resolved[v.Key]
			if current == "" {
				current = "(not set)"
			}
			fmt.Printf("    %s = %s\n", v.Key, current)
			fmt.Printf("      Description: %s\n", v.Description)
			if v.Default != "" {
				fmt.Printf("      Default: %q\n", v.Default)
			}
			if v.Prompt != "" {
				fmt.Printf("      Prompt: %s\n", v.Prompt)
			}
			fmt.Println()
		}

	case "hub":
		cmdSkillsHub(homeDir, args[1:])

	default:
		fmt.Fprintf(os.Stderr, "Unknown skills subcommand: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "Available: list, search, info, disable, enable, config, hub")
	}
}

func cmdSkillsHub(homeDir string, args []string) {
	if len(args) == 0 || args[0] == "help" {
		fmt.Fprintln(os.Stderr, "Usage: covo-agent skill hub <command> [args]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  list              List available skills from the hub")
		fmt.Fprintln(os.Stderr, "  search <term>     Search for skills on the hub")
		fmt.Fprintln(os.Stderr, "  install <name>    Install a skill from the hub")
		return
	}

	hub := skillhub.New(homeDir)

	switch args[0] {
	case "list":
		skills, err := hub.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "hub list: %v\n", err)
			return
		}
		if len(skills) == 0 {
			fmt.Println("  No skills available in the hub.")
			return
		}
		for _, s := range skills {
			fmt.Printf("  %s", s.Name)
			if s.Description != "" {
				fmt.Printf(" — %s", s.Description)
			}
			fmt.Println()
		}
		fmt.Printf("\n  Total: %d skills\n", len(skills))

	case "search":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: covo-agent skill hub search <term>")
			return
		}
		results, err := hub.Search(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "hub search: %v\n", err)
			return
		}
		if len(results) == 0 {
			fmt.Printf("  No skills matching %q on the hub.\n", args[1])
			return
		}
		for _, s := range results {
			fmt.Printf("  %s", s.Name)
			if s.Description != "" {
				fmt.Printf(" — %s", s.Description)
			}
			fmt.Println()
		}
		fmt.Printf("\n  Found: %d skills\n", len(results))

	case "install":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: covo-agent skill hub install <name>")
			return
		}
		path, err := hub.Install(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "hub install: %v\n", err)
			return
		}
		fmt.Printf("  Installed skill %q → %s\n", args[1], path)

	default:
		fmt.Fprintf(os.Stderr, "Unknown hub subcommand: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "Available: list, search, install")
	}
}

func completeSkillNames(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	homeDir, err := cli.HomeDir()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	entries, err := os.ReadDir(filepath.Join(homeDir, "skills"))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
