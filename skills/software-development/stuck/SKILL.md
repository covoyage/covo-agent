---
name: stuck
description: "Diagnose frozen, stuck, or slow covo-agent sessions. Scans for problematic processes, high CPU/memory usage, hung subprocesses, and debug logs."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos]
metadata:
  tags: [debugging, diagnostics, troubleshooting, processes, performance]
---

# Stuck Session Diagnostics

Diagnose frozen, stuck, or very slow covo-agent sessions on this machine. Investigate and present a diagnostic report.

## When to Use

- User says "my session is frozen", "it's stuck", "why is it slow?", "diagnose this"
- Agent seems unresponsive for an extended period
- High CPU or memory usage reported by the system

## What to Look For

Signs of a stuck session:

- **High CPU (>=90%) sustained** — likely an infinite loop. Sample twice, 1-2s apart.
- **Process state `D` / `U`** (uninterruptible sleep) — I/O hang. Linux uses `D`, macOS uses `U`.
- **Process state `T`** (stopped) — user hit Ctrl+Z.
- **Process state `Z`** (zombie) — parent isn't reaping.
- **Very high RSS (>=4GB)** — possible memory leak.
- **State `S` with low CPU** — the most common hang: a hung API request to the model provider.

## Investigation Steps

### Step 1 — Find covo-agent processes

```bash
ps -xo pid=,pcpu=,rss=,etime=,state=,command= -ww | grep -E 'covo-agent|/covo\b' | grep -v grep
```

If a specific PID was given:
```bash
ps -p <pid> -o pid=,pcpu=,rss=,etime=,state=,command= -ww
```

### Step 2 — Check child processes

For suspicious PIDs, look at what children they spawned:

```bash
CHILDREN=$(pgrep -P <pid> | tr '\n' ',' | sed 's/,$//')
[ -n "$CHILDREN" ] && ps -p "$CHILDREN" -o pid=,ppid=,pcpu=,state=,etime=,command= -ww
```

Common hung children:
- `git` operations that hang on network or locks
- `node`/`python` subprocesses stuck in infinite loops
- Shell processes waiting on stdin

### Step 3 — Network connection check

If CPU is low and state is `S`, the most likely cause is a hung API request:

**macOS:**
```bash
lsof -nP -i -p <pid> 2>/dev/null | head -20
```

**Linux:**
```bash
ss -tnp 2>/dev/null | grep "pid=<pid>," || lsof -nP -i -p <pid> 2>/dev/null | head -20
```

Look for long-lived ESTABLISHED connections to model API hosts (openai.com, api.anthropic.com, dashscope.aliyuncs.com, etc.).

### Step 4 — Check session logs

covo-agent stores session data in `~/.covo-agent/sessions/`:

```bash
ls -lt ~/.covo-agent/sessions/ | head -5
```

Check the most recent session log for clues:
```bash
tail -n 100 ~/.covo-agent/sessions/<session-id>.jsonl
```

### Step 5 — Resource usage

```bash
# Memory
ps -p <pid> -o rss=,vsz= -ww

# Open file descriptors
lsof -p <pid> 2>/dev/null | wc -l

# Check if hitting fd limits
ulimit -n
```

### Step 6 — Optional stack dump

For truly frozen processes:

**macOS:**
```bash
sample <pid> 3 2>/dev/null | head -100
```

**Linux:**
```bash
# Requires appropriate permissions
pstack <pid> 2>/dev/null || gdb -p <pid> -batch -ex "thread apply all bt" 2>/dev/null | head -100
```

## Diagnostic Report Format

Present findings in this structure:

```
## Session Diagnostic Report

### Process Summary
- PID: <pid>
- CPU: <cpu%>
- Memory: <rss in MB/GB>
- State: <state>
- Uptime: <etime>

### Assessment
<brief diagnosis based on findings>

### Root Cause (if identified)
<most likely cause>

### Recommended Actions
1. <action 1>
2. <action 2>
```

## Common Root Causes

| Symptom | Likely Cause | Fix |
|---------|-------------|-----|
| State `S`, low CPU, ESTABLISHED connection | API request hung | Kill and restart session; check API key validity |
| High CPU, state `R` | Infinite loop in code | Kill process; review last code change |
| State `T` | Ctrl+Z sent to terminal | `fg` or `kill -CONT <pid>` |
| State `Z` | Parent process bug | Kill parent process |
| High RSS (>4GB) | Memory leak | Kill and restart; reduce batch sizes |
| Many `git` children | Git operations hung | Check network; `git gc` may help |
| State `U` (macOS) / `D` (Linux) | I/O hang | Check disk health; reboot may be needed |