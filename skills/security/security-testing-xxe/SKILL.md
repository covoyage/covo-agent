---
name: security-testing-xxe
description: Verify unsafe XML external entity resolution and its concrete impact.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# XML External Entity Verification

Identify XML parsers in APIs, uploads, office documents, feeds, SAML, SVG, and background processors. Determine whether DTDs, external entities, XInclude, or network resolution are enabled.

Use a harmless local marker or controlled callback. Parser errors mentioning entities do not prove expansion, and outbound DNS alone may not prove response disclosure.

Confirm only with repeatable local resource access, controlled callback, or protected network reachability caused by XML parsing. Record the parser path, content type, processing stage, and exchange ids.