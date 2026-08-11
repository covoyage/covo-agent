package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"

	"github.com/covoyage/covo-agent/internal/i18n"
)

const gistAPI = "https://api.github.com/gists"

type gistFile struct {
	Content string `json:"content"`
}

type gistRequest struct {
	Description string              `json:"description"`
	Public      bool                `json:"public"`
	Files       map[string]gistFile `json:"files"`
}

type gistResponse struct {
	HTMLURL string `json:"html_url"`
}

// shareSessionAsGist uploads the current conversation as an anonymous Gist.
func shareSessionAsGist(app *chat.ChatApp, ag *agentcore.Agent) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token == "" {
		app.PrintSystem(i18n.T("gist.no_token"))
		return
	}

	msgs := ag.State().Messages()
	if len(msgs) == 0 {
		app.PrintSystem(i18n.T("gist.no_messages"))
		return
	}

	var buf bytes.Buffer
	for _, msg := range msgs {
		if msg.Role == agentcore.RoleSystem {
			continue
		}
		role := strings.ToUpper(string(msg.Role))
		buf.WriteString(fmt.Sprintf("--- %s ---\n", role))
		buf.WriteString(msg.Content)
		buf.WriteString("\n\n")
	}

	description := fmt.Sprintf("covo-agent conversation — %s", time.Now().Format("2006-01-02 15:04"))

	reqBody := gistRequest{
		Description: description,
		Public:      false,
		Files: map[string]gistFile{
			"conversation.md": {Content: buf.String()},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		app.PrintError(fmt.Errorf("marshal: %w", err))
		return
	}

	req, err := http.NewRequest("POST", gistAPI, bytes.NewReader(body))
	if err != nil {
		app.PrintError(fmt.Errorf("request: %w", err))
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "covo-agent")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		app.PrintError(fmt.Errorf("create gist: %w", err))
		return
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		app.PrintError(fmt.Errorf("read response: %w", err))
		return
	}

	if resp.StatusCode != http.StatusCreated {
		app.PrintError(fmt.Errorf("gist API: %s", string(respData)))
		return
	}

	var gistResp gistResponse
	if err := json.Unmarshal(respData, &gistResp); err != nil {
		app.PrintError(fmt.Errorf("parse response: %w", err))
		return
	}

	app.PrintSystem(i18n.T("gist.created", "url", gistResp.HTMLURL))
}
