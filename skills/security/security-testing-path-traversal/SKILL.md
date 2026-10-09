---
name: security-testing-path-traversal
description: Verify traversal and local or remote file inclusion across path normalization boundaries.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# Path Traversal Verification

Locate file names, templates, archive entries, download paths, static handlers, and storage keys. Account for URL decoding layers, separators, canonicalization, symlinks, drive prefixes, and allowlisted roots.

Use a harmless known file or uniquely created marker. Error text mentioning a path is only a lead, and reading an intentionally public file is not a boundary violation.

Confirm only when input escapes the intended root or includes an unintended local or remote resource. Record the normalized path, expected root, returned marker, source location when available, and exchange id.