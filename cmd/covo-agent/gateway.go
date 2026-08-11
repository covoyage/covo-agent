package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/gateway"
	"github.com/covoyage/covo-agent/internal/plugin"
	"github.com/covoyage/covo-agent/internal/plugin/builtin"
	"github.com/spf13/cobra"
)

func newGatewayCommand(runtime *commandRuntime) *cobra.Command {
	gatewayCmd := &cobra.Command{
		Use:   "gateway",
		Short: "Manage messaging gateways",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	gatewayCmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show gateway and platform status",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			gatewayStatus(runtime.homeDir, gatewayPIDFile(runtime.homeDir))
		},
	})
	gatewayCmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start the messaging gateway in the foreground",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			startGateway(gatewayPIDFile(runtime.homeDir))
		},
	})
	gatewayCmd.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Stop the messaging gateway",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			stopGateway(gatewayPIDFile(runtime.homeDir))
		},
	})
	gatewayCmd.AddCommand(&cobra.Command{
		Use:   "setup",
		Short: "Configure messaging platforms interactively",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			gatewaySetup(runtime.homeDir)
		},
	})

	return gatewayCmd
}

func gatewayPIDFile(homeDir string) string {
	return filepath.Join(homeDir, "gateway.pid")
}

func gatewayStatus(homeDir, pidFile string) {
	if err := cli.LoadDotEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "load .env: %v\n", err)
	}

	running := false
	var pid int
	if data, err := os.ReadFile(pidFile); err == nil {
		pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		if pid > 0 {
			proc, err := os.FindProcess(pid)
			if err == nil {
				if err := proc.Signal(syscall.Signal(0)); err == nil {
					running = true
				}
			}
		}
	}

	if running {
		fmt.Printf("  Gateway: running (PID: %d)\n", pid)
	} else {
		fmt.Println("  Gateway: stopped")
		_ = os.Remove(pidFile)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	pluginSystem, err := plugin.NewSystem(ctx, plugin.SystemConfig{
		HomeDir: homeDir,
		Logger:  logger,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "  plugin system: %v\n", err)
		return
	}
	defer pluginSystem.Shutdown()

	pluginSystem.RegisterBuiltin(builtin.Providers())

	allPlatforms := builtin.Names()
	fmt.Println("  Platforms:")
	for _, name := range allPlatforms {
		entry := pluginSystem.Registry.Get(name)
		status := "  -"
		detail := ""
		if entry != nil && entry.Enabled {
			status = "  ✓"
			if entry.Provider != nil {
				if pp, ok := entry.Provider.(plugin.PlatformProvider); ok {
					if err := pp.Validate(); err != nil {
						status = "  ✗"
						detail = " (" + err.Error() + ")"
					}
				}
			}
		}
		fmt.Printf("    %s %-12s%s\n", status, name, detail)
	}

	fmt.Println()
	fmt.Println("  Use 'covo-agent plugin enable <name>' to enable a platform")
	fmt.Println("  Use 'covo-agent gateway start' to start the gateway")
}

func stopGateway(pidFile string) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		fmt.Println("  Gateway: not running (no PID file found)")
		return
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		fmt.Println("  Gateway: invalid PID file, removing")
		_ = os.Remove(pidFile)
		return
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Printf("  Gateway: process %d not found, removing PID file\n", pid)
		_ = os.Remove(pidFile)
		return
	}

	if err := proc.Signal(syscall.Signal(0)); err != nil {
		fmt.Printf("  Gateway: process %d not running, removing PID file\n", pid)
		_ = os.Remove(pidFile)
		return
	}

	if err := proc.Signal(os.Interrupt); err != nil {
		fmt.Printf("  Gateway: failed to send interrupt to PID %d: %v\n", pid, err)
		return
	}

	fmt.Printf("  Gateway: sent interrupt to PID %d, waiting for shutdown...\n", pid)

	for i := 0; i < 30; i++ {
		time.Sleep(200 * time.Millisecond)
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			fmt.Println("  Gateway: stopped")
			_ = os.Remove(pidFile)
			return
		}
	}

	fmt.Println("  Gateway: graceful shutdown timed out, sending SIGKILL")
	_ = proc.Signal(syscall.SIGKILL)
	_ = os.Remove(pidFile)
}

func gatewayFooterEnabled(homeDir string) bool {
	_, err := os.Stat(filepath.Join(homeDir, "gateway_footer_on"))
	return err == nil
}

func notifyGatewayFooter(homeDir string, enabled bool) {
	flagFile := filepath.Join(homeDir, "gateway_footer_on")
	if enabled {
		_ = os.WriteFile(flagFile, nil, 0o644)
	} else {
		_ = os.Remove(flagFile)
	}
}

func gatewaySetup(homeDir string) {
	fmt.Println("  ╔══════════════════════════════════════════╗")
	fmt.Println("  ║     Gateway Interactive Setup            ║")
	fmt.Println("  ╚══════════════════════════════════════════╝")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)

	platforms := []struct {
		name   string
		envVar string
		desc   string
	}{
		{"telegram", "TELEGRAM_BOT_TOKEN", "Telegram Bot API - chat messaging"},
		{"discord", "DISCORD_BOT_TOKEN", "Discord Bot - gaming/community chat"},
		{"slack", "SLACK_BOT_TOKEN", "Slack Bot - workplace messaging"},
		{"dingtalk", "DINGTALK_BOT_TOKEN", "DingTalk - enterprise communication"},
		{"feishu", "FEISHU_BOT_TOKEN", "Feishu/Lark - enterprise collaboration"},
		{"wecom", "WECOM_BOT_TOKEN", "WeCom (企业微信) - enterprise WeChat"},
		{"whatsapp", "WHATSAPP_ACCESS_TOKEN", "WhatsApp Business API"},
		{"webhook", "", "Generic Webhook - custom HTTP integration"},
		{"signal", "SIGNAL_BOT_NUMBER", "Signal - encrypted messaging"},
		{"email", "EMAIL_SMTP_HOST", "Email - SMTP/IMAP email"},
		{"matrix", "MATRIX_HOMESERVER", "Matrix - decentralized chat"},
		{"qqbot", "QQBOT_APP_ID", "QQ Bot - QQ messaging"},
		{"weixin", "WEIXIN_APP_ID", "WeChat Official Account"},
		{"yuanbao", "YUANBAO_API_KEY", "Yuanbao (腾讯元宝) - Tencent AI"},
		{"bluebubbles", "BLUEBUBBLES_SERVER_URL", "BlueBubbles - iMessage bridge"},
		{"mattermost", "MATTERMOST_SERVER_URL", "Mattermost - open-source messaging"},
		{"wecom_callback", "WECOM_CALLBACK_CORP_ID", "WeCom Callback Server"},
		{"api_server", "", "REST API Server - HTTP endpoint"},
		{"cron", "", "Cron Scheduler - scheduled messages"},
		{"sms", "SMS_ACCOUNT_SID", "SMS - text messages"},
		{"homeassistant", "HOMEASSISTANT_SERVER_URL", "Home Assistant - smart home"},
		{"msgraph", "MSGRAPH_TENANT_ID", "Microsoft Graph - Teams/Outlook"},
		{"googlechat", "GOOGLECHAT_BOT_TOKEN", "Google Chat - Google Workspace Chat API"},
		{"imessage", "IMESSAGE_ENABLED", "iMessage - Apple Messages integration"},
		{"irc", "IRC_SERVER", "IRC - Internet Relay Chat protocol"},
		{"line", "LINE_CHANNEL_ACCESS_TOKEN", "LINE - LINE Messaging API bot"},
		{"msteams", "MSTEAMS_BOT_TOKEN", "Microsoft Teams - Teams SDK integration"},
		{"nextcloud-talk", "NEXTCLOUD_TALK_BASE_URL", "Nextcloud Talk - self-hosted chat"},
		{"nostr", "NOSTR_PRIVATE_KEY", "Nostr - decentralized encrypted DMs"},
		{"synology-chat", "SYNOLOGY_CHAT_WEBHOOK_URL", "Synology Chat - NAS chat webhook"},
		{"tlon", "TLON_SHIP_NAME", "Tlon - decentralized messaging on Urbit"},
		{"twitch", "TWITCH_OAUTH_TOKEN", "Twitch - Twitch Chat integration"},
		{"voice-call", "VOICE_CALL_PROVIDER", "Voice Call - Twilio/Telnyx/Plivo phone calls"},
		{"zalo", "ZALO_ACCESS_TOKEN", "Zalo - Vietnam messaging platform Bot API"},
		{"zalouser", "ZALO_PHONE_NUMBER", "Zalo Personal - Zalo personal account via QR"},
	}

	fmt.Println("  Step 1: API Keys")
	fmt.Println("  ─────────────────")
	fmt.Println("  Enter your API tokens for each platform you want to use.")
	fmt.Println("  Press Enter to skip a platform.")
	fmt.Println()

	for _, p := range platforms {
		if p.envVar != "" {
			current := os.Getenv(p.envVar)
			display := ""
			if current != "" {
				masked := current[:min(4, len(current))] + "***"
				display = fmt.Sprintf(" [current: %s]", masked)
			}
			fmt.Printf("  %s%s:\n", p.desc, display)
			fmt.Printf("  %s: ", p.envVar)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)
			if input != "" {
				cli.SaveEnvValue(p.envVar, input)
				os.Setenv(p.envVar, input)
				fmt.Println("    ✓ saved")
			} else {
				fmt.Println("    (skipped)")
			}
		} else {
			fmt.Printf("  %s (no API key required)\n", p.desc)
		}
	}

	fmt.Println()
	fmt.Println("  Step 2: Enable Platforms")
	fmt.Println("  ─────────────────────────")
	fmt.Println("  Enable the platforms you want to activate:")
	fmt.Println()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	pluginSystem, err := plugin.NewSystem(ctx, plugin.SystemConfig{
		HomeDir: homeDir,
		Logger:  logger,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "  plugin system: %v\n", err)
		return
	}
	defer pluginSystem.Shutdown()

	pluginSystem.RegisterBuiltin(builtin.Providers())

	for _, p := range platforms {
		entry := pluginSystem.Registry.Get(p.name)
		if entry == nil {
			continue
		}
		current := "disabled"
		if entry.Enabled {
			current = "enabled"
		}
		fmt.Printf("  %s [%s] - enable? (y/N): ", p.name, current)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))
		if input == "y" || input == "yes" {
			pluginSystem.Registry.Enable(p.name)
			fmt.Println("    ✓ enabled")
		} else {
			pluginSystem.Registry.Disable(p.name)
			fmt.Println("    (disabled)")
		}
	}

	pluginSystem.SavePluginConfig()

	fmt.Println()
	fmt.Println("  Step 3: LLM Provider")
	fmt.Println("  ────────────────────")
	cfg, _ := cli.LoadConfig()
	provider := cli.ResolveProvider(cfg)
	model := cli.ResolveModel(cfg)
	fmt.Printf("  Provider: %s\n", provider)
	fmt.Printf("  Model:    %s\n", model)
	fmt.Println()
	fmt.Println("  Configure with: covo-agent config provider <name>")
	fmt.Println("                 covo-agent config model <name>")

	fmt.Println()
	fmt.Println("  ══════════════════════════════════════════")
	fmt.Println("  Setup complete!")
	fmt.Println("  Start the gateway: covo-agent gateway start")
	fmt.Println("  Check status:      covo-agent gateway status")
	fmt.Println("  ══════════════════════════════════════════")
}

func startGateway(pidFile string) {
	_ = os.Remove(pidFile)

	if err := cli.LoadDotEnv(); err != nil {
		return
	}

	homeDir, err := cli.HomeDir()
	if err != nil {
		log.Fatalf("home dir: %v", err)
	}

	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0644); err != nil {
		log.Fatalf("write pid file: %v", err)
	}
	defer func() {
		_ = os.Remove(pidFile)
		fmt.Println("  PID file removed.")
	}()

	cfg, err := cli.LoadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Initialize plugin system
	ctx := context.Background()
	pluginSystem, err := plugin.NewSystem(ctx, plugin.SystemConfig{
		HomeDir: homeDir,
		Logger:  logger,
	})
	if err != nil {
		log.Fatalf("plugin system: %v", err)
	}
	defer pluginSystem.Shutdown()

	pluginSystem.RegisterBuiltin(builtin.Providers())

	registerPluginMemoryProviders(homeDir, pluginSystem)

	// Find enabled platform plugins (startGateway)
	platformEntries := pluginSystem.Registry.ListEnabledByCategory(plugin.CategoryPlatform)
	if len(platformEntries) == 0 {
		fmt.Println("  No enabled platform plugins found.")
		fmt.Println("  Enable one with: covo-agent plugin enable <name>")
		allPlatforms := builtin.Names()
		fmt.Println("  Available:", strings.Join(allPlatforms, ", "))
		return
	}

	var platforms []plugin.PlatformProvider
	for _, entry := range platformEntries {
		if p, ok := entry.Provider.(plugin.PlatformProvider); ok {
			if err := p.Validate(); err != nil {
				logger.Warn("gateway: platform validation failed",
					"name", p.Name(), "error", err)
				continue
			}
			platforms = append(platforms, p)
		}
	}

	if len(platforms) == 0 {
		fmt.Println("  No valid platform providers found (check API keys in .env)")
		return
	}

	fmt.Printf("  Starting gateway with %d platform(s):\n", len(platforms))
	for _, p := range platforms {
		fmt.Printf("    - %s\n", p.Name())
	}
	fmt.Println()

	providerType := cli.ResolveProvider(cfg)
	model := cli.ResolveModel(cfg)

	llm, err := cli.BuildProvider(providerType)
	if err != nil {
		log.Fatalf("build provider: %v", err)
	}

	workingDir, _ := os.Getwd()

	// Collect plugin lifecycle hooks
	pluginHooks := agent.ConvertPluginHooks(pluginSystem.LifecycleHooks())

	suspendStore := gateway.NewSessionSuspendStore()

	// Agent factory — creates a new agent per channel
	factory := func(ctx context.Context) (gateway.Agent, error) {
		ca, err := agent.NewCovoAgent(agent.CovoAgentConfig{
			Mode:                     agent.ModeGeneral,
			Provider:                 llm,
			ProviderName:             providerType,
			Model:                    model,
			WorkingDir:               workingDir,
			HomeDir:                  homeDir,
			Logger:                   logger,
			LifecycleHooks:           pluginHooks,
			Auxiliary:                auxiliaryConfigFromCLI(cfg),
			AuxiliaryProviderBuilder: cli.ResolveAuxiliaryProviderBuilder(),
			SessionSuspendFunc:       suspendStore.Suspend,
		})
		if err != nil {
			return nil, fmt.Errorf("create agent: %w", err)
		}
		return ca, nil
	}

	gw := gateway.New(gateway.Config{
		Platforms:      platforms,
		AgentFactory:   factory,
		FooterEnabled:  gatewayFooterEnabled(homeDir),
		FooterModel:    model,
		FooterProvider: providerType,
		PairingStore:   gateway.NewPairingStore(homeDir),
		SuspendStore:   suspendStore,
	})

	fmt.Println("  Gateway starting... Press Ctrl+C to stop.")
	fmt.Println()

	if err := gw.Start(ctx); err != nil {
		log.Fatalf("gateway start: %v", err)
	}

	// Wait for signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\n  Shutting down gateway...")
	gw.Stop()
	fmt.Println("  Gateway stopped.")
}
