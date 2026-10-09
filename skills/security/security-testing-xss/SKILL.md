---
name: security-testing-xss
description: Verify reflected, stored, and DOM cross-site scripting in the actual browser execution context.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# Cross-Site Scripting Verification

Trace input from source to HTML, attribute, URL, script, CSS, template, or DOM sink. Account for encoding, sanitization, browser parsing, CSP, and whether execution occurs in a victim-relevant origin.

Reflection is not execution. Validate in a browser with a harmless observable effect and distinguish server-rendered, stored, and client-side paths. For stored cases, verify through a separate viewing flow.

Record the source, sink, context, rendered DOM evidence, relevant response headers, and exchange ids. Mark blocked or non-executable reflections as coverage, not confirmed findings.