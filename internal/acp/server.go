package acp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	acplib "github.com/covoyage/covonaut/acp"
)

var Version = "0.1.0-dev"

func NewServer(ctx context.Context, logger *slog.Logger) (*acplib.Server, error) {
	homeDir, err := acplib.EnsureHomeDir("")
	if err != nil {
		return nil, fmt.Errorf("ensure home dir: %w", err)
	}

	_ = os.MkdirAll(filepath.Join(homeDir, "sessions"), 0700)

	factory := newCovoAgentFactory(homeDir, logger)
	sessionStore := newFileSessionStore(homeDir)
	authProvider := &covoAuthProvider{}

	sessionMgr := acplib.NewSessionManager(acplib.SessionManagerConfig{
		AgentFactory: factory,
		SessionStore: sessionStore,
		HomeDir:      homeDir,
		Logger:       logger,
	})

	server := acplib.NewServer(acplib.ServerConfig{
		SessionManager: sessionMgr,
		AgentInfo: acplib.AgentInfo{
			Name:    "covo-agent",
			Version: Version,
		},
		AuthProvider: authProvider,
		Logger:       logger,
	})

	return server, nil
}
