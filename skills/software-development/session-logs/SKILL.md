---
name: session-logs
description: "Search and analyze past session logs to find previous discussions, decisions, and context."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [jq, rg]
metadata:
  tags: [session, history, search, log, context, recall]
  related_skills: [codebase-inspection, systematic-debugging]
---

# Session Logs — Search Past Conversations

Search through historical session JSONL log files to find what was discussed
before, prior decisions made, and context from related conversations.

## When to Use

- "what did we discuss last time?"
- "find the conversation about X"
- "what was the decision on Y?"
- "show me past work on this project"
- User references a past session that isn't in current context

## Process

### Step 1 — Locate Session Logs

Session logs are stored as JSONL files:
```bash
ls ~/.covo-agent/sessions/*.jsonl
```

Each file contains one JSON object per line (turn):
```json
{"role": "user", "content": "...", "timestamp": "2026-06-09T15:04:05Z"}
{"role": "assistant", "content": "...", "timestamp": "..."}
```

### Step 2 — Search by Keyword

```bash
# Full-text search across all sessions
rg -l "keyword" ~/.covo-agent/sessions/

# Search with context (2 lines before/after)
rg -C 2 "keyword" ~/.covo-agent/sessions/

# Case-insensitive
rg -i "keyword" ~/.covo-agent/sessions/
```

### Step 3 — Parse and Filter with jq

```bash
# Extract all user messages containing a keyword
jq -r 'select(.role=="user" and (.content|test("keyword";"i"))) | .content' \
  ~/.covo-agent/sessions/*.jsonl

# Find conversations from a specific date
jq -r 'select(.timestamp | startswith("2026-06-09")) | 
  "[\(.role)] \(.content[:120])"' \
  ~/.covo-agent/sessions/*.jsonl

# List session summaries (first user message + last assistant message)
for f in ~/.covo-agent/sessions/*.jsonl; do
  echo "--- $(basename $f) ---"
  jq -r 'select(.role=="user") | .content' "$f" | head -1
done
```

### Step 4 — Decision Extraction

Find decisions made in past sessions:
```bash
# Search for decision markers
rg -i "(decided|agreed|concluded|settled on|going with|let's use|choose)" \
  ~/.covo-agent/sessions/
```

### Step 5 — Present Results

Format findings clearly:
1. Session file and date
2. Key topics discussed
3. Decisions made
4. Action items still open
5. Context relevant to current task
