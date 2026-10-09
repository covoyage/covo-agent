---
name: security-testing-file-upload
description: Verify upload validation, storage, retrieval, parsing, and execution boundaries.
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
---

# File Upload Verification

Map filename, extension, declared type, detected type, archive handling, image/document processing, storage key, retrieval headers, and access control. Use inert marker files whenever execution is unnecessary.

Test validation and serving as separate boundaries. An accepted file is not a vulnerability unless it crosses an intended trust boundary such as execution, active content, overwrite, unauthorized read, or unsafe parser invocation.

Record upload and retrieval exchanges, final storage location, transformation behavior, and cleanup status. Do not claim execution without an observable server-side effect.