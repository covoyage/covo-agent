package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// ghClientID is a public OAuth client ID used for the device authorization
// grant flow. GitHub's device flow does not require a client secret — the
// client_id is a public identifier. This one is used by many CLI tools.
const ghClientID = "Iv23liQ7fJtRmLKtPx5F"

type deviceCodeResp struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Interval        int    `json:"interval"`
}

type accessTokenResp struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

// GitHubDeviceLogin performs the GitHub device authorization flow.
// It requests a device code, displays the user code, opens the browser,
// and polls until the user authorizes the application.
func GitHubDeviceLogin(ctx context.Context) (*LoginResult, error) {
	// Step 1: Request device code
	body := url.Values{"client_id": {ghClientID}, "scope": {"gist"}}
	resp, err := http.PostForm("https://github.com/login/device/code", body)
	if err != nil {
		return nil, fmt.Errorf("device code request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("device code: HTTP %d: %s", resp.StatusCode, string(data))
	}

	var dc deviceCodeResp
	if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
		return nil, fmt.Errorf("decode device code: %w", err)
	}

	// Step 2: Show user code and open browser
	fmt.Fprintf(os.Stderr, "\n  ───────────────────────────────────────────────\n")
	fmt.Fprintf(os.Stderr, "  GitHub Device Authorization\n\n")
	fmt.Fprintf(os.Stderr, "  First, copy your one-time code:  \033[1m%s\033[0m\n\n", dc.UserCode)
	fmt.Fprintf(os.Stderr, "  A browser will open to GitHub…\n")
	fmt.Fprintf(os.Stderr, "  (If it doesn't, visit: %s)\n", dc.VerificationURI)
	fmt.Fprintf(os.Stderr, "  ───────────────────────────────────────────────\n")

	_ = openBrowser(dc.VerificationURI)

	// Step 3: Poll for access token
	interval := dc.Interval
	if interval < 5 {
		interval = 5
	}

	pollBody := url.Values{
		"client_id":   {ghClientID},
		"device_code": {dc.DeviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(interval) * time.Second):
		}

		fmt.Fprintf(os.Stderr, "  Waiting for authorization...\n")

		resp, err := http.PostForm("https://github.com/login/oauth/access_token", pollBody)
		if err != nil {
			return nil, fmt.Errorf("poll: %w", err)
		}

		var tr accessTokenResp
		json.NewDecoder(resp.Body).Decode(&tr)
		resp.Body.Close()

		switch tr.Error {
		case "":
			fmt.Fprintf(os.Stderr, "  ✓ Authorized!\n")
			return &LoginResult{
				EnvVar:   "GITHUB_TOKEN",
				Value:    tr.AccessToken,
				Provider: "GitHub",
			}, nil
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5
			continue
		case "expired_token":
			return nil, fmt.Errorf("device code expired, please try again")
		default:
			return nil, fmt.Errorf("oauth error: %s: %s", tr.Error, tr.ErrorDesc)
		}
	}
}
