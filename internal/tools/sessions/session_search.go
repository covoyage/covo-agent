package sessions

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covo-agent/internal/visibility"
)

type sessionEntry struct {
	Type      string          `json:"type"`
	ID        string          `json:"id,omitempty"`
	Timestamp string          `json:"timestamp,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

type sessionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type sessionResult struct {
	SessionID  string   `json:"session_id"`
	Name       string   `json:"name,omitempty"`
	Timestamp  string   `json:"timestamp,omitempty"`
	MatchCount int      `json:"match_count,omitempty"`
	Snippets   []string `json:"snippets,omitempty"`
	MsgCount   int      `json:"msg_count"`
	Preview    string   `json:"preview,omitempty"`
}

type sessionMeta struct {
	id        string
	name      string
	timestamp time.Time
	msgCount  int
	preview   string
}

func BuildSessionSearchTool(sessionsDir string, fts *FTSSearcher) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "session_search",
		Description: strings.Join([]string{
			"Search and browse past conversation sessions. Use this to recall previous work,",
			"find code changes discussed earlier, or check what was done in past sessions.",
			"",
			"Without a query: lists recent sessions with metadata (most recent first).",
			"With a keyword query: full-text search across session transcripts, returning",
			"matching snippets grouped by session.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Optional keyword or phrase to search for. Omit to list recent sessions.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Max number of sessions to return (default: 10, max: 50).",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if params.Limit <= 0 {
				params.Limit = 10
			}
			if params.Limit > 50 {
				params.Limit = 50
			}

			vp := visibility.PolicyFromContext(ctx)
			visibleIDs := loadVisibleSessionIDs(sessionsDir, vp)

			if fts != nil {
				results, err := fts.Search(ctx, params.Query, params.Limit)
				if err != nil {
					return nil, err
				}
				return filterSessionResults(results, visibleIDs), nil
			}

			// Fallback to linear scan if FTS not available
			if params.Query == "" {
				results, err := listRecentSessions(sessionsDir, params.Limit)
				if err != nil {
					return nil, err
				}
				return filterSessionResults(results, visibleIDs), nil
			}
			results, err := searchSessions(sessionsDir, params.Query, params.Limit)
			if err != nil {
				return nil, err
			}
			return filterSessionResults(results, visibleIDs), nil
		},
	}
}

func listRecentSessions(sessionsDir string, limit int) (any, error) {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("read sessions dir: %w", err)
	}

	var sessions []sessionMeta
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		meta, err := readSessionMeta(filepath.Join(sessionsDir, entry.Name()))
		if err != nil {
			continue
		}
		meta.id = id
		sessions = append(sessions, meta)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].timestamp.After(sessions[j].timestamp)
	})

	if len(sessions) > limit {
		sessions = sessions[:limit]
	}

	var results []sessionResult
	for _, s := range sessions {
		r := sessionResult{
			SessionID: s.id,
			MsgCount:  s.msgCount,
			Preview:   s.preview,
		}
		if !s.timestamp.IsZero() {
			r.Timestamp = s.timestamp.Format(time.RFC3339)
		}
		if s.name != "" {
			r.Name = s.name
		}
		results = append(results, r)
	}

	return map[string]any{
		"count":    len(results),
		"sessions": results,
	}, nil
}

func readSessionMeta(path string) (meta sessionMeta, err error) {
	f, err := os.Open(path)
	if err != nil {
		return meta, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var firstUserMsg string
	for scanner.Scan() {
		line := scanner.Text()
		var entry sessionEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		switch entry.Type {
		case "session":
			if entry.Timestamp != "" {
				meta.timestamp, _ = time.Parse(time.RFC3339, entry.Timestamp)
			}
		case "session_info":
			var info struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(entry.Data, &info)
			meta.name = info.Name
		case "message":
			meta.msgCount++
			if firstUserMsg == "" {
				var msg sessionMessage
				if err := json.Unmarshal(entry.Data, &msg); err == nil && msg.Role == "user" {
					content := msg.Content
					if len(content) > 200 {
						content = content[:200] + "..."
					}
					firstUserMsg = content
				}
			}
		}
	}

	meta.preview = firstUserMsg
	return meta, nil
}

func searchSessions(sessionsDir, query string, limit int) (any, error) {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("read sessions dir: %w", err)
	}

	queryLower := strings.ToLower(query)
	queryTerms := strings.Fields(queryLower)

	var results []sessionResult
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		matches, msgCount, name, ts, err := searchSessionFile(
			filepath.Join(sessionsDir, entry.Name()), queryLower, queryTerms,
		)
		if err != nil || len(matches) == 0 {
			continue
		}

		r := sessionResult{
			SessionID:  id,
			Name:       name,
			MatchCount: len(matches),
			Snippets:   matches,
			MsgCount:   msgCount,
		}
		if !ts.IsZero() {
			r.Timestamp = ts.Format(time.RFC3339)
		}
		results = append(results, r)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].MatchCount > results[j].MatchCount
	})

	if len(results) > limit {
		results = results[:limit]
	}

	return map[string]any{
		"query":   query,
		"count":   len(results),
		"results": results,
	}, nil
}

func searchSessionFile(path, queryLower string, queryTerms []string) (snippets []string, msgCount int, name string, ts time.Time, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, "", ts, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	const maxSnippets = 5
	const snippetContext = 120

	for scanner.Scan() {
		line := scanner.Text()
		var entry sessionEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		switch entry.Type {
		case "session":
			if entry.Timestamp != "" {
				ts, _ = time.Parse(time.RFC3339, entry.Timestamp)
			}
		case "session_info":
			var info struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(entry.Data, &info)
			name = info.Name
		case "message":
			msgCount++
			var msg sessionMessage
			if err := json.Unmarshal(entry.Data, &msg); err != nil {
				continue
			}
			contentLower := strings.ToLower(msg.Content)
			if !matchesQuery(contentLower, queryLower, queryTerms) {
				continue
			}
			if len(snippets) < maxSnippets {
				snippet := extractSnippet(msg.Content, queryLower, snippetContext)
				snippets = append(snippets, fmt.Sprintf("[%s] %s", msg.Role, snippet))
			}
		}
	}

	return snippets, msgCount, name, ts, nil
}

func matchesQuery(contentLower, queryLower string, queryTerms []string) bool {
	if strings.Contains(contentLower, queryLower) {
		return true
	}
	for _, term := range queryTerms {
		if !strings.Contains(contentLower, term) {
			return false
		}
	}
	return len(queryTerms) > 1
}

func extractSnippet(content, queryLower string, contextLen int) string {
	contentLower := strings.ToLower(content)
	idx := strings.Index(contentLower, queryLower)

	if idx < 0 {
		// Multi-term match — find first term
		for _, term := range strings.Fields(queryLower) {
			idx = strings.Index(contentLower, term)
			if idx >= 0 {
				break
			}
		}
	}
	if idx < 0 {
		if len(content) > contextLen*2 {
			return content[:contextLen*2] + "..."
		}
		return content
	}

	start := idx - contextLen
	if start < 0 {
		start = 0
	}
	end := idx + contextLen
	if end > len(content) {
		end = len(content)
	}

	snippet := content[start:end]
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(content) {
		snippet = snippet + "..."
	}
	return snippet
}

// loadVisibleSessionIDs returns the set of session IDs that the current
// visibility policy allows. When vp is nil or mode is Shared, returns nil
// (meaning all sessions are visible).
func loadVisibleSessionIDs(sessionsDir string, vp *visibility.Policy) map[string]bool {
	if vp == nil || vp.Mode == visibility.Shared {
		return nil // nil = all visible
	}

	indexPath := filepath.Join(sessionsDir, "sessions.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil // can't determine visibility; allow all
	}

	var sessions []struct {
		ID     string `json:"id"`
		Origin struct {
			Platform string `json:"platform"`
			ChatID   string `json:"chat_id"`
		} `json:"origin"`
	}
	if err := json.Unmarshal(data, &sessions); err != nil {
		return nil
	}

	visible := make(map[string]bool)
	for _, s := range sessions {
		key := s.Origin.Platform + ":" + s.Origin.ChatID
		if vp.ShouldAllow(key) {
			visible[s.ID] = true
		}
	}
	return visible
}

// filterSessionResults filters session search results by removing
// sessions not in the visibleIDs set. If visibleIDs is nil, no filtering
// is applied. Handles both map[string]any (from list/search) and
// []sessionResult (from FTS) return shapes.
func filterSessionResults(results any, visibleIDs map[string]bool) any {
	if visibleIDs == nil {
		return results
	}

	// Direct slice of sessionResult (from FTS Search)
	if list, ok := results.([]sessionResult); ok {
		var filtered []sessionResult
		for _, s := range list {
			if s.SessionID == "" || visibleIDs[s.SessionID] {
				filtered = append(filtered, s)
			}
		}
		return filtered
	}

	resultMap, ok := results.(map[string]any)
	if !ok {
		return results
	}

	// Handle different result shapes from search vs list
	for _, key := range []string{"sessions", "results"} {
		sessionList, ok := resultMap[key]
		if !ok {
			continue
		}
		list, ok := sessionList.([]sessionResult)
		if !ok {
			// Try []any path for JSON-decoded results
			anyList, ok := sessionList.([]any)
			if !ok {
				continue
			}
			var filtered []any
			for _, item := range anyList {
				m, ok := item.(map[string]any)
				if !ok {
					continue
				}
				sid, _ := m["session_id"].(string)
				if sid == "" || visibleIDs[sid] {
					filtered = append(filtered, item)
				}
			}
			resultMap[key] = filtered
			continue
		}
		var filtered []sessionResult
		for _, s := range list {
			if s.SessionID == "" || visibleIDs[s.SessionID] {
				filtered = append(filtered, s)
			}
		}
		resultMap[key] = filtered
	}

	return resultMap
}
