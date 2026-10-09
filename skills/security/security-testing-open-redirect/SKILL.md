---
name: security-testing-open-redirect
description: Verify attacker-controlled navigation across URL parsing and allowlist boundaries.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# Open Redirect Verification

Find login returns, logout targets, OAuth callbacks, deep links, and forwarding endpoints. Test parser boundaries such as relative URLs, authority syntax, encoding, user-info, and trusted suffix matching.

Client-side display of a URL is not proof. Confirm the final browser or HTTP navigation and whether the flow carries credentials, authorization codes, or trusted branding.

Record the source endpoint, redirect chain, final origin, sensitive values propagated, and exchange ids. Rate isolated redirects lower than redirects that enable phishing or token theft.