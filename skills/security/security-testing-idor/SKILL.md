---
name: security-testing-idor
description: Verify object-level authorization and tenant isolation failures with differential evidence.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# Object Authorization Verification

Build a two-principal matrix: same-role users for horizontal checks, lower/higher roles for vertical checks, and separate tenants where available. Enumerate object references in paths, query strings, JSON, headers, GraphQL variables, exports, jobs, and nested relationship fields.

Change one identity or object reference at a time. Compare status, body shape, object ownership, and side effects. A guessed identifier, public object, or intentionally shared resource is not proof.

Confirm only when one principal can read or mutate another principal's protected object without the intended authorization. Save both baseline and unauthorized exchanges, affected object type, principal roles, and tenant boundaries.