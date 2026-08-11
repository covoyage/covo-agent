---
name: duckduckgo-search
description: "Free web search via DuckDuckGo: text, news, images, videos — no API key needed."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [search, duckduckgo, web, free, news]
  related_skills: [web_search, arxiv]
---

# DuckDuckGo Search — Free Web Search

Search the web using DuckDuckGo — completely free, no API key required.
Use as a fallback when web_search tool is unavailable.

## Installation

```bash
pip install duckduckgo_search
```

## Usage

### Text Search
```python
from duckduckgo_search import DDGS

with DDGS() as ddgs:
    results = list(ddgs.text("Python async programming", max_results=10))
    for r in results:
        print(f"{r['title']}")
        print(f"  {r['href']}")
        print(f"  {r['body'][:150]}...\n")
```

### News Search
```python
with DDGS() as ddgs:
    news = list(ddgs.news("AI agents", max_results=5, timelimit="w"))
    for n in news:
        print(f"{n['title']}")
        print(f"  Source: {n['source']}")
        print(f"  Date: {n['date']}")
        print(f"  URL: {n['url']}\n")
```

### Image Search
```python
with DDGS() as ddgs:
    images = list(ddgs.images("mountain landscape", max_results=5))
    for img in images:
        print(f"{img['title']} — {img['image']}")
```

### Video Search
```python
with DDGS() as ddgs:
    videos = list(ddgs.videos("Python tutorial", max_results=3))
    for v in videos:
        print(f"{v['title']} — {v['content']}")
```

### CLI Usage
```bash
# Install CLI
pip install duckduckgo-search[cli]

# Search
ddgs text "quantum computing" --max-results 5
ddgs news "stock market" --max-results 5 --time w  # w=week, m=month, y=year
ddgs images "cat" --max-results 5
```

### Region-Specific
```python
with DDGS() as ddgs:
    # Search in Chinese
    results = list(ddgs.text("人工智能", region="cn-zh", max_results=5))
    
    # Region codes: us-en, cn-zh, jp-jp, kr-kr, de-de, fr-fr, etc.
```

## Rate Limits

DuckDuckGo is free but rate-limited. Add delays between requests:
```python
import time
for query in queries:
    results = list(ddgs.text(query, max_results=3))
    time.sleep(2)  # Be respectful
```
