---
name: security-testing-prototype-pollution
description: Verify prototype mutation and a reachable security-relevant effect in JavaScript systems.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# Prototype Pollution Verification

Trace recursive merges, path setters, query parsers, cloning, and object expansion from attacker input. Determine whether dangerous keys reach a shared prototype or only an isolated object.

Pollution without a reachable consumer is a lead, not impact. Identify a gadget that changes authorization, configuration, templating, request behavior, or code execution.

Confirm with a controlled property mutation plus a repeatable security effect. Record the source, mutation path, affected prototype scope, gadget, source locations, and runtime evidence.