package code

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

func NewPRCommand() *cobra.Command {
	prCmd := &cobra.Command{
		Use:   "pr",
		Short: "Manage pull requests",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cmdPR(nil)
			return nil
		},
	}
	var publish bool
	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Print a git summary of the current branch (use --publish to open a GitHub PR)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			args := []string{"create"}
			if publish {
				args = append(args, "--publish")
			}
			cmdPR(args)
			return nil
		},
	}
	createCmd.Flags().BoolVar(&publish, "publish", false, "push the PR to GitHub")
	prCmd.AddCommand(createCmd)
	prCmd.AddCommand(&cobra.Command{
		Use:   "review [base]",
		Short: "Review current branch changes with the agent",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			base := ""
			if len(args) > 0 {
				base = args[0]
			}
			return runReview(cmd.OutOrStdout(), base, false)
		},
	})
	return prCmd
}

func cmdPR(args []string) {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		printPRUsage()
		return
	}

	switch args[0] {
	case "create":
		cmdPRCreate(args[1:])
	case "review":
		if err := runReview(os.Stdout, "", false); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		printPRUsage()
	}
}

func printPRUsage() {
	fmt.Println("Usage: covo-agent pr <command>")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  create           Print a git summary of the current branch")
	fmt.Println("    --publish      Push the PR to GitHub (requires GITHUB_TOKEN)")
	fmt.Println("  review           Review current branch changes with the agent")
	fmt.Println("  help             Show this help")
}

func cmdPRCreate(args []string) {
	publish := false
	for _, a := range args {
		if a == "--publish" {
			publish = true
		}
	}

	if _, err := exec.LookPath("git"); err != nil {
		fmt.Fprintln(os.Stderr, "error: git is not installed or not in PATH")
		os.Exit(1)
	}

	repoCheck := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	if err := repoCheck.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error: not in a git repository")
		os.Exit(1)
	}

	branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	branchOut, err := branchCmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting branch: %v\n", err)
		os.Exit(1)
	}
	branch := strings.TrimSpace(string(branchOut))

	base := detectBaseBranch()

	diffStatCmd := exec.Command("git", "diff", base+"...HEAD", "--stat")
	diffStatOut, _ := diffStatCmd.Output()
	diffStat := strings.TrimSpace(string(diffStatOut))

	diffCmd := exec.Command("git", "diff", base+"...HEAD")
	diffOut, _ := diffCmd.Output()
	fullDiff := string(diffOut)

	logCmd := exec.Command("git", "log", base+"..HEAD", "--oneline", "--no-decorate")
	logOut, _ := logCmd.Output()
	commits := strings.TrimSpace(string(logOut))

	title := branch
	if firstLine := strings.SplitN(commits, "\n", 2); len(firstLine) > 0 && firstLine[0] != "" {
		title = firstLine[0]
	}

	var summaryParts []string
	summaryParts = append(summaryParts, fmt.Sprintf("Branch: %s", branch))
	summaryParts = append(summaryParts, "")

	if diffStat != "" {
		summaryParts = append(summaryParts, "Changes:")
		for _, line := range strings.Split(diffStat, "\n") {
			summaryParts = append(summaryParts, "  "+line)
		}
		summaryParts = append(summaryParts, "")
	}

	if commits != "" {
		summaryParts = append(summaryParts, "Commits:")
		for _, c := range strings.Split(commits, "\n") {
			summaryParts = append(summaryParts, "  "+c)
		}
		summaryParts = append(summaryParts, "")
	}

	if fullDiff != "" {
		summaryParts = append(summaryParts, "Key Changes Overview:")
		lines := strings.Split(fullDiff, "\n")
		shown := 0
		for _, line := range lines {
			if shown >= 30 {
				summaryParts = append(summaryParts, "  ... (truncated)")
				break
			}
			if strings.HasPrefix(line, "diff --git") || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "@@") {
				summaryParts = append(summaryParts, "  "+line)
				shown++
			}
		}
	}

	body := strings.Join(summaryParts, "\n")

	fmt.Println("── PR Summary ──")
	fmt.Printf("Title: %s\n\n", title)
	fmt.Println(body)

	if publish {
		token := os.Getenv("GITHUB_TOKEN")
		if token == "" {
			token = os.Getenv("GH_TOKEN")
		}
		if token == "" {
			fmt.Fprintln(os.Stderr, "\nerror: GITHUB_TOKEN is not set (try 'covo-agent auth login github')")
			os.Exit(1)
		}

		remoteCmd := exec.Command("git", "remote", "get-url", "origin")
		remoteOut, err := remoteCmd.Output()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error getting remote: %v\n", err)
			os.Exit(1)
		}
		remote := strings.TrimSpace(string(remoteOut))

		owner, repo := parseGitRemote(remote)
		if owner == "" || repo == "" {
			fmt.Fprintln(os.Stderr, "error: could not parse owner/repo from remote URL")
			os.Exit(1)
		}

		payload := map[string]string{
			"title": title,
			"head":  branch,
			"base":  "main",
			"body":  body,
		}
		payloadBytes, _ := json.Marshal(payload)

		apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls", owner, repo)
		req, err := http.NewRequest("POST", apiURL, bytes.NewReader(payloadBytes))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating request: %v\n", err)
			os.Exit(1)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating PR: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)

		if resp.StatusCode >= 400 {
			fmt.Fprintf(os.Stderr, "GitHub API error (%d): %s\n", resp.StatusCode, string(respBody))
			os.Exit(1)
		}

		var result map[string]any
		if err := json.Unmarshal(respBody, &result); err == nil {
			if htmlURL, ok := result["html_url"].(string); ok {
				fmt.Printf("\nPR created: %s\n", htmlURL)
				return
			}
		}

		fmt.Printf("\nPR created: %s\n", string(respBody))
	}
}

// detectBaseBranch finds the best merge base (main > master > first parent).
func detectBaseBranch() string {
	for _, candidate := range []string{"origin/main", "origin/master", "main", "master"} {
		cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", candidate)
		if cmd.Run() == nil {
			// Check it has commits reachable from HEAD
			merge := exec.Command("git", "merge-base", "--is-ancestor", candidate, "HEAD")
			if merge.Run() == nil {
				return candidate
			}
		}
	}
	return "HEAD~1"
}

func parseGitRemote(remote string) (string, string) {
	remote = strings.TrimSpace(remote)

	if strings.HasPrefix(remote, "https://") {
		path := strings.TrimPrefix(remote, "https://")
		if idx := strings.IndexByte(path, '/'); idx >= 0 {
			path = path[idx+1:]
		}
		path = strings.TrimSuffix(path, ".git")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) == 2 {
			return parts[0], parts[1]
		}
		return "", ""
	}

	if strings.HasPrefix(remote, "git@") {
		remote = strings.TrimPrefix(remote, "git@")
		if idx := strings.IndexByte(remote, ':'); idx >= 0 {
			remote = remote[idx+1:]
		}
		remote = strings.TrimSuffix(remote, ".git")
		parts := strings.SplitN(remote, "/", 2)
		if len(parts) == 2 {
			return parts[0], parts[1]
		}
		return "", ""
	}

	return "", ""
}
