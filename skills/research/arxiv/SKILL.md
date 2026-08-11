---
name: arxiv
description: "Search arXiv papers by keyword, author, category, or paper ID."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [arxiv, paper, research, academic, search]
  related_skills: [duckduckgo-search]
---

# arXiv — Paper Search

Search the arXiv repository for academic papers.

## Installation

```bash
pip install arxiv
```

## Usage

### Search by Keyword
```python
import arxiv

search = arxiv.Search(
    query="attention mechanism transformer",
    max_results=10,
    sort_by=arxiv.SortCriterion.Relevance,
)

for paper in search.results():
    print(f"{paper.title}")
    print(f"  Authors: {', '.join(a.name for a in paper.authors[:3])}")
    print(f"  Date: {paper.published.date()}")
    print(f"  URL: {paper.entry_id}")
    print(f"  PDF: {paper.pdf_url}")
    print(f"  Summary: {paper.summary[:200]}...\n")
```

### Specific Paper by ID
```python
paper = next(arxiv.Search(id_list=["1706.03762"]).results())
print(paper.title)  # "Attention Is All You Need"
```

### Filter by Category
```python
# cs.AI = AI, cs.CL = NLP, cs.LG = ML, stat.ML = stats ML
search = arxiv.Search(
    query="diffusion models",
    max_results=5,
    sort_by=arxiv.SortCriterion.SubmittedDate,
)

# Filter by category
papers = [p for p in search.results() 
          if any(c.startswith("cs.") for c in p.categories)]
```

### Advanced Query
```python
# Search title and abstract
search = arxiv.Search(
    query='ti:"reinforcement learning" AND au:"silver"',
    max_results=5,
)
```

### Download PDF
```python
paper = next(search.results())
paper.download_pdf(dirpath="./papers", filename=f"{paper.get_short_id()}.pdf")
```

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
