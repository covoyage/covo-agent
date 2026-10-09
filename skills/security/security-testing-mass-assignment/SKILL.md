---
name: security-testing-mass-assignment
description: Verify unauthorized binding of writable object fields.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# Mass Assignment Verification

Compare documented fields with serialized model fields, update DTOs, GraphQL inputs, and nested relationships. Prioritize role, owner, tenant, status, price, credit, approval, and capability fields.

Submit one additional field at a time and read the authoritative object through an independent endpoint. Echoing an unknown property is not proof if it is discarded before persistence.

Confirm only when an unauthorized field is persisted or changes behavior. Record the baseline object, submitted field, resulting state, principal privileges, and exchanges.