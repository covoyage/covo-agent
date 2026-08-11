package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/session"
	"github.com/spf13/cobra"
)

func newSessionCommand(runtime *commandRuntime) *cobra.Command {
	sessionCmd := &cobra.Command{
		Use:   "session",
		Short: "Manage session history",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSessionManager(runtime.homeDir, func(store *session.Manager) error {
				return listSessions(cmd.OutOrStdout(), store, cmd.Context())
			})
		},
	}

	sessionCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List saved sessions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSessionManager(runtime.homeDir, func(store *session.Manager) error {
				return listSessions(cmd.OutOrStdout(), store, cmd.Context())
			})
		},
	})
	sessionCmd.AddCommand(&cobra.Command{
		Use:   "delete <session-id>",
		Short: "Delete a saved session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSessionManager(runtime.homeDir, func(store *session.Manager) error {
				if err := store.DeleteSession(cmd.Context(), args[0]); err != nil {
					return fmt.Errorf("delete session: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  ✓ session %s deleted\n", args[0])
				return nil
			})
		},
	})

	format := "text"
	exportCmd := &cobra.Command{
		Use:   "export <session-id>",
		Short: "Export a saved session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSessionManager(runtime.homeDir, func(store *session.Manager) error {
				return exportSession(cmd.OutOrStdout(), store, cmd.Context(), args[0], format)
			})
		},
	}
	exportCmd.Flags().StringVarP(&format, "format", "f", "text", "export format: text, md, markdown, or html")
	sessionCmd.AddCommand(exportCmd)

	sessionCmd.AddCommand(&cobra.Command{
		Use:   "rename <session-id> <new-name>",
		Short: "Rename a saved session",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSessionManager(runtime.homeDir, func(store *session.Manager) error {
				if err := store.RenameSession(cmd.Context(), args[0], args[1]); err != nil {
					return fmt.Errorf("rename session: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  ✓ session %s renamed to %q\n", args[0], args[1])
				return nil
			})
		},
	})
	sessionCmd.AddCommand(&cobra.Command{
		Use:   "prune [days]",
		Short: "Delete sessions older than a number of days",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			days, err := parsePruneDays(args)
			if err != nil {
				return err
			}
			return withSessionManager(runtime.homeDir, func(store *session.Manager) error {
				deleted, err := store.PruneSessions(cmd.Context(), days)
				if err != nil {
					return fmt.Errorf("prune sessions: %w", err)
				}
				if days == 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "  ✓ cleaned up all %d sessions\n", deleted)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "  ✓ pruned %d sessions older than %d days\n", deleted, days)
				}
				return nil
			})
		},
	})
	sessionCmd.AddCommand(&cobra.Command{
		Use:   "search <query...>",
		Short: "Search saved sessions",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSessionManager(runtime.homeDir, func(store *session.Manager) error {
				return searchSessions(cmd.OutOrStdout(), store, cmd.Context(), strings.Join(args, " "))
			})
		},
	})
	sessionCmd.AddCommand(&cobra.Command{
		Use:   "import <jsonl-file>",
		Short: "Import a session from JSONL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSessionManager(runtime.homeDir, func(store *session.Manager) error {
				id, count, err := importSessionFromJSONL(cmd.Context(), store, args[0])
				if err != nil {
					return fmt.Errorf("import session: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  ✓ imported %d messages as session %s\n", count, shortSessionID(id))
				return nil
			})
		},
	})
	sessionCmd.AddCommand(&cobra.Command{
		Use:   "stats",
		Short: "Show session statistics",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSessionManager(runtime.homeDir, func(store *session.Manager) error {
				return writeSessionStats(cmd.OutOrStdout(), store, cmd.Context())
			})
		},
	})

	return sessionCmd
}

func withSessionManager(homeDir string, run func(*session.Manager) error) error {
	if homeDir == "" {
		var err error
		homeDir, err = cli.HomeDir()
		if err != nil {
			return fmt.Errorf("home dir: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Join(homeDir, "sessions"), 0755); err != nil {
		return fmt.Errorf("sessions dir: %w", err)
	}
	store, err := session.NewManager(homeDir)
	if err != nil {
		return fmt.Errorf("init session store: %w", err)
	}
	defer store.Close()
	return run(store)
}

func exportSession(w io.Writer, store *session.Manager, ctx context.Context, sessionID, format string) error {
	var text string
	var err error
	switch format {
	case "text":
		text, err = store.ExportSession(ctx, sessionID)
	case "md", "markdown":
		text, err = store.ExportSessionMarkdown(ctx, sessionID)
	case "html":
		text, err = store.ExportSessionHTML(ctx, sessionID)
	default:
		return fmt.Errorf("unsupported export format %q", format)
	}
	if err != nil {
		return fmt.Errorf("export session: %w", err)
	}
	_, err = fmt.Fprint(w, text)
	return err
}

func parsePruneDays(args []string) (int, error) {
	days := 30
	if len(args) == 0 {
		return days, nil
	}
	if _, err := fmt.Sscanf(args[0], "%d", &days); err != nil {
		return 0, fmt.Errorf("invalid days %q: must be an integer", args[0])
	}
	if days < 0 {
		return 0, nil
	}
	return days, nil
}

func searchSessions(w io.Writer, store *session.Manager, ctx context.Context, query string) error {
	results, err := store.SearchSessions(ctx, query, 20)
	if err != nil {
		return fmt.Errorf("search sessions: %w", err)
	}
	if len(results) == 0 {
		fmt.Fprintln(w, "  No results.")
		return nil
	}
	fmt.Fprintf(w, "── Search results for %q (%d hits) ──\n", query, len(results))
	for _, result := range results {
		fmt.Fprintf(w, "  [%s] %s: %s\n", shortSessionID(result.SessionID), result.Role, result.Snippet)
	}
	return nil
}

func writeSessionStats(w io.Writer, store *session.Manager, ctx context.Context) error {
	list, err := store.ListSessions(ctx)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	totalMessages := int64(0)
	oldest := time.Now()
	newest := time.Time{}
	for _, item := range list {
		totalMessages += item.MessageCount
		if item.CreatedAt.Before(oldest) {
			oldest = item.CreatedAt
		}
		if item.UpdatedAt.After(newest) {
			newest = item.UpdatedAt
		}
	}
	fmt.Fprintf(w, "  Total sessions: %d\n", len(list))
	fmt.Fprintf(w, "  Total messages: %d\n", totalMessages)
	if len(list) > 0 {
		fmt.Fprintf(w, "  Oldest:        %s\n", oldest.Format("2006-01-02"))
		fmt.Fprintf(w, "  Newest:        %s\n", newest.Format("2006-01-02"))
	}
	return nil
}

func shortSessionID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

type importMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func importSessionFromJSONL(ctx context.Context, mgr *session.Manager, path string) (string, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	sessionID := fmt.Sprintf("%x", time.Now().UnixNano())
	mgr.SetCurrentSessionID(sessionID)
	mgr.DB().EnsureSession(ctx, sessionID, "")

	var count int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg importMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			return "", count, fmt.Errorf("line %d: %w", count+1, err)
		}
		role := agentcore.Role(msg.Role)
		if role != agentcore.RoleUser && role != agentcore.RoleAssistant && role != agentcore.RoleSystem {
			role = agentcore.RoleUser
		}
		mgr.DB().AppendMessage(ctx, sessionID, agentcore.Message{
			Role:    role,
			Content: msg.Content,
		})
		count++
	}
	if err := scanner.Err(); err != nil {
		return sessionID, count, err
	}

	// Set title from filename
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	mgr.RenameSession(ctx, sessionID, name)

	return sessionID, count, nil
}

func listSessions(w io.Writer, store *session.Manager, ctx context.Context) error {
	list, err := store.ListSessions(ctx)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	if len(list) == 0 {
		fmt.Fprintln(w, "  No sessions.")
		return nil
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].UpdatedAt.After(list[j].UpdatedAt)
	})
	for _, s := range list {
		name := s.ID
		if s.Label != "" {
			name = s.Label
		} else if s.Name != "" {
			name = s.Name
		}
		fmt.Fprintf(w, "  %s  %s  %d msgs  %s\n",
			shortSessionID(s.ID),
			s.UpdatedAt.Format("2006-01-02 15:04"),
			s.MessageCount,
			name,
		)
	}
	return nil
}
