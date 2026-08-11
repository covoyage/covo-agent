---
name: model-usage
description: "Track LLM token usage and cost: per-model breakdowns, session totals, and trends."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [cost, tokens, usage, tracking, model, budget]
  related_skills: [plan]
---

# Model Usage — Token & Cost Tracking

Track LLM token usage and estimated costs across models and sessions.

## Python Summarizer

```python
import json, sys
from collections import defaultdict

def summarize_usage(log_path="~/.covo-agent/sessions/.usage.jsonl"):
    """Summarize token usage from JSONL log."""
    usage = defaultdict(lambda: {"input": 0, "output": 0, "cache_read": 0, "requests": 0})
    
    with open(log_path) as f:
        for line in f:
            if not line.strip(): continue
            entry = json.loads(line)
            model = entry.get("model", "unknown")
            usage[model]["input"] += entry.get("input_tokens", 0)
            usage[model]["output"] += entry.get("output_tokens", 0)
            usage[model]["cache_read"] += entry.get("cache_read_tokens", 0)
            usage[model]["requests"] += 1
    
    # Pricing (per 1M tokens, approximate)
    prices = {
        "gpt-4o": (2.50, 10.00), "gpt-4.1-mini": (0.40, 1.60),
        "claude-sonnet-4-20250514": (3.00, 15.00), "claude-haiku": (0.80, 4.00),
        "gemini-2.5-pro": (1.25, 10.00), "gemini-2.5-flash": (0.15, 0.60),
    }
    
    total_cost = 0
    for model, u in sorted(usage.items()):
        inp_price, out_price = prices.get(model, (0, 0))
        cost = (u["input"] / 1e6 * inp_price) + (u["output"] / 1e6 * out_price)
        total_cost += cost
        
        print(f"\n{model}")
        print(f"  Requests:  {u['requests']}")
        print(f"  Input:     {u['input']:,} tokens")
        print(f"  Output:    {u['output']:,} tokens")
        if u["cache_read"]:
            print(f"  Cache hit: {u['cache_read']:,} tokens")
        print(f"  Est cost:  ${cost:.4f}")
    
    print(f"\n{'='*40}")
    print(f"Total estimated cost: ${total_cost:.4f}")

if __name__ == "__main__":
    summarize_usage()
```

## Quick Check

```bash
# Simple grep for session token counts
grep "tokens" ~/.covo-agent/sessions/*.jsonl | tail -5

# Check current session usage via /status slash command
# Or call get_goal to see token budget
```

## Cost Comparison

```python
def compare_models(tokens_in, tokens_out):
    """Compare costs across models for a given token volume."""
    models = {
        "gpt-4.1-mini": (0.40, 1.60),
        "gpt-4o": (2.50, 10.00),
        "claude-haiku": (0.80, 4.00),
        "claude-sonnet-4": (3.00, 15.00),
        "gemini-2.5-flash": (0.15, 0.60),
        "gemini-2.5-pro": (1.25, 10.00),
    }
    
    for model, (inp, out) in models.items():
        cost = (tokens_in/1e6*inp) + (tokens_out/1e6*out)
        print(f"{model:20s}: ${cost:8.4f}")

compare_models(100000, 20000)
```

## Session Budget Monitoring

```python
# Check if approaching budget
BUDGET_USD = 10.00
current_cost = 4.57  # from get_goal or usage log
pct = current_cost / BUDGET_USD * 100

if pct > 80:
    print(f"⚠️ {pct:.0f}% of ${BUDGET_USD} budget used (${current_cost:.2f})")
elif pct > 50:
    print(f"📊 {pct:.0f}% of budget used")
```
