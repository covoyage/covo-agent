---
name: coding-agent
description: "Delegate coding tasks to Codex, Claude Code, OpenCode, or Pi via background process spawn (PTY when needed)."
version: 1.1.0
author: Covo Agent
license: MIT
platforms: [linux, macos]
prerequisites:
  commands: [claude, codex, opencode, pi]
metadata:
  tags: [Coding, Agent, Delegation, Background, Git, PR, Review]
---

# Coding Agent (always backgrounded)

Spawn workers with the **`process`** tool (`action=spawn`). Do not use a foreground one-shot path.
There is no `shell` tool parameter named `pty` or `background`.

- Codex / Pi / OpenCode: `process` `action=spawn` `pty=true`
- Claude Code: `process` `action=spawn` without PTY, flags `--print --permission-mode bypassPermissions`

Start the agent, keep the returned `session_id`, monitor with `process` `poll`/`log`, and require the worker to notify via `sessions_send` when it finishes.

```
process action=spawn pty=true cwd:~/project command:"codex exec 'Your prompt'"
process action=spawn cwd:~/project command:"claude --permission-mode bypassPermissions --print 'Your task'"
```

Wrong: `shell pty:true background:true ...`

### Process tool

| Action      | Description                                            |
| ----------- | ------------------------------------------------------ |
| `spawn`     | Start a background process; set `pty=true` for TUI CLIs |
| `list`      | List running/recent sessions                           |
| `poll`      | Status + recent output                                 |
| `log`       | Full output with offset/limit                          |
| `write`     | Raw stdin (no newline)                                 |
| `submit`    | stdin + newline                                        |
| `send_keys` | Key tokens (`C-c`, `Enter`, literal text)              |
| `kill`      | SIGTERM then SIGKILL                                   |

There is no `paste` action and no `send-keys` alias. Session id field is `session_id`.

---

## Mandatory Pattern

1. Capture a real `sessions_send` **target** (parent session id or routing key) before spawning.
2. Spawn the coding CLI with `process` immediately (`pty=true` for Codex/Pi/OpenCode).
3. Include that target in the worker prompt and require `sessions_send` on completion.
4. Monitor with `process` `action=log` / `poll` and `session_id`.
5. If the worker needs input or fails before notifying, handle that yourself. Do not rely on heartbeat.

If you do not have a trustworthy notification target, say so and do not claim that completion will notify the user automatically.

### Multi-hour issue-to-PR builds

1. Create or reuse the issue with `gh issue create` / `gh issue view`.
2. Spawn Codex in the background and include the issue URL, target repo, branch/base, expected PR output, required proof, and the `sessions_send` target.
3. Tell Codex to create a branch, implement, run checks, review until clean, and open a PR linked to the issue.
4. Return the issue URL and background `session_id` immediately.
5. Monitor with `process`. Abort with `process` `action=kill`.

Do not block the user on the issue creation or build loop.

---

## Notification

`sessions_send` takes **`target`** and **`message`** only.

```
sessions_send target:<parent-session-or-route> message:"Done: <brief summary>"
```

There is no `channel`, `account`, `reply_to`, or `thread_id` argument.

Append this to every worker prompt:

```text
When the task is completely finished, send exactly one completion message with sessions_send:
  target: <notifyTarget>
  message: Done: <brief summary>
If the task fails fatally, send exactly one failure message the same way.
Do not skip the completion/failure message.
```

---

## Quick Start

Codex needs a trusted git directory.

```
SCRATCH=$(mktemp -d) && git -C "$SCRATCH" init
process action=spawn pty=true cwd:$SCRATCH command:"codex exec 'Your prompt. When finished, sessions_send target:<notifyTarget> message:Done: <summary>.'"
```

Inject the completion snippet into every worker prompt; examples below omit it for brevity.

---

## Codex CLI

Default model comes from `~/.codex/config.toml`.

| Flag            | Effect                                   |
| --------------- | ---------------------------------------- |
| `exec "prompt"` | One-shot execution inside the worker CLI |
| `--full-auto`   | Sandboxed but auto-approves in workspace |
| `--yolo`        | No sandbox, no approvals                 |

```
process action=spawn pty=true cwd:~/project command:"codex exec --full-auto 'Build a dark mode toggle'"
process action=spawn pty=true cwd:~/project command:"codex --yolo 'Refactor the auth module'"
```

### Reviewing PRs

Never review PRs in the live project folder. Clone to a temp dir or use a worktree.

```
REVIEW_DIR=$(mktemp -d)
git clone https://github.com/user/repo.git $REVIEW_DIR
git -C $REVIEW_DIR fetch origin pull/130/head:pr-130 && git -C $REVIEW_DIR checkout pr-130
process action=spawn pty=true cwd:$REVIEW_DIR command:"codex review --base origin/main"
```

Or:

```
git worktree add /tmp/pr-130-review pr-130-branch
process action=spawn pty=true cwd:/tmp/pr-130-review command:"codex review --base main"
```

Batch:

```
git fetch origin '+refs/pull/*/head:refs/remotes/origin/pr/*'
process action=spawn pty=true cwd:~/project command:"codex exec 'Review PR #86. git diff origin/main...origin/pr/86'"
process action=spawn pty=true cwd:~/project command:"codex exec 'Review PR #87. git diff origin/main...origin/pr/87'"
process action=list
process action=log session_id:XXX
```

---

## Claude Code

```
process action=spawn cwd:~/project command:"claude --permission-mode bypassPermissions --print 'Your task'"
```

Do not use PTY for Claude Code here.

---

## OpenCode

```
process action=spawn pty=true cwd:~/project command:"opencode run 'Your task'"
```

---

## Pi Coding Agent

```
# Install: npm install -g @earendil-works/pi-coding-agent
process action=spawn pty=true cwd:~/project command:"pi 'Your task'"
process action=spawn pty=true cwd:~/project command:"pi -p 'Summarize src/'"
process action=spawn pty=true cwd:~/project command:"pi --provider openai --model gpt-5.6 -p 'Your task'"
```

---

## Parallel issue fixing with git worktrees

```
git worktree add -b fix/issue-78 /tmp/issue-78 main
git worktree add -b fix/issue-99 /tmp/issue-99 main
process action=spawn pty=true cwd:/tmp/issue-78 command:"pnpm install && codex --yolo 'Fix issue #78. Commit and push after review. sessions_send target:<notifyTarget> message:Done.'"
process action=spawn pty=true cwd:/tmp/issue-99 command:"pnpm install && codex --yolo 'Fix issue #99. sessions_send target:<notifyTarget> message:Done.'"
process action=list
```

---

## Rules

1. Always spawn in the background with `process`. Never a foreground one-shot.
2. Codex/Pi/OpenCode: `pty=true`. Claude Code: `--print --permission-mode bypassPermissions`, no PTY.
3. If the user asked for Codex, use Codex. Do not silently hand-code the patch.
4. Capture a real `sessions_send` target before spawn.
5. Monitor with `process` `log`/`poll`. Do not kill sessions just because they are slow.
6. Parallel is OK.
7. Never start Codex inside `~/.covo-agent`.
8. Never checkout review/fix branches in the live project directory.
9. Always inject the completion snippet before spawning.

## Progress updates

- One short message when you start: what is running and the `session_id`.
- Update only on milestone, question, error, or finish.
- If you kill a session, say so immediately.
- If the worker should self-notify with `sessions_send`, say that in the start update.

## Learnings

- PTY is required for Codex/Pi/OpenCode TUI CLIs.
- Codex needs a trusted git directory.
- `submit` sends input plus Enter; `write` does not.
- `sessions_send` is `target` + `message` only.
