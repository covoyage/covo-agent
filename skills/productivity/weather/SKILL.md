---
name: weather
description: "Current weather and forecasts via wttr.in — no API key required."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [curl]
metadata:
  tags: [weather, forecast, climate, utility]
---

# Weather — Quick Forecast

Get current weather and forecasts using wttr.in, or fall back to
web_fetch for richer data.

## Quick Lookup

```bash
# Default: auto-detect location by IP
curl wttr.in

# Specific city
curl "wttr.in/Tokyo"
curl "wttr.in/London"

# US zip code
curl "wttr.in/94107"

# Metric units
curl "wttr.in/Beijing?u"

# One-line format
curl "wttr.in/Berlin?format=3"
# Output: Berlin: ☀️ +18°C

# JSON (v3)
curl "wttr.in/London?format=j1"

# Simplified
curl "wttr.in/Paris?format=%l:+%c+%t+%w+%h"
# %l=location %c=condition %t=temp %w=wind %h=humidity
```

## CLI Wrapper

```bash
weather() {
  local city="${1:-}"
  if [ -n "$city" ]; then
    curl -s "wttr.in/$city?format=3"
  else
    curl -s "wttr.in?format=3"
  fi
}

weather Tokyo
weather
```

## Forecast

```bash
# 3-day forecast
curl "wttr.in/Shanghai?format=3"

# Full forecast (ASCII art)
curl "wttr.in/New+York"

# JSON forecast data
curl "wttr.in/Sydney?format=j1" | python3 -c "
import json,sys
d=json.load(sys.stdin)
for day in d['weather'][:3]:
    print(f\"{day['date']}: {day['avgtempC']}°C, {day['hourly'][4]['weatherDesc'][0]['value']}\")
"
```

## Format Specifiers

| Code | Output |
|------|--------|
| `%l` | Location |
| `%c` | Weather condition emoji |
| `%C` | Condition text |
| `%t` | Temperature |
| `%f` | Feels like |
| `%w` | Wind |
| `%h` | Humidity |
| `%p` | Precipitation |
| `%P` | Pressure |
| `%m` | Moon phase |
| `%M` | Moon day |
