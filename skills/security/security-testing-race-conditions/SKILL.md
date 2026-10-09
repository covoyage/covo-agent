---
name: security-testing-race-conditions
description: Verify concurrency flaws in state transitions, limits, and one-time operations.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# Race Condition Verification

Model the state invariant first: balance, quota, uniqueness, ownership, inventory, redemption, or one-time token use. Establish sequential behavior before controlled concurrent attempts.

Synchronize requests, preserve identical prerequisites, and repeat trials. A transient timeout or duplicate client retry is not proof unless the server commits an invalid final state.

Confirm only when concurrency violates the stated invariant. Record request count, synchronization method, final authoritative state, cleanup or rollback, and all relevant exchange ids.