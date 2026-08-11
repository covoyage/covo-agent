---
name: qmd
description: "Search personal knowledge bases locally: BM25 + vector search + LLM reranking."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [knowledge-base, search, bm25, vector, semantic, local, notes]
  related_skills: [llm-wiki, codebase-inspection]
---

# QMD — Local Knowledge Base Search

Search personal notes, docs, and meeting transcripts locally using a
hybrid retrieval engine (BM25 + vector + LLM reranking).

## Installation

```bash
pip install qmd
```

## When to Use

- "search my notes for..."
- "find that discussion about..."
- "what did I write about X?"
- "search my local docs"

## Index Your Content

```bash
# Index a directory
qmd index ~/Documents/notes
qmd index ~/meetings --type transcripts

# Index with custom name
qmd index ~/projects/reports --name "reports"

# List indexed sources
qmd list
```

## Search

```bash
# Quick search
qmd search "machine learning pipeline architecture"

# With options
qmd search "project timeline" --limit 10 --source reports

# Output as JSON
qmd search "api design" --json

# Open results in browser
qmd search "deployment guide" --web
```

## Python API

```python
from qmd import QMD

qm = QMD()

# Search across all sources
results = qm.search("database migration strategy", limit=5)
for r in results:
    print(f"Score: {r.score:.2f} — {r.source}")
    print(f"  {r.content[:200]}...\n")

# Search specific source
results = qm.search("code review", source="notes")

# Hybrid options
results = qm.search(
    "authentication flow",
    use_bm25=True,    # BM25 keyword search
    use_vector=True,  # Semantic/vector search
    rerank=True,      # LLM reranking for relevance
)
```

## MCP Integration

QMD can also be used as an MCP server:
```json
{
  "mcpServers": {
    "qmd": {
      "command": "qmd",
      "args": ["mcp"]
    }
  }
}
```

## Similar Approaches (No Install)

If qmd is not available, build a simple local search:

```bash
# Ripgrep for full-text search
rg "keyword" ~/Documents/notes/

# With context
rg -C 2 "keyword" ~/Documents/

# Fuzzy search filenames
find ~/Documents/ -name "*keyword*"

# grep recursively with context
grep -r -n -C 3 "search term" ~/Documents/notes/
```
