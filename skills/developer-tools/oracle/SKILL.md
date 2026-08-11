---
name: oracle
description: "Second-model code review: send code to another LLM for independent analysis and feedback."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [code-review, oracle, second-opinion, review, debug, refactor]
  related_skills: [requesting-code-review, simplify-code]
---

# Oracle — Second-Model Code Review

Get an independent analysis from a second model (different from your
primary agent). Use for architecture decisions, tricky bugs, or
design validation where a fresh perspective helps.

## When to Use

- "get a second opinion on this code"
- "ask another model to review this"
- "compare what two models think"
- "debug this with a fresh pair of eyes"
- "review my architecture decision"

## Setup

```bash
pip install oracle-ai
# Or direct API usage
```

## Using sessions_spawn

The simplest approach — spawn a child agent with a different model:

```python
# In agent tool call:
{
  "action": "spawn",
  "task": "Review this code for bugs and design issues:\n\n<code>",
  "model": "gpt-4o",         # Different model
  "provider": "openai",       # Different provider
  "role": "leaf",
  "toolsets": ["coding"],     # Read-only tools
}
```

## Structured Oracle Prompt

When asking for a second opinion, use this prompt template:

```
You are an independent code reviewer. Review the following code with
fresh eyes. Be critical and thorough.

## Code to Review
```python
[code here]
```

## Review Angles
1. **Correctness:** Are there bugs, edge cases missed, or incorrect logic?
2. **Security:** Any vulnerabilities, injection risks, or unsafe operations?
3. **Performance:** Bottlenecks, N+1 queries, unnecessary allocations?
4. **Design:** Is the architecture sound? Are the abstractions right?
5. **Alternatives:** Is there a better approach? What would you do differently?

## Output Format
For each issue found:
- **Severity:** critical / major / minor
- **Location:** line or function
- **Problem:** what's wrong
- **Fix:** how to resolve it
```

## Comparing Opinions

```python
def oracle_compare(primary_response, oracle_response, context=""):
    """Compare what primary and oracle said."""
    print("=== PRIMARY AGENT ===")
    print(primary_response[:500])
    
    print("\n=== ORACLE (Second Opinion) ===")
    print(oracle_response[:500])
    
    print("\n=== KEY DIFFERENCES ===")
    # Simple diff — real analysis by the agent
    if "alternative" in oracle_response.lower():
        print("→ Oracle suggests alternative approaches")
    if "bug" in oracle_response.lower() and "bug" not in primary_response.lower():
        print("→ Oracle found bugs that primary missed")
    if "security" in oracle_response.lower():
        print("→ Oracle flagged security concerns")
```

## When NOT to Use Oracle

- **Simple questions** — overkill for one-liners
- **When you already have high confidence** — trust your judgment
- **Cost-sensitive scenarios** — spawning a second model doubles cost
- **Time-critical tasks** — adds latency from parallel agent execution
