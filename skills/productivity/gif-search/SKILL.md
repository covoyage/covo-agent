---
name: gif-search
description: "Search and download GIFs from Tenor and GIPHY via curl + python."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [curl, python3]
metadata:
  tags: [gif, search, tenor, giphy, download, media, fun]
---

# GIF Search

Search and download GIFs using Tenor (requires a free API key) or GIPHY.

## Tenor Search

```bash
# Set TENOR_API_KEY env var (get a free key from https://developers.google.com/tenor/guides/quickstart)
python3 << 'EOF'
import urllib.request, json, sys, os

query = sys.argv[1] if len(sys.argv) > 1 else "cat"
key = os.environ.get("TENOR_API_KEY", "")
url = f"https://tenor.googleapis.com/v2/search?q={query}&key={key}&limit=10&media_filter=gif"

with urllib.request.urlopen(url) as resp:
    data = json.loads(resp.read())

for i, result in enumerate(data["results"]):
    gif = result["media_formats"]["gif"]
    print(f"{i+1}. {result['content_description']}")
    print(f"   URL: {gif['url']}")
    print(f"   Size: {gif['size']} bytes")
    print()
EOF
```

## Download GIF

```bash
# Download by number from search results
python3 << 'EOF'
import urllib.request, json, sys, os

query = "happy dance"
key = os.environ.get("TENOR_API_KEY", "")
url = f"https://tenor.googleapis.com/v2/search?q={query}&key={key}&limit=5&media_filter=gif"

with urllib.request.urlopen(url) as resp:
    data = json.loads(resp.read())

os.makedirs("gifs", exist_ok=True)
for i, result in enumerate(data["results"][:3]):
    gif_url = result["media_formats"]["gif"]["url"]
    path = f"gifs/{i+1}_{query.replace(' ', '_')}.gif"
    urllib.request.urlretrieve(gif_url, path)
    print(f"Downloaded: {path}")
EOF
```

## GIPHY (requires API key)

```bash
# Set GIPHY_API_KEY env var
curl "https://api.giphy.com/v1/gifs/search?api_key=$GIPHY_API_KEY&q=cat&limit=5"
```

## Extract Stills from GIFs

```bash
# Extract all frames
ffmpeg -i input.gif frames/frame_%04d.png

# First frame as preview
ffmpeg -i input.gif -vframes 1 preview.jpg

# GIF info
ffprobe -v quiet -print_format json -show_streams input.gif | python3 -c "
import json,sys
d=json.load(sys.stdin)
s=d['streams'][0]
print(f\"{s['width']}×{s['height']}, {s['nb_frames']} frames, {float(s['duration']):.1f}s\")
"
```
