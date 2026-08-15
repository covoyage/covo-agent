package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/logutil"
	mcpserver "github.com/covoyage/covo-agent/internal/mcpserver"
	"github.com/covoyage/covo-agent/internal/plugin"
	"github.com/covoyage/covo-agent/internal/plugin/builtin"
	covonautMCP "github.com/covoyage/covonaut/mcp"
	"github.com/spf13/cobra"
)

func newMCPCommand() *cobra.Command {
	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP servers",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cmdMCP(nil)
			return nil
		},
	}
	testCmd := &cobra.Command{
		Use:   "test <command> [args...]",
		Short: "Connect to an MCP server and list its tools",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cmdMCP(prependArg("test", args))
			return nil
		},
	}
	testCmd.Flags().SetInterspersed(false)
	mcpCmd.AddCommand(testCmd)
	addCmd := &cobra.Command{
		Use:   "add <name> <command> [args...]",
		Short: "Add MCP server config",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			cmdMCP(prependArg("add", args))
			return nil
		},
	}
	addCmd.Flags().SetInterspersed(false)
	mcpCmd.AddCommand(addCmd)
	mcpCmd.AddCommand(&cobra.Command{Use: "list", Short: "List configured MCP servers", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error { cmdMCP([]string{"list"}); return nil }})
	mcpCmd.AddCommand(&cobra.Command{Use: "remove <name>", Short: "Remove MCP server config", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error { cmdMCP(prependArg("remove", args)); return nil }})
	mcpCmd.AddCommand(&cobra.Command{Use: "serve", Short: "Run as an MCP server", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error { cmdMCP([]string{"serve"}); return nil }})
	return mcpCmd
}

func cmdMCP(args []string) {
	homeDir, err := cli.HomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "home dir: %v\n", err)
		return
	}
	if len(args) == 0 || args[0] == "help" {
		fmt.Fprintln(os.Stderr, "Usage: covo-agent mcp <command> [args]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  test <command> [args...]    Connect to an MCP server and list its tools")
		fmt.Fprintln(os.Stderr, "  add <name> <command>         Add MCP server config (writes to config.yaml)")
		fmt.Fprintln(os.Stderr, "  list                        List configured MCP servers")
		fmt.Fprintln(os.Stderr, "  remove <name>               Remove MCP server config")
		fmt.Fprintln(os.Stderr, "  serve                       Run as an MCP server (expose tools to MCP clients)")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "Config: %s\n", filepath.Join(homeDir, "config.yaml"))
		return
	}

	switch args[0] {
	case "test":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: covo-agent mcp test <command> [args...]")
			return
		}
		testMCPConnection(homeDir, args[1], args[2:])

	case "add":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: covo-agent mcp add <name> <command> [args...]")
			return
		}
		addMCPServer(homeDir, args[1], args[2], args[3:])

	case "list":
		listMCPServers(homeDir)

	case "remove":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: covo-agent mcp remove <name>")
			return
		}
		removeMCPServer(homeDir, args[1])

	case "serve":
		cmdMCPServe()

	default:
		fmt.Fprintf(os.Stderr, "Unknown mcp subcommand: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "Available: test, add, list, remove, serve")
	}
}

func testMCPConnection(homeDir, command string, args []string) {
	_ = homeDir
	fmt.Printf("  Testing MCP server: %s %v\n", command, args)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client, err := covonautMCP.NewStdioClient(ctx, covonautMCP.StdioConfig{
		Command:        command,
		Args:           args,
		RequestTimeout: 10 * time.Second,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ Connection failed: %v\n", err)
		return
	}
	defer client.Close()

	fmt.Println("  ✓ Connected")
	fmt.Println()

	tools, err := client.ListTools(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ List tools failed: %v\n", err)
		return
	}

	if len(tools) == 0 {
		fmt.Println("  No tools exposed by this server.")
		return
	}
	fmt.Printf("  Tools (%d):\n", len(tools))
	for _, t := range tools {
		fmt.Printf("    - %s", t.Name)
		if t.Description != "" {
			desc := t.Description
			if len(desc) > 80 {
				desc = desc[:80] + "..."
			}
			fmt.Printf(": %s", desc)
		}
		fmt.Println()
	}
}

func addMCPServer(homeDir, name, command string, args []string) {
	cfg, err := cli.LoadConfigFrom(homeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		return
	}

	if cfg.MCPServers == nil {
		cfg.MCPServers = make(map[string]cli.MCPServerConfig)
	}
	cfg.MCPServers[name] = cli.MCPServerConfig{
		Command: command,
		Args:    args,
	}

	if err := cli.SaveConfigToDir(cfg, homeDir); err != nil {
		fmt.Fprintf(os.Stderr, "save config: %v\n", err)
		return
	}
	fmt.Printf("  ✓ MCP server %q added\n", name)
}

func listMCPServers(homeDir string) {
	cfg, err := cli.LoadConfigFrom(homeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		return
	}

	if len(cfg.MCPServers) == 0 {
		fmt.Println("  No MCP servers configured.")
		return
	}

	fmt.Printf("  MCP Servers (%d):\n", len(cfg.MCPServers))
	for name, srv := range cfg.MCPServers {
		fmt.Printf("    %s: %s %v\n", name, srv.Command, srv.Args)
	}
}

func removeMCPServer(homeDir, name string) {
	cfg, err := cli.LoadConfigFrom(homeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		return
	}

	if _, ok := cfg.MCPServers[name]; !ok {
		fmt.Fprintf(os.Stderr, "  ✗ MCP server %q not found\n", name)
		return
	}
	delete(cfg.MCPServers, name)

	if err := cli.SaveConfigToDir(cfg, homeDir); err != nil {
		fmt.Fprintf(os.Stderr, "save config: %v\n", err)
		return
	}
	fmt.Printf("  ✓ MCP server %q removed\n", name)
}

func cmdMCPServe() {
	homeDir, err := cli.EnsureHomeDir()
	if err != nil {
		log.Fatalf("ensure home dir: %v", err)
	}

	logFile, err := os.OpenFile(filepath.Join(homeDir, "covo-agent.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("open log file: %v", err)
		logFile = os.Stderr
	}
	logger := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
		Level: logutil.ResolveLevel(slog.LevelWarn),
	}))

	if err := cli.LoadDotEnv(); err != nil {
		logger.Warn("load .env", "err", err)
	}

	cfg, err := cli.LoadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if !cli.HasProviderConfigured() {
		logger.Warn("no provider configured — MCP tools will be limited to built-in tools")
	}

	providerType := cli.ResolveProvider(cfg)
	model := cli.ResolveModel(cfg)

	// Collect plugin lifecycle hooks
	pluginSystem, err := plugin.NewSystem(context.Background(), plugin.SystemConfig{
		HomeDir: homeDir,
		Logger:  logger,
	})
	if err != nil {
		logger.Warn("init plugin system", "err", err)
	}
	if pluginSystem != nil {
		pluginSystem.RegisterBuiltin(builtin.Providers())
		defer pluginSystem.Shutdown()
	}

	pluginHooks := agent.ConvertPluginHooks(pluginSystem.LifecycleHooks())

	agentCfg := agent.CovoAgentConfig{
		Mode:                     agent.ModeGeneral,
		Provider:                 nil,
		ProviderName:             providerType,
		Model:                    model,
		WorkingDir:               homeDir,
		HomeDir:                  homeDir,
		Logger:                   logger,
		MCPServers:               mcpAgentConfig(cfg),
		LifecycleHooks:           pluginHooks,
		Auxiliary:                auxiliaryConfigFromCLI(cfg),
		AuxiliaryProviderBuilder: cli.ResolveAuxiliaryProviderBuilder(),
	}

	if cli.HasProviderConfigured() {
		llm, err := cli.BuildProvider(providerType)
		if err != nil {
			logger.Warn("build provider", "err", err)
		} else {
			agentCfg.Provider = llm
		}
	}

	covoAgent, err := agent.NewCovoAgent(agentCfg)
	if err != nil {
		log.Fatalf("create agent: %v", err)
	}
	defer covoAgent.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	sessionsDir := filepath.Join(homeDir, "sessions")
	sessionToolProvider, err := mcpserver.NewSessionToolProvider(sessionsDir)
	if err != nil {
		logger.Warn("init session tools", "err", err)
	}

	server := mcpserver.NewServerFromAgent(covoAgent.Core())
	if sessionToolProvider != nil {
		sessionToolProvider.RegisterTools(server)
		logger.Info("registered session management tools")
	}
	logger.Info("MCP server started (stdio transport)")
	if err := server.Run(ctx); err != nil {
		logger.Error("MCP server error", "err", err)
		os.Exit(1)
	}
}
