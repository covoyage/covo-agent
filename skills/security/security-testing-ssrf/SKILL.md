---
name: security-testing-ssrf
description: Verify server-side URL fetching and network boundary impact with controlled callbacks.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# Server-Side Request Forgery Verification

Locate URL, webhook, import, preview, image, proxy, and callback features. Separate client-side fetching from server-side fetching with a controlled endpoint and unique request markers.

Test normalization boundaries such as redirects, schemes, DNS resolution, ports, and parser differences without assuming that an error proves reachability. Compare allowed external requests against blocked destinations.

Confirm only with a server-originated callback or a repeatable response demonstrating access to a protected network resource. Record redirect chains, destination, timing, and exchange ids while minimizing retrieved sensitive data.