---
name: coding-agent
description: "Delegate coding tasks to Codex, Claude Code, OpenCode, or Pi agents via background shell processes."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos]
prerequisites:
  commands: [claude, codex, opencode, pi]
metadata:
  tags: [Coding, Agent, Delegation, Background, Git, PR, Review]
---

# Coding Agent (always backgrounded)

Use **shell** with **background:true** for all coding-agent work.
Do not use a foreground one-shot path here.
Start the agent, get the `sessionId`, monitor with `process`, and require the worker to notify the user directly when it finishes.

## PTY Mode: Codex/Pi/OpenCode yes, Claude Code no

For **Codex, Pi, and OpenCode**, PTY is required:

```bash
# Correct for Codex/Pi/OpenCode
shell pty:true background:true command:"codex exec 'Your prompt'"
```

For **Claude Code** (`claude` CLI), use `--print --permission-mode bypassPermissions` instead.
Do not use PTY for Claude Code here.

```bash
# Correct for Claude Code
shell background:true command:"claude --permission-mode bypassPermissions --print 'Your task'"

# Wrong for Claude Code (PTY, wrong flags, no background)
shell pty:true command:"claude --dangerously-skip-permissions 'task'"
```

### Shell Tool Parameters

| Parameter    | Type    | Description                                 |
| ------------ | ------- | ------------------------------------------- |
| `command`    | string  | The shell command to run                    |
| `pty`        | boolean | Use for Codex/Pi/OpenCode                   |
| `workdir`    | string  | Working directory                           |
| `background` | boolean | **Always true for this skill**              |
| `timeout`    | number  | Timeout in seconds                          |
| `elevated`   | boolean | Run on host instead of sandbox (if allowed) |

### Process Tool Actions

| Action      | Description                                          |
| ----------- | ---------------------------------------------------- |
| `list`      | List all running/recent sessions                     |
| `poll`      | Check if session is still running                    |
| `log`       | Get session output (with optional offset/limit)      |
| `write`     | Send raw data to stdin                               |
| `submit`    | Send data + newline (like typing and pressing Enter) |
| `send-keys` | Send key tokens or hex bytes                         |
| `paste`     | Paste text (with optional bracketed mode)            |
| `kill`      | Terminate the session                                |

---

## Mandatory Pattern

Every coding-agent run follows this pattern:

1. Capture the notification route from the current conversation before spawning:
   - `notifyChannel`
   - `notifyTarget`
   - `notifyAccount` (if applicable)
   - `notifyReplyTo` (if replying to a specific message is desired)
   - `notifyThreadId` (Telegram topic / Slack thread when applicable)
2. Start the coding CLI with `background:true` immediately.
3. Include the notification route in the worker prompt and require the worker to call `sessions_send` on completion.
4. Monitor with `process action:log` / `poll`.
5. If the worker needs input or fails before notifying, handle that explicitly yourself. Do not rely on heartbeat.

If you do not have a trustworthy notification route, say so and do not claim that completion will notify the user automatically.

### Multi-hour issue-to-PR builds

For feature builds, bug fixes, or product work that may take a long time, use a
GitHub issue as the durable spec before starting Codex:

1. Create or reuse the issue with `gh issue create` / `gh issue view`.
2. Start Codex in the background and include the issue URL, target repo, target
   branch/base, expected PR output, required proof, and the notification route.
3. Tell Codex to create a branch, implement, run the relevant checks, run Codex
   review/auto-review until there are no accepted actionable findings, and open
   a PR linked to the issue.
4. Return the issue URL and background `sessionId` immediately. If a dashboard
   task/flow URL exists, return that too.
5. Monitor with `process`. Abort with `process kill` for raw background runs, or
   `tasks.cancel` when the work is mirrored into the Task Registry.

Prefer a dashboard-backed issue-build/task-flow lane over a raw background
process when available. Do not block the user on the issue creation or build
loop; long-running work is expected.

---

## Notification Route

Use a direct outbound completion message:

```bash
sessions_send channel:<channel> target:<target> message:"<text>"
```

Add optional routing flags only when they are real and applicable:

- `account:<id>`
- `reply_to:<messageId>`
- `thread_id:<threadId>`

### Completion Prompt Snippet

Append something like this to every worker prompt:

```text
Notification route for completion:
- channel: <notifyChannel>
- target: <notifyTarget>
- account: <notifyAccount or omit>
- reply_to: <notifyReplyTo or omit>
- thread_id: <notifyThreadId or omit>

When the task is completely finished, send exactly one completion message back to the user with sessions_send using that route.
If the task fails fatally, send exactly one failure message back to the user with sessions_send using that route.
Do not skip the completion/failure message.
```

### Completion Command Template

```bash
sessions_send channel:<notifyChannel> target:<notifyTarget> message:"Done: <brief summary>"
```

Optional additions:

```bash
sessions_send channel:<notifyChannel> target:<notifyTarget> message:"Done: <brief summary>" account:<notifyAccount> reply_to:<notifyReplyTo> thread_id:<notifyThreadId>
```

---

## Quick Start

For scratch Codex work, create a temp git repo first, then start the worker in the background with the completion route injected into the prompt:

```bash
SCRATCH=$(mktemp -d)
cd "$SCRATCH" && git init

shell pty:true workdir:$SCRATCH background:true command:"codex exec 'Your prompt here.

Notification route for completion:
- channel: <notifyChannel>
- target: <notifyTarget>
- account: <notifyAccount or omit>
- reply_to: <notifyReplyTo or omit>
- thread_id: <notifyThreadId or omit>

When the task is completely finished, send exactly one completion message back to the user with sessions_send using that route.
If the task fails fatally, send exactly one failure message back to the user with sessions_send using that route.
Do not skip the completion/failure message.'"
```

Codex refuses to run outside a trusted git directory.
Reuse this same notify-route injection block in every example below; only the task-specific prompt body should change.

---

## Codex CLI

**Model:** `gpt-5.2-codex` is the default (set in ~/.codex/config.toml)

### Flags

| Flag            | Effect                                   |
| --------------- | ---------------------------------------- |
| `exec "prompt"` | One-shot execution inside the worker CLI |
| `--full-auto`   | Sandboxed but auto-approves in workspace |
| `--yolo`        | No sandbox, no approvals                 |

### Building/Creating

```bash
# Always background immediately
shell pty:true workdir:~/project background:true command:"codex exec --full-auto 'Build a dark mode toggle'"

# More autonomy
shell pty:true workdir:~/project background:true command:"codex --yolo 'Refactor the auth module'"
```

### Reviewing PRs

**Never review PRs in the project's own folder.**
Clone to a temp folder or use a worktree.

```bash
REVIEW_DIR=$(mktemp -d)
git clone https://github.com/user/repo.git $REVIEW_DIR
cd $REVIEW_DIR && gh pr checkout 130

shell pty:true workdir:$REVIEW_DIR background:true command:"codex review --base origin/main"
```

Or:

```bash
git worktree add /tmp/pr-130-review pr-130-branch
shell pty:true workdir:/tmp/pr-130-review background:true command:"codex review --base main"
```

### Batch PR Reviews

```bash
git fetch origin '+refs/pull/*/head:refs/remotes/origin/pr/*'

shell pty:true workdir:~/project background:true command:"codex exec 'Review PR #86. git diff origin/main...origin/pr/86'"
shell pty:true workdir:~/project background:true command:"codex exec 'Review PR #87. git diff origin/main...origin/pr/87'"

process action:list
process action:log sessionId:XXX
```

---

## Claude Code

```bash
shell workdir:~/project background:true command:"claude --permission-mode bypassPermissions --print 'Your task'"
```

---

## OpenCode

```bash
shell pty:true workdir:~/project background:true command:"opencode run 'Your task'"
```

---

## Pi Coding Agent

```bash
# Install: npm install -g @earendil-works/pi-coding-agent
shell pty:true workdir:~/project background:true command:"pi 'Your task'"

# Non-interactive mode
shell pty:true workdir:~/project background:true command:"pi -p 'Summarize src/'"

# Different provider/model
shell pty:true workdir:~/project background:true command:"pi --provider openai --model gpt-5.6 -p 'Your task'"
```

---

## Parallel Issue Fixing with git worktrees

```bash
git worktree add -b fix/issue-78 /tmp/issue-78 main
git worktree add -b fix/issue-99 /tmp/issue-99 main

shell pty:true workdir:/tmp/issue-78 background:true command:"pnpm install && codex --yolo 'Fix issue #78: <description>. Commit and push after review. Send the completion message with sessions_send using the provided notify route.'"
shell pty:true workdir:/tmp/issue-99 background:true command:"pnpm install && codex --yolo 'Fix issue #99 from the approved ticket summary. Implement only the in-scope edits. Send the completion message with sessions_send using the provided notify route.'"

process action:list
process action:log sessionId:XXX
```

---

## Rules

1. **Use the right execution mode per agent**:
   - Codex/Pi/OpenCode: `pty:true`
   - Claude Code: `--print --permission-mode bypassPermissions` (no PTY required)
2. **Respect tool choice** - if user asks for Codex, use Codex.
   - Orchestrator mode: do NOT hand-code patches yourself.
   - If an agent fails/hangs, respawn it or ask the user for direction, but don't silently take over.
3. **Be patient** - don't kill sessions because they're "slow"
4. **Monitor with process:log** - check progress without interfering
5. **--full-auto for building** - auto-approves changes
6. **vanilla for reviewing** - no special flags needed
7. **Parallel is OK** - run many Codex processes at once for batch work
8. **NEVER start Codex inside the agent state directory** (`~/.covo-agent`) - it'll read internal docs and get weird ideas!
9. **NEVER checkout branches in the live project directory** - that's the LIVE instance!
10. **Always inject the Completion Prompt Snippet** into the worker prompt before spawning. The simplified examples below omit it for brevity — never spawn a worker without it.

---

## Progress Updates (Critical)

When you spawn a coding agent in the background, keep the user in the loop.

- Send 1 short message when you start: what is running and where.
- Update only when something changes:
  - a milestone completes
  - the worker asks a question
  - you hit an error or need user action
  - the worker finishes
- If you kill a session, immediately say you killed it and why.
- If you are expecting the worker to self-notify with `sessions_send`, say that clearly in your start update.

This prevents the user from seeing only a missing reply and having no idea what happened.

---

## Rules Summary

1. **Always background immediately.**
   - Use `background:true` for every coding-agent launch.
   - Do not use the foreground one-shot path in this skill.
2. **Use the right execution mode per agent.**
   - Codex/Pi/OpenCode: `pty:true`
   - Claude Code: `--print --permission-mode bypassPermissions`
3. **Respect tool choice.**
   - If the user asked for Codex, use Codex.
   - Orchestrator mode: do not hand-code the patch yourself instead of using the requested coding agent.
4. **Capture notify routing before spawn.**
   - Completion messaging must have a real route.
5. **Use direct completion messaging.**
   - Require `sessions_send`.
6. **Do not silently take over.**
   - If a worker fails or hangs, respawn it or ask for direction. Do not quietly switch to hand-editing.
7. **Monitor with `process`.**
   - `process action:log` is the default low-friction check.
8. **Be patient.**
   - Do not kill sessions just because they are slow.
9. **Parallel is OK.**
   - Many background Codex sessions can run at once.
10. **Never start Codex in `~/.covo-agent/`.**
11. **Never checkout branches in the live project directory.**

---

## Learnings

- **PTY is essential** for Codex/Pi/OpenCode.
- **Git repo required**: Codex needs a trusted git directory.
- **Use `exec` under background orchestration**: short and long tasks follow the same path now.
- **`submit` vs `write`**: use `submit` to send input plus Enter.
- **Direct message send beats heartbeat for completion notification** when the user must be told immediately and heartbeat may be disabled.
