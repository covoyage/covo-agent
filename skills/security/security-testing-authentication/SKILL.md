---
name: security-testing-authentication
description: Verify login, recovery, MFA, session, and identity-binding weaknesses.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# Authentication Verification

Model account creation, login, recovery, MFA enrollment and challenge, session renewal, logout, device trust, and identity-provider callbacks. Test each transition with separate principals and fresh sessions.

Look for account confusion, stale-session acceptance, weak recovery binding, factor substitution, session fixation, and inconsistent enforcement across APIs. User enumeration alone does not prove account compromise.

Confirm only when an attacker gains or retains authority beyond the intended authentication state. Record preconditions, principal identities, token or cookie lifecycle, final access, and exchange ids without retaining live secrets.