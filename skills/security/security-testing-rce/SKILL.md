---
name: security-testing-rce
description: Verify command or code execution with minimal, non-destructive proof.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# Code Execution Verification

Trace input into process launch, evaluators, interpreters, template engines, build hooks, or plugin loaders. In source, cite the exact sink and transformations between input and execution.

Use a harmless deterministic marker that demonstrates the execution context without altering persistent state. Error messages, command-like reflection, or a dangerous function call unreachable from input are not proof.

Confirm only with repeatable execution attributable to the supplied input. Record the command or expression, output marker, effective identity, source location, exchange id, and cleanup.