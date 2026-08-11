---
name: obsidian
description: "Work with Obsidian vaults: read, search, create, edit notes, tasks, links, and properties."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [obsidian, notes, vault, markdown, knowledge-base]
  related_skills: [llm-wiki]
---

# Obsidian — Vault Management

Work with Obsidian vaults — read, search, create, and edit markdown
notes with full frontmatter and link support.

## Vault Discovery

```bash
# Find Obsidian vaults (macOS)
find ~/Library/Mobile\ Documents/iCloud~md~obsidian/Documents/ -name "*.obsidian" -maxdepth 1 -type d

# Common locations
echo ~/Documents/Vault
echo ~/Obsidian/Vault
```

## Read Notes

```bash
# List all notes
find /path/to/vault -name "*.md" -not -path "*/.trash/*" | head -20

# Read a note
cat "/path/to/vault/My Note.md"

# Search note content
rg "keyword" /path/to/vault/ -l

# Search with context
rg -C 2 "search term" /path/to/vault/
```

## Create & Edit Notes

```bash
# Create new note
cat > "/path/to/vault/New Note.md" << 'EOF'
---
tags: [tag1, tag2]
created: $(date +%Y-%m-%d)
---

# New Note

Content here.

## Related
- [[Other Note]]
EOF

# Append to existing note
cat >> "/path/to/vault/Daily.md" << 'EOF'

## $(date +%Y-%m-%d)

- [x] Task completed
- [ ] New task
EOF
```

## Frontmatter Operations

```bash
# Extract all tags
rg "^tags:" /path/to/vault/ -A0 | sort | uniq -c | sort -rn

# Find notes by tag
rg "^tags:.*tag-name" /path/to/vault/ -l

# Find notes with specific property
rg "^created: 2024" /path/to/vault/ -l

# Update frontmatter
python3 << 'EOF'
import re, sys, glob

vault = "/path/to/vault"
for f in glob.glob(f"{vault}/**/*.md", recursive=True):
    with open(f) as fp:
        content = fp.read()
    # Example: update created date
    content = re.sub(r'created: \d{4}-\d{2}-\d{2}', 'created: 2026-01-01', content)
    with open(f, "w") as fp:
        fp.write(content)
EOF
```

## Link Analysis

```bash
# Find backlinks to a note
note="Target Note"
rg "\[\[$note\]\]" /path/to/vault/ -l

# Find orphan notes (no incoming links)
for f in /path/to/vault/*.md; do
  name=$(basename "$f" .md)
  count=$(rg "\[\[$name\]\]" /path/to/vault/ -l | wc -l)
  [ $count -eq 0 ] && echo "Orphan: $name"
done

# Find broken links
rg "\[\[([^\]]+)\]\]" /path/to/vault/ -o --no-filename | \
  sed 's/\[\[//;s/\]\]//;s/|.*//' | sort -u | while read target; do
    [ ! -f "/path/to/vault/$target.md" ] && echo "Broken: $target"
  done
```

## Task Management

```bash
# Find all open tasks
rg "\- \[ \]" /path/to/vault/ -n

# Find completed tasks
rg "\- \[x\]" /path/to/vault/ -n

# Tasks with dates
rg "\- \[.\] .*📅" /path/to/vault/ -n

# Count tasks
echo "Open: $(rg '\- \[ \]' /path/to/vault/ -c | awk -F: '{s+=$2}END{print s}')"
echo "Done: $(rg '\- \[x\]' /path/to/vault/ -c | awk -F: '{s+=$2}END{print s}')"
```

## Vault Statistics

```bash
#!/bin/bash
vault="/path/to/vault"
echo "Notes: $(find $vault -name '*.md' | wc -l)"
echo "Tags:  $(rg '^tags:' $vault -o --no-filename | sed 's/tags: \[//;s/\]//' | tr ',' '\n' | sed 's/^ *//' | sort -u | wc -l)"
echo "Links: $(rg '\[\[[^\]]+\]\]' $vault -c | awk -F: '{s+=$2}END{print s}')"
echo "Tasks: $(rg '\- \[.\]' $vault -c | awk -F: '{s+=$2}END{print s}')"
```
