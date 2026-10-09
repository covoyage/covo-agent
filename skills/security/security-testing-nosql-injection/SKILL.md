---
name: security-testing-nosql-injection
description: Verify operator and query-shape injection in document and key-value data stores.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# NoSQL Injection Verification

Identify inputs that may become selectors, operators, filters, projections, or aggregation expressions. Compare scalar, object, array, null, and duplicate-key representations while preserving all unrelated request fields.

Look for authentication bypass, widened result sets, changed counts, altered error classes, or controlled timing. Type coercion and validation errors alone do not prove query execution.

Confirm only with a repeatable semantic change attributable to attacker-controlled query structure. Store the baseline and changed exchanges plus the affected data boundary.