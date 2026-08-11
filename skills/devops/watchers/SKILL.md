---
name: watchers
description: "Monitor RSS feeds, JSON APIs, and GitHub repos with deduplication via watermark tracking."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [monitor, rss, api, github, polling, automation, watch]
  related_skills: [cronjob, docker-management]
---

# Watchers — Automated Monitoring

Set up polling watchers for RSS feeds, JSON APIs, and GitHub repos.
Deduplicate results using watermark tracking.

## RSS Feed Watcher

```python
import feedparser, json, os, time
from datetime import datetime

WATERMARK_FILE = "~/.covo-agent/watcher_watermarks.json"

def load_watermarks():
    try:
        with open(os.path.expanduser(WATERMARK_FILE)) as f:
            return json.load(f)
    except:
        return {}

def save_watermarks(wm):
    os.makedirs(os.path.dirname(os.path.expanduser(WATERMARK_FILE)), exist_ok=True)
    with open(os.path.expanduser(WATERMARK_FILE), "w") as f:
        json.dump(wm, f, indent=2)

def watch_rss(url, name):
    wm = load_watermarks()
    last = wm.get(name, "1970-01-01")
    
    feed = feedparser.parse(url)
    new_items = []
    for entry in feed.entries:
        published = entry.get("published", entry.get("updated", ""))
        if published > last:
            new_items.append({
                "title": entry.title,
                "link": entry.link,
                "published": published,
            })
    
    if new_items:
        newest = max(e["published"] for e in new_items)
        wm[name] = newest
        save_watermarks(wm)
    
    return new_items

# Example
items = watch_rss("https://blog.example.com/feed.xml", "example_blog")
for item in items:
    print(f"NEW: {item['title']} — {item['link']}")
```

## JSON API Watcher

```python
import requests

def watch_json(url, name, id_field="id"):
    wm = load_watermarks()
    last_id = wm.get(name)
    
    resp = requests.get(url)
    data = resp.json()
    
    items = data if isinstance(data, list) else data.get("items", data.get("results", []))
    new_items = [i for i in items if str(i.get(id_field)) != last_id]
    
    if new_items:
        wm[name] = str(new_items[0][id_field])
        save_watermarks(wm)
    
    return new_items
```

## GitHub Repo Watcher

```bash
#!/bin/bash
# Watch GitHub releases
REPO="owner/repo"
LASTMOD=$(curl -s "https://api.github.com/repos/$REPO/releases/latest" | jq -r '.tag_name')

if [ "$LASTMOD" != "$(cat ~/.covo-agent/gh_$REPO 2>/dev/null)" ]; then
    echo "New release: $LASTMOD"
    echo "$LASTMOD" > ~/.covo-agent/gh_$REPO
fi
```

## Cron Integration

```bash
# Run every 30 minutes
*/30 * * * * python3 ~/.covo-agent/watchers/check_feeds.py
```

## Loop Mode

```python
import time

def watch_loop(interval=300):
    """Watch continuously, check every N seconds."""
    while True:
        items = watch_rss("https://feed.example.com/rss", "feed")
        for item in items:
            print(f"[{datetime.now()}] {item['title']}")
        time.sleep(interval)
```
