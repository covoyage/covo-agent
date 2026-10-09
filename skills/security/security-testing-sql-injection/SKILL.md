---
name: security-testing-sql-injection
description: Verify SQL injection using controlled behavioral differentials and reproducible evidence.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# SQL Injection Verification

Map every input to its likely query context: string, numeric, identifier, sort, pagination, or stored value. Establish a stable baseline before testing syntax, boolean, timing, or error differentials.

Change one condition at a time and repeat enough times to rule out cache, jitter, and generic server errors. Scanner output or a single slow response is only a lead.

Confirm only with a repeatable database-controlled differential or retrieved data that cannot be explained by normal application behavior. Record the exact request pair, response distinction, database clues, and affected parameter.