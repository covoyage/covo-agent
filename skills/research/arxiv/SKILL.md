---
name: arxiv
description: "Search arXiv papers by keyword, author, category, or paper ID."
version: 1.1.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [arxiv, paper, research, academic, search]
  related_skills: [duckduckgo-search, grounded-citations]
---

# arXiv — Paper Search

Search the arXiv repository for academic papers. Prefer the bundled stdlib script (no pip).

## Script

```bash
skill_script {"action":"run","name":"arxiv","script":"search_arxiv.py","args":["search","attention mechanism","--max","10"]}
skill_script {"action":"run","name":"arxiv","script":"search_arxiv.py","args":["id","1706.03762"]}
```

Or via python3:

```bash
python3 scripts/search_arxiv.py search "attention mechanism" --max 10
python3 scripts/search_arxiv.py id 1706.03762
python3 scripts/search_arxiv.py search 'ti:"reinforcement learning" AND au:silver'
```

JSON on stdout. No `pip install arxiv`.

Query language (Atom API `search_query`):

- `all:transformer`
- `ti:"reinforcement learning" AND au:silver`
- `cat:cs.LG`

## Categories

| Code | Field |
|------|-------|
| cs.AI | Artificial Intelligence |
| cs.CL | Computation and Language (NLP) |
| cs.CV | Computer Vision |
| cs.LG | Machine Learning |
| cs.SE | Software Engineering |
| cs.PL | Programming Languages |
| stat.ML | Machine Learning (Statistics) |
| quant-ph | Quantum Physics |
| q-fin | Quantitative Finance |
