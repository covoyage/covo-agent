---
name: sherlock
description: "OSINT username search: find accounts across 400+ social networks."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [osint, username, search, social-media, reconnaissance]
  related_skills: [domain-intel]
---

# Sherlock — Username OSINT

Search for a username across 400+ social networks and websites.

## Installation

```bash
pip install sherlock-project
# Or: git clone https://github.com/sherlock-project/sherlock
```

## Usage

```bash
# Search username
sherlock username

# Output to file
sherlock username --output results/

# Search multiple usernames
sherlock user1 user2 user3

# Filter by country
sherlock username --site "github.com" "twitter.com"

# JSON output
sherlock username --json results.json

# Quiet mode (results only)
sherlock username --quiet

# Timeout per site
sherlock username --timeout 10

# Tor proxy
sherlock username --tor
```

## Python API

```python
from sherlock import sherlock

results = sherlock.sherlock("username")
for site, data in results.items():
    if data["exists"] == "yes":
        print(f"{site}: {data['url_user']}")
```
