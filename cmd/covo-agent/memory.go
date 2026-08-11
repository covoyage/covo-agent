package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/evolution"
	"github.com/spf13/cobra"
)

func newMemoryCommand() *cobra.Command {
	memoryCmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage memory",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cmdMemory(nil)
			return nil
		},
	}
	memoryCmd.AddCommand(&cobra.Command{Use: "providers", Short: "List available memory providers", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error { cmdMemory([]string{"providers"}); return nil }})
	memoryCmd.AddCommand(&cobra.Command{Use: "ping [name]", Short: "Ping a memory provider", Args: cobra.MaximumNArgs(1), RunE: func(_ *cobra.Command, args []string) error { cmdMemory(append([]string{"ping"}, args...)); return nil }})
	var clear bool
	migrateCmd := &cobra.Command{
		Use:   "migrate <source_provider> <dest_provider>",
		Short: "Migrate memory data between providers",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			runArgs := []string{"migrate"}
			if clear {
				runArgs = append(runArgs, "--clear")
			}
			runArgs = append(runArgs, args...)
			cmdMemory(runArgs)
			return nil
		},
	}
	migrateCmd.Flags().BoolVar(&clear, "clear", false, "clear destination before migrating")
	migrateCmd.Flags().BoolVar(&clear, "clear-destination", false, "clear destination before migrating")
	memoryCmd.AddCommand(migrateCmd)
	return memoryCmd
}

func cmdMemory(args []string) {
	homeDir, err := cli.HomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "home dir: %v\n", err)
		return
	}

	if len(args) == 0 || args[0] == "help" {
		fmt.Fprintln(os.Stderr, "Usage: covo-agent memory <command> [args]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  providers              List available memory providers")
		fmt.Fprintln(os.Stderr, "  ping [name]            Ping a memory provider to check health")
		fmt.Fprintln(os.Stderr, "  migrate [--clear] <src> <dst>  Migrate memory data between providers")
		return
	}

	switch args[0] {
	case "providers":
		cmdMemoryProviders(homeDir)
	case "ping":
		cmdMemoryPing(homeDir, args[1:])
	case "migrate":
		cmdMemoryMigrate(homeDir, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown memory subcommand: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "Available: providers, ping, migrate")
	}
}

func cmdMemoryPing(homeDir string, args []string) {
	name := os.Getenv("COVO_MEMORY_PROVIDER")
	if name == "" {
		name = "file"
	}
	if len(args) > 0 {
		name = args[0]
	}

	factory, ok := evolution.GetMemoryProvider(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "  Unknown provider: %q\n", name)
		fmt.Fprintf(os.Stderr, "  Available: %v\n", evolution.MemoryProviderNames())
		return
	}

	p, err := factory(evolution.MemoryProviderConfig{HomeDir: homeDir})
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Initialize %q: %v\n", name, err)
		return
	}
	defer p.Close()

	if err := p.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "  Init %q: %v\n", name, err)
		return
	}

	fmt.Printf("  Pinging %q... ", name)
	if err := p.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		return
	}
	fmt.Println("OK")
}

func cmdMemoryProviders(homeDir string) {
	names := evolution.MemoryProviderNames()
	sort.Strings(names)

	current := os.Getenv("COVO_MEMORY_PROVIDER")
	if current == "" {
		current = "file"
	}

	fmt.Println("  Available memory providers:")
	fmt.Println()
	for _, n := range names {
		mark := " "
		if n == current {
			mark = "›"
		}
		fmt.Printf("    %s %s\n", mark, n)
	}
	fmt.Println()
	fmt.Printf("  Current: %s (set via COVO_MEMORY_PROVIDER)\n", current)
}

func cmdMemoryMigrate(homeDir string, args []string) {
	clearDst := false
	var rest []string
	for _, a := range args {
		if a == "--clear" || a == "--clear-destination" {
			clearDst = true
		} else {
			rest = append(rest, a)
		}
	}
	args = rest

	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: covo-agent memory migrate [--clear] <source_provider> <dest_provider>")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		fmt.Fprintln(os.Stderr, "  --clear, --clear-destination  Clear destination before migrating")
		return
	}

	srcName, dstName := args[0], args[1]

	srcFactory, ok := evolution.GetMemoryProvider(srcName)
	if !ok {
		fmt.Fprintf(os.Stderr, "  Unknown source provider: %q\n", srcName)
		fmt.Fprintf(os.Stderr, "  Available: %v\n", evolution.MemoryProviderNames())
		return
	}
	dstFactory, ok := evolution.GetMemoryProvider(dstName)
	if !ok {
		fmt.Fprintf(os.Stderr, "  Unknown destination provider: %q\n", dstName)
		fmt.Fprintf(os.Stderr, "  Available: %v\n", evolution.MemoryProviderNames())
		return
	}

	memCfg := evolution.MemoryProviderConfig{HomeDir: homeDir}
	src, err := srcFactory(memCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Initialize source %q: %v\n", srcName, err)
		return
	}
	defer src.Close()
	if err := src.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "  Init source %q: %v\n", srcName, err)
		return
	}

	dst, err := dstFactory(memCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Initialize destination %q: %v\n", dstName, err)
		return
	}
	defer dst.Close()
	if err := dst.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "  Init destination %q: %v\n", dstName, err)
		return
	}

	if clearDst {
		fmt.Printf("  Clearing destination %q...\n", dstName)
		for _, store := range []evolution.MemoryStore{evolution.MemoryAgent, evolution.MemoryUser} {
			entries, err := dst.Read(store)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  Warning: read %s: %v\n", store, err)
				continue
			}
			for _, e := range entries {
				_ = dst.Remove(store, e.Content)
			}
		}
	}

	fmt.Printf("  Migrating from %q to %q...\n", srcName, dstName)

	if err := evolution.MigrateMemoryProvider(src, dst); err != nil {
		fmt.Fprintf(os.Stderr, "  Migration failed: %v\n", err)
		return
	}

	fmt.Println("  Migration complete.")
	fmt.Println()
	fmt.Printf("  To use %q, set: export COVO_MEMORY_PROVIDER=%s\n", dstName, dstName)
}
