---
name: security-testing-deserialization
description: Verify unsafe object deserialization and attacker-controlled type or gadget activation.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# Deserialization Verification

Identify native object formats, polymorphic JSON, signed state, message queues, caches, and import formats. In source, trace attacker-controlled bytes to a deserializer and document enabled type resolution or gadget reachability.

Use a harmless marker action or deterministic parser-side effect. Library presence, serialized-looking data, or a stack trace alone does not establish exploitability.

Confirm only when attacker input controls object construction or invokes behavior beyond ordinary data parsing. Record the format, entry point, type path, runtime evidence, and source file and line when available.