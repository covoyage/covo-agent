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

	acpadapt "github.com/covoyage/covo-agent/internal/acp"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/spf13/cobra"
)

func newACPCommand() *cobra.Command {
	var check bool
	var setup bool
	var version bool
	var wsMode bool
	var wsAddr string
	acpCmd := &cobra.Command{
		Use:   "acp",
		Short: "Run the ACP server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if version {
				fmt.Fprintf(cmd.OutOrStdout(), "covo-agent v%s (ACP)\n", Version)
				return nil
			}
			if check {
				fmt.Fprintln(cmd.OutOrStdout(), "covo-agent ACP check OK")
				return nil
			}
			if setup {
				fmt.Fprintln(os.Stderr, "ACP setup: run 'covo-agent config' to configure provider/model")
				return nil
			}

			homeDir, err := cli.EnsureHomeDir()
			if err != nil {
				return fmt.Errorf("ensure home dir: %w", err)
			}

			logFile, err := os.OpenFile(filepath.Join(homeDir, "covo-agent.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err != nil {
				log.Printf("open log file: %v", err)
				logFile = os.Stderr
			}
			logger := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
				Level: slog.LevelWarn,
			}))

			if err := cli.LoadDotEnv(); err != nil {
				logger.Warn("load .env", "err", err)
			}

			if !cli.HasProviderConfigured() {
				return fmt.Errorf("no provider configured. Run: covo-agent --setup")
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			// WebSocket relay mode: start a WebSocket-to-stdio bridge
			if wsMode {
				relay, err := acpadapt.NewWSRelay(acpadapt.WSRelayConfig{
					Addr:   wsAddr,
					Logger: logger,
				})
				if err != nil {
					return fmt.Errorf("create WS relay: %w", err)
				}
				fmt.Fprintf(os.Stderr, "ACP WebSocket relay listening on %s\n", wsAddr)
				fmt.Fprintf(os.Stderr, "  Connect: ws://%s/ws\n", wsAddr)
				fmt.Fprintf(os.Stderr, "  Health:  http://%s/health\n", wsAddr)
				return relay.Start(ctx)
			}

			server, err := acpadapt.NewServer(context.Background(), logger)
			if err != nil {
				return fmt.Errorf("create ACP server: %w", err)
			}

			return server.Run(ctx)
		},
	}
	acpCmd.Flags().BoolVar(&check, "check", false, "verify ACP dependencies and exit")
	acpCmd.Flags().BoolVar(&setup, "setup", false, "run interactive provider/model setup")
	acpCmd.Flags().BoolVar(&version, "version", false, "show version and exit")
	acpCmd.Flags().BoolVar(&wsMode, "ws", false, "start as WebSocket relay server (for remote IDE integration)")
	acpCmd.Flags().StringVar(&wsAddr, "addr", ":17891", "WebSocket relay listen address (use with --ws)")
	return acpCmd
}
