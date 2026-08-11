---
name: humanizer
description: "Strip AI-isms from text: remove passive voice, long sentences, and formulaic patterns."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [writing, humanize, tone, style, editing]
  related_skills: [summarize]
---

# Humanizer — Natural Text Rewriting

Remove AI telltale signs from generated text. Apply when the user wants
output that sounds natural, conversational, and human-written.

## When to Use

- "make this sound less AI"
- "rewrite in natural language"
- "humanize this text"
- "this sounds robotic, fix it"
- Any output going to external readers (emails, docs, blog posts)

## Rules to Apply

### Remove AI Fingerprints
- ❌ "It is worth noting that..." → Delete or restate directly
- ❌ "In conclusion" / "To summarize" → Just state the conclusion
- ❌ "Moreover" / "Furthermore" / "Additionally" → Remove, chain naturally
- ❌ "It is important to understand that..." → "Here's why:"
- ❌ "This allows for..." → "This lets you..."
- ❌ "In order to" → "To"
- ❌ "Due to the fact that" → "Because"

### Sentence Structure
- **Vary length:** Mix short (3-8 words) with medium (10-18 words). No paragraphs of all-long sentences
- **Start differently:** Don't begin consecutive sentences with the same word
- **Prefer active:** "We built the feature" not "The feature was built"
- **Cut hedging:** Remove "potentially", "generally", "typically", "arguably"
- **One idea per sentence:** Break compound sentences at natural joints

### Word Choice
- **Concrete over abstract:** "It took 3 hours" not "It required significant time"
- **Simple over formal:** "use" not "utilize", "show" not "demonstrate", "help" not "facilitate"
- **Contractions ok:** "it's" not "it is", "don't" not "do not" (in casual contexts)
- **No buzzwords:** Strip "synergy", "leverage", "ecosystem", "game-changer"

### Paragraph Flow
- Lead with the point, not the setup
- Short paragraphs (2-4 sentences) for readability
- Transition naturally: "That means...", "Here's why...", "The catch is..."
- End paragraphs with a punch, not a fade

## Before/After Example

**Before (AI):**
> It is important to note that the implementation of this feature will allow
> for significant improvements in the overall workflow. Moreover, users will
> be able to leverage the new capabilities to enhance their productivity
> metrics. Additionally, the system architecture has been designed to
> facilitate seamless integration with existing tooling.

**After (Human):**
> This feature makes your workflow faster. Pipe commands together, skip the
> boilerplate, and get results in one step. We designed it to plug into your
> existing tools with zero config.

## Delivery

Apply the rules and present the rewritten text. Show a short summary of
what was changed if the user asks.
