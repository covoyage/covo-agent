package integrations

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	acpadapt "github.com/covoyage/covo-agent/internal/acp"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/cli/commands/setup"
	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/workspace"
	"github.com/spf13/cobra"
)

func NewACPCommand() *cobra.Command {
	var check bool
	var doSetup bool
	var version bool
	var wsMode bool
	var wsAddr string
	var remote string
	var remoteRuntime bool
	acpCmd := &cobra.Command{
		Use:   "acp",
		Short: "Run the ACP server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if version {
				fmt.Fprintf(cmd.OutOrStdout(), "covo-agent v%s (ACP)\n", cli.Version)
				return nil
			}
			if check {
				if err := cli.LoadDotEnv(); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: load .env: %v\n", err)
				}
				if _, err := cli.EnsureHomeDir(); err != nil {
					return fmt.Errorf("ACP check failed: home dir: %w", err)
				}
				if !cli.HasProviderConfigured() {
					return fmt.Errorf("ACP check failed: no provider configured. Run: covo-agent setup")
				}
				fmt.Fprintln(cmd.OutOrStdout(), "covo-agent ACP check OK")
				return nil
			}
			if doSetup {
				setup.Run()
				return nil
			}

			homeDir, err := cli.EnsureHomeDir()
			if err != nil {
				return fmt.Errorf("ensure home dir: %w", err)
			}

			if err := cli.LoadDotEnv(); err != nil {
				log.Printf("load .env: %v", err)
			}
			retainDays := 0
			if loaded, err := cli.LoadConfig(); err == nil && loaded != nil && loaded.Logging != nil {
				retainDays = loaded.Logging.RetainDays
			}
			logFile, err := logutil.OpenAppLog(homeDir, logutil.ResolveRetainDays(retainDays))
			var logWriter io.Writer = logFile
			if err != nil {
				log.Printf("open log file: %v", err)
				logWriter = os.Stderr
			} else {
				defer logFile.Close()
			}
			logger := slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{
				Level: logutil.ResolveLevel(slog.LevelWarn),
			}))

			if !cli.HasProviderConfigured() {
				return fmt.Errorf("no provider configured. Run: covo-agent --setup")
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			if remoteRuntime {
				spec, perr := workspace.ParseRemote(remote)
				if perr != nil || spec == nil || !spec.Enabled() {
					return fmt.Errorf("--remote-runtime requires --remote")
				}
				session, err := spec.DeployRuntime(ctx, "acp")
				if err != nil {
					return err
				}
				return session.Wait()
			}

			// WebSocket relay mode: start a WebSocket-to-stdio bridge
			if wsMode {
				relay, err := acpadapt.NewWSRelay(acpadapt.WSRelayConfig{
					Addr:      wsAddr,
					AuthToken: os.Getenv("COVO_ACP_WS_TOKEN"),
					Logger:    logger,
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
	acpCmd.Flags().BoolVar(&doSetup, "setup", false, "run interactive provider/model setup")
	acpCmd.Flags().BoolVar(&version, "version", false, "show version and exit")
	acpCmd.Flags().BoolVar(&wsMode, "ws", false, "start as WebSocket relay server (for remote IDE integration)")
	acpCmd.Flags().StringVar(&wsAddr, "addr", "127.0.0.1:17891", "WebSocket relay address; non-loopback requires COVO_ACP_WS_TOKEN (use with --ws)")
	acpCmd.Flags().StringVar(&remote, "remote", "", "remote workspace (user@host:/path or ssh://user@host/path)")
	acpCmd.Flags().BoolVar(&remoteRuntime, "remote-runtime", false, "upload this binary and run acp on --remote")
	return acpCmd
}
