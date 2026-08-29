package session

import (
	"context"
	"fmt"
	"github.com/covoyage/covo-agent/internal/cli"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/evolution"
	"github.com/spf13/cobra"
)

func NewDreamingCommand() *cobra.Command {
	dreamingCmd := &cobra.Command{
		Use:   "dreaming",
		Short: "Manage memory dreaming",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cmdDreaming(nil)
			return nil
		},
	}
	dreamingCmd.AddCommand(&cobra.Command{Use: "run", Short: "Run a full memory consolidation sweep", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error { cmdDreaming([]string{"run"}); return nil }})
	dreamingCmd.AddCommand(&cobra.Command{Use: "diary", Short: "Show the dream diary", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error { cmdDreaming([]string{"diary"}); return nil }})
	dreamingCmd.AddCommand(&cobra.Command{Use: "status", Short: "Show dreaming engine status", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error { cmdDreaming([]string{"status"}); return nil }})
	dreamingCmd.AddCommand(&cobra.Command{Use: "enable", Short: "Enable scheduled dreaming", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error { cmdDreaming([]string{"enable"}); return nil }})
	dreamingCmd.AddCommand(&cobra.Command{Use: "disable", Short: "Disable scheduled dreaming", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error { cmdDreaming([]string{"disable"}); return nil }})
	dreamingCmd.AddCommand(&cobra.Command{Use: "config [key=value...]", Short: "Show or set dreaming configuration", Args: cobra.ArbitraryArgs, RunE: func(_ *cobra.Command, args []string) error {
		cmdDreaming(append([]string{"config"}, args...))
		return nil
	}})
	return dreamingCmd
}

func cmdDreaming(args []string) {
	homeDir, err := cli.HomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: home dir: %v\n", err)
		return
	}

	if len(args) == 0 || args[0] == "help" {
		fmt.Fprintln(os.Stderr, "Usage: covo-agent dreaming <command> [args]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  run        Run a full memory consolidation sweep (Light->REM->Deep)")
		fmt.Fprintln(os.Stderr, "  diary      Show the dream diary")
		fmt.Fprintln(os.Stderr, "  status     Show dreaming engine status")
		fmt.Fprintln(os.Stderr, "  enable     Enable scheduled dreaming")
		fmt.Fprintln(os.Stderr, "  disable    Disable scheduled dreaming")
		fmt.Fprintln(os.Stderr, "  config     Show or set dreaming configuration")
		return
	}

	switch args[0] {
	case "run":
		cmdDreamingRun(homeDir)
	case "diary":
		cmdDreamingDiary(homeDir)
	case "status":
		cmdDreamingStatus(homeDir)
	case "enable":
		cmdDreamingEnable(homeDir, true)
	case "disable":
		cmdDreamingEnable(homeDir, false)
	case "config":
		cmdDreamingConfig(homeDir, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown dreaming subcommand: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "Available: run, diary, status, enable, disable, config")
	}
}

func cmdDreamingRun(homeDir string) {
	ms, closeFn := openMem(homeDir)
	if ms == nil {
		return
	}
	defer closeFn()

	engine := evolution.NewDreamingEngine(ms, evolution.DefaultDreamingConfig(), homeDir, nil)

	fmt.Println("Running memory consolidation sweep...")
	fmt.Println("  Light phase: scanning entries...")
	fmt.Println("  REM phase: analyzing patterns...")
	fmt.Println("  Deep phase: promoting candidates...")

	entry, err := engine.Run(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}

	fmt.Println()
	fmt.Printf("  Complete: %s\n", entry.Timestamp.Format(time.RFC3339))
	fmt.Printf("  Entries:  %d in -> %d out\n", entry.EntriesIn, entry.EntriesOut)
	if entry.StaleFound > 0 {
		fmt.Printf("  Stale:    %d removed\n", entry.StaleFound)
	}
	if entry.Conflicts > 0 {
		fmt.Printf("  Conflicts: %d flagged for review\n", entry.Conflicts)
	}
	fmt.Printf("  Summary:  %s\n", entry.Summary)
}

func cmdDreamingDiary(homeDir string) {
	diaryPath := filepath.Join(homeDir, "dreaming", "DREAMS.md")
	data, err := os.ReadFile(diaryPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No dream diary yet. Run 'covo-agent dreaming run' first.")
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}

	fmt.Println(string(data))
}

func cmdDreamingStatus(homeDir string) {
	ms, closeFn := openMem(homeDir)
	if ms == nil {
		return
	}
	defer closeFn()

	engine := evolution.NewDreamingEngine(ms, evolution.DefaultDreamingConfig(), homeDir, nil)

	cfg := engine.Config()
	lastRun := engine.LastRun()
	runCount := engine.RunCount()

	fmt.Println("Dreaming Engine Status:")
	fmt.Printf("  Enabled:  %v\n", cfg.Enabled)
	fmt.Printf("  Interval: %v\n", cfg.Interval)
	fmt.Printf("  Runs:     %d\n", runCount)
	if !lastRun.IsZero() {
		fmt.Printf("  Last run: %s\n", lastRun.Format(time.RFC3339))
	}
	fmt.Printf("  Min score: %.2f\n", cfg.MinScore)
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  covo-agent dreaming run       Run consolidation now")
	fmt.Println("  covo-agent dreaming diary      View dream diary")
	fmt.Println("  covo-agent dreaming enable     Enable scheduled dreaming")
	fmt.Println("  covo-agent dreaming disable    Disable scheduled dreaming")
}

func cmdDreamingEnable(homeDir string, enabled bool) {
	ms, closeFn := openMem(homeDir)
	if ms == nil {
		return
	}
	defer closeFn()

	engine := evolution.NewDreamingEngine(ms, evolution.DefaultDreamingConfig(), homeDir, nil)
	cfg := engine.Config()
	cfg.Enabled = enabled
	engine.SetConfig(cfg)

	if enabled {
		fmt.Println("Scheduled dreaming enabled. Runs every", cfg.Interval)
	} else {
		fmt.Println("Scheduled dreaming disabled.")
	}
}

func cmdDreamingConfig(homeDir string, args []string) {
	ms, closeFn := openMem(homeDir)
	if ms == nil {
		return
	}
	defer closeFn()

	engine := evolution.NewDreamingEngine(ms, evolution.DefaultDreamingConfig(), homeDir, nil)

	if len(args) == 0 {
		cfg := engine.Config()
		fmt.Println("Dreaming Configuration:")
		fmt.Printf("  enabled:              %v\n", cfg.Enabled)
		fmt.Printf("  interval:             %v\n", cfg.Interval)
		fmt.Printf("  min_score:            %.2f\n", cfg.MinScore)
		fmt.Printf("  max_entries_light:    %d\n", cfg.MaxEntriesLight)
		fmt.Printf("  max_promote_daily:    %d\n", cfg.MaxPromoteDaily)
		fmt.Println()
		fmt.Println("To change: covo-agent dreaming config <key>=<value>")
		fmt.Println("  e.g., covo-agent dreaming config interval=12h min_score=0.7")
		return
	}

	cfg := engine.Config()
	for _, arg := range args {
		kv := strings.SplitN(arg, "=", 2)
		if len(kv) != 2 {
			fmt.Fprintf(os.Stderr, "  Invalid format: %s (expected key=value)\n", arg)
			continue
		}
		key, value := kv[0], kv[1]
		switch key {
		case "interval":
			d, err := time.ParseDuration(value)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  Invalid interval: %s\n", value)
				continue
			}
			cfg.Interval = d
		case "min_score":
			var s float64
			if _, err := fmt.Sscanf(value, "%f", &s); err != nil {
				fmt.Fprintf(os.Stderr, "  Invalid score: %s\n", value)
				continue
			}
			cfg.MinScore = s
		default:
			fmt.Fprintf(os.Stderr, "  Unknown key: %s\n", key)
		}
	}
	engine.SetConfig(cfg)
	fmt.Println("Configuration updated.")
}

func openMem(homeDir string) (*evolution.MemorySystem, func()) {
	providerName := os.Getenv("COVO_MEMORY_PROVIDER")
	if providerName == "" {
		providerName = "file"
	}

	factory, ok := evolution.GetMemoryProvider(providerName)
	if !ok {
		fmt.Fprintf(os.Stderr, "Unknown memory provider: %q\n", providerName)
		return nil, func() {}
	}

	p, err := factory(evolution.MemoryProviderConfig{HomeDir: homeDir})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Init %q: %v\n", providerName, err)
		return nil, func() {}
	}

	if err := p.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "Init %q: %v\n", providerName, err)
		return nil, func() {}
	}

	ms := evolution.NewMemorySystemWithProvider(p)
	return ms, func() { p.Close() }
}
