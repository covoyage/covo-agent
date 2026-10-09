---
name: security-testing-weak-passwords
description: Verify password-policy and credential-defense weaknesses with bounded account-safe checks.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# Password Defense Verification

Document password creation policy, rate limits, lockout, MFA, breached-password checks, recovery, and monitoring. Use designated test accounts and a bounded attempt budget.

A weak written policy is not the same as an exploitable login path. Distinguish acceptance of a weak password from successful credential guessing, and avoid account denial-of-service.

Confirm only with reproducible policy acceptance or controlled authentication evidence. Record the attempt budget, rate-limit behavior, test-account scope, successful condition, and cleanup.