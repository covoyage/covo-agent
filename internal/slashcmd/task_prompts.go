package slashcmd

import "strings"

const (
	reviewScopeConversation = "conversation"
	reviewScopeUncommitted  = "uncommitted"
	reviewScopeBranch       = "branch"
)

func parseReviewArgs(parts []string) (scope, extra string) {
	args := strings.TrimSpace(strings.Join(parts[1:], " "))
	if args == "" {
		return reviewScopeUncommitted, ""
	}
	fields := strings.Fields(args)
	switch strings.ToLower(fields[0]) {
	case reviewScopeConversation, reviewScopeUncommitted, reviewScopeBranch:
		return strings.ToLower(fields[0]), strings.TrimSpace(strings.Join(fields[1:], " "))
	default:
		return reviewScopeUncommitted, args
	}
}

func buildReviewPrompt(scope, extra string) string {
	target := "the current uncommitted changes (staged, unstaged, and untracked)"
	switch scope {
	case reviewScopeConversation:
		target = "all code changes made in this conversation"
	case reviewScopeBranch:
		target = "this branch compared to main (or master)"
	}
	prompt := strings.Join([]string{
		"Please gather relevant context and carefully review " + target + ".",
		"Use git_status, git_diff, and read. Spawn role=reviewer if the diff is large.",
		"Report correctness bugs, regressions, and security issues with severity and file paths.",
		"Do not implement fixes unless I explicitly ask.",
	}, " ")
	if extra != "" {
		prompt += " Focus on: " + extra
	}
	return prompt
}

func buildPlanTaskPrompt(task string) string {
	prompt := strings.Join([]string{
		"Gather all relevant context, then think carefully about how to implement the following.",
		"Write a concrete plan with files to touch and risks.",
		"Do not edit files or run mutating commands yet.",
	}, " ")
	task = strings.TrimSpace(task)
	if task != "" {
		prompt += "\n\n" + task
	}
	return prompt
}

func buildInterviewPrompt(topic string) string {
	prompt := strings.Join([]string{
		"Interview me to better understand my request, then write a spec file.",
		"First gather relevant context (read files, docs, and manifests).",
		"Then use clarify or ask_user for several rounds of non-obvious questions — edge cases, preferences, constraints, and design decisions.",
		"Aim for at least 3 rounds with multiple questions each. Do not ask those questions as plain chat text.",
		"When satisfied, write a short-name-spec.md with everything gathered.",
		"Do not make code changes and do not write AGENTS.md (use /init for project instructions).",
		"Stop after creating the spec.",
	}, " ")
	topic = strings.TrimSpace(topic)
	if topic != "" {
		prompt += "\n\nRequest:\n" + topic
	}
	return prompt
}
