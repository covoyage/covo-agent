---
name: security-testing-llm-prompt-injection
description: Verify instruction-boundary failures in LLM applications and agent tool flows.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# LLM Prompt Injection Verification

Map trust boundaries among user input, retrieved documents, memory, tool output, system instructions, and tool arguments. Define the prohibited action or protected datum before testing.

Textual compliance alone is not impact. Distinguish model output from actual tool execution, data disclosure, persistent memory mutation, or cross-user effects. Use benign canaries and reversible actions.

Confirm only when untrusted content crosses a defined boundary and causes the prohibited effect. Record the source, instruction path, model output, tool trace, affected policy, and repeatability.