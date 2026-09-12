---
name: grounded-citations
description: "Use when an answer rests on fetched pages or papers: numbered citations from retrieval, never URLs from memory."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [research, citations, sources, web, reports]
  related_skills: [arxiv, duckduckgo-search, scrapling, llm-wiki]
---

# Grounded Citations

Every claim taken from an outside source gets an inline `[n]` and a `Sources`
list. Numbers map to URLs (or arXiv ids) you **actually fetched this turn**.
Do not emit a link you did not retrieve.

Skip this skill for incidental lookups mid-coding, casual chat, or fiction.

## When to Use

- Research, comparisons, news, "current state of X"
- Written artifacts (markdown reports, briefs) that paraphrase the web
- Multi-source synthesis where sources may conflict

## Procedure

1. Search or open sources with `web_search` / `web_fetch` / `arxiv` / `scrapling`.
2. Keep a ledger while you work (scratch notes, not shown unless useful):

   | n | url or arxiv id | what you actually read |
   |---|-----------------|------------------------|
   | 1 | https://...     | quote or paraphrase    |

3. Write the answer. Attach `[n]` to claims that came from a row in that ledger.
4. End with:

   ```
   Sources:
   [1] https://...
   [2] https://arxiv.org/abs/...
   ```

5. If a claim is from model knowledge, not a fetch, mark it `[unverified]` or
   drop it. Do not backfill a plausible URL.

## Rules

- One URL → one number for the whole answer. Do not renumber mid-draft.
- Quotes must appear in the fetched text. If `web_fetch` failed or was empty,
  do not cite that URL.
- Prefer primary sources over aggregators when both were fetched.
- Conflicting sources: state both and cite both; do not pick a winner silently.
- Do not dump raw page text into the user reply unless they asked.

## When NOT to Use

- Syntax/version lookup while editing code
- Commands, file paths, or repo facts you just read with `read`/`grep`
- Creative writing

## Verification

- [ ] Every `[n]` has a Sources line
- [ ] Every Sources URL was fetched this turn
- [ ] Unverified model knowledge is labeled or omitted
