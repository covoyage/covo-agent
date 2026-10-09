---
name: security-testing-ssti
description: Verify server-side template expression evaluation and reachable impact.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# Server-Side Template Injection Verification

Determine the template engine and the exact input context before testing. Compare literal reflection against a harmless arithmetic or string expression and include a non-expression control.

Client-side interpolation, formatting syntax, and generic template errors are not proof. In source, trace the input into a compile or render API and cite the controlling location.

Confirm only with repeatable server-side evaluation. Record the expression, evaluated output, control response, engine evidence, sandbox boundary, and exchange id.