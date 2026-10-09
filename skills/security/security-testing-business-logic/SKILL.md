---
name: security-testing-business-logic
description: Verify workflow and invariant violations with state-aware evidence.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# Business Logic Verification

Write the intended state machine before testing: actors, prerequisites, transitions, limits, and irreversible effects. Compare the normal sequence with skipped, reordered, repeated, stale, or cross-account transitions.

Focus on pricing, credits, approvals, invitations, refunds, entitlement changes, and one-time actions. Unexpected UI behavior is not proof if the authoritative state remains valid.

Confirm only when the server accepts an invalid state transition or violates a stated invariant. Record prerequisites, exact sequence, final state, affected actor, and cleanup.