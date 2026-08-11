---
name: llm-wiki
description: "Build and query interlinked markdown knowledge bases inspired by Karpathy's LLM Wiki."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [wiki, knowledge-base, markdown, interlinked, encyclopedia]
  related_skills: [qmd, code-wiki]
---

# LLM Wiki — Interlinked Knowledge Base

Build and query a personal knowledge base structured as interlinked
markdown files, inspired by Andrej Karpathy's LLM Wiki approach.

## When to Use

- "build a knowledge base for..."
- "create a personal wiki on topic X"
- "explain this concept in a wiki format"
- "link all my notes together"

## Wiki Structure

```
wiki/
├── README.md          # Index and navigation
├── topics/
│   ├── machine-learning/
│   │   ├── README.md  # ML overview
│   │   ├── neural-networks.md
│   │   ├── transformers.md
│   │   └── attention.md
│   ├── programming/
│   │   ├── README.md
│   │   ├── rust.md
│   │   └── async-python.md
│   └── ...
├── concepts/          # Atomic concepts
│   ├── gradient-descent.md
│   ├── backpropagation.md
│   └── ...
└── glossary.md        # Term definitions
```

## Entry Format

Each wiki entry follows this template:

```markdown
# Topic Name

**Tags:** #tag1 #tag2 #tag3
**Related:** [[other-topic]], [[another-concept]]

## TL;DR
One-sentence summary of what this is and why it matters.

## Overview
Explain the concept in plain language. Start broad, then go deep.

## Key Ideas
- Point 1: ...
- Point 2: ...
- Point 3: ...

## Examples
Provide concrete examples. Code snippets, diagrams, real-world analogies.

## When to Use
Practical scenarios where this applies.

## Common Pitfalls / Gotchas
What beginners get wrong, edge cases, limitations.

## See Also
- [[related-topic]] — short description of relationship
- [[resource-link]] — external reference

## Sources
- [Paper/Article Title](url)
- [Documentation](url)
```

## Building the Wiki

### Step 1 — Structure First
1. Identify the main topics (3-7 top-level categories)
2. For each topic, list 5-10 subtopics
3. Create the directory structure with README.md files

### Step 2 — Write Core Entries
1. Write one entry at a time
2. Start with the TL;DR — if you can't summarize in one sentence, narrow the scope
3. Add concrete examples before abstract explanations
4. Link to related entries immediately (create stubs if they don't exist yet)

### Step 3 — Cross-Link
1. Re-read existing entries and add `[[links]]` to newly created pages
2. Create a glossary of terms
3. Add "See Also" sections with one-sentence relationship descriptions

### Step 4 — Maintain
1. Review entries monthly for staleness
2. Add new examples as you encounter them
3. Refactor entries that grow too large (>500 lines)

## Querying the Wiki

```bash
# Search with ripgrep
rg "backpropagation" wiki/ -l

# Full-text with context
rg -C 3 "gradient descent" wiki/

# Find all links to a topic
rg "\[\[transformers\]\]" wiki/

# List all tags
rg "#[a-z-]+" wiki/ -o --no-filename | sort | uniq -c | sort -rn

# Find orphan pages (not linked from anywhere)
for f in wiki/**/*.md; do
  name=$(basename "$f" .md)
  count=$(rg "\[\[$name\]\]" wiki/ -l | wc -l)
  [ $count -eq 0 ] && echo "Orphan: $f"
done
```

## Wiki Maintenance Script

```bash
#!/bin/bash
# wiki-stats.sh — print knowledge base statistics

echo "=== Wiki Statistics ==="
echo "Total pages: $(find wiki/ -name '*.md' | wc -l)"
echo "Total topics: $(find wiki/topics/ -mindepth 1 -maxdepth 1 -type d | wc -l)"
echo ""
echo "Top tags:"
rg "#[a-z-]+" wiki/ -o --no-filename | sort | uniq -c | sort -rn | head -10
echo ""
echo "Most linked:"
rg "\[\[([^\]]+)\]\]" wiki/ -o --no-filename | sed 's/\[\[//;s/\]\]//' | sort | uniq -c | sort -rn | head -10
echo ""
echo "Orphan pages:"
for f in wiki/**/*.md; do
  name=$(basename "$f" .md)
  rg -q "\[\[$name\]\]" wiki/ || echo "  $f"
done
```
