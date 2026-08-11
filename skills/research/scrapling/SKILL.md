---
name: scrapling
description: "Advanced web scraping: stealth HTTP, browser automation, Cloudflare bypass."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [scraping, web, stealth, cloudflare, spider, crawl]
  related_skills: [web_fetch, duckduckgo-search]
---

# Scrapling — Stealth Web Scraping

Scrape JavaScript-heavy or bot-protected pages using Scrapling's stealth
HTTP engine and optional browser fallback.

## Installation

```bash
pip install scrapling
```

## When to Use

- "scrape this page (it blocks normal requests)"
- "extract data from a JS-rendered site"
- "bypass bot detection"
- "crawl a website"

## Stealth HTTP

```python
from scrapling import Fetcher

f = Fetcher(auto_match=True)

# Fetch with stealth headers
page = f.get("https://example.com/products")
print(page.css("h1::text").get())

# Extract multiple items
for item in page.css(".product-card"):
    name = item.css(".name::text").get()
    price = item.css(".price::text").get()
    print(f"{name}: {price}")

# CSS + XPath + regex
page.css("div.content")  # CSS selector
page.xpath("//div[@class='content']")  # XPath
page.re(r"price: \$(\d+\.?\d*)")  # Regex extraction
```

## Browser Fallback (Playwright)

```bash
pip install scrapling[playwright]
playwright install chromium
```

```python
from scrapling import PlayWrightFetcher

f = PlayWrightFetcher()

# Full browser rendering
page = f.get("https://spa-site.com")
print(page.text)

# Interact with page
page = f.get("https://login-site.com")
page.click("#login-button")
page.type("#username", "user")
page.wait_for_selector(".dashboard")
```

## Cloudflare Bypass

```python
# Scrapling handles Cloudflare challenges automatically
page = f.get("https://cloudflare-protected-site.com")
# Just works — no extra config needed
```

## Spider/Crawler

```python
from scrapling import Fetcher

f = Fetcher()
visited = set()

def crawl(url, depth=0):
    if depth > 2 or url in visited:
        return
    visited.add(url)
    try:
        page = f.get(url)
        print(f"[{depth}] {page.css('title::text').get()}")
        for link in page.css("a[href]::attr(href)").getall():
            if link.startswith("/"):
                crawl(f"https://example.com{link}", depth + 1)
    except Exception as e:
        print(f"Error: {e}")

crawl("https://example.com")
```

## Data Export

```python
import json

items = []
for card in page.css(".product"):
    items.append({
        "name": card.css(".name::text").get(),
        "price": card.css(".price::text").get(),
        "link": card.css("a::attr(href)").get(),
    })

# Save as JSON
with open("products.json", "w") as f:
    json.dump(items, f, indent=2)

# Or CSV
import csv
with open("products.csv", "w", newline="") as f:
    writer = csv.DictWriter(f, fieldnames=["name", "price", "link"])
    writer.writeheader()
    writer.writerows(items)
```
