---
name: rest-graphql-debug
description: "Debug REST and GraphQL APIs: status codes, authentication, schemas, and reproducible scripts."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [curl]
metadata:
  tags: [api, rest, graphql, debug, http, curl, schema]
  related_skills: [systematic-debugging, plan]
---

# REST/GraphQL API Debug

Debug failing API calls systematically. Use when the user reports API errors,
needs to understand an API, or wants to produce a reproducible test case.

## When to Use

- "this API call is returning an error"
- "debug the authentication for this API"
- "what's wrong with this endpoint?"
- "help me understand this GraphQL schema"
- "produce a curl command that works"

## Process

### Phase 1 — Gather Context

1. Ask for the exact error message / status code
2. Get the full request: URL, method, headers (redact secrets), body
3. Get the full response: status, headers, body
4. Check if authentication is involved (Bearer tokens, API keys, OAuth)

### Phase 2 — Classify the Problem

| Symptom | Likely Cause | Check |
|---------|-------------|-------|
| 401 Unauthorized | Missing/expired token | Verify Authorization header format; check token expiry |
| 403 Forbidden | Insufficient scope | Check API scopes/permissions |
| 404 Not Found | Wrong endpoint/ID | Verify URL path and resource ID |
| 422 Unprocessable | Invalid request body | Validate against schema; check required fields |
| 429 Too Many Requests | Rate limiting | Check Retry-After header; implement backoff |
| 500 Server Error | Server-side bug | Check server logs if accessible; retry with exponential backoff |
| Timeout | Connection/network | Check DNS, firewall, proxy; verify URL reachable with `curl -v` |
| SSL/TLS Error | Certificate issue | Check certificate chain; try with `--insecure` for testing |

### Phase 3 — Reproduce Isolated

Create a minimal `curl` command that reproduces the issue:
```bash
curl -v -X METHOD \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"key": "value"}' \
  https://api.example.com/v1/endpoint
```

Key flags:
- `-v` — verbose (show request/response headers)
- `-w "\n%{http_code}"` — print status code
- `-o /dev/null` — discard body, headers only

### Phase 4 — GraphQL Specific

For GraphQL APIs:

1. **Introspect the schema:**
```graphql
query { __schema { types { name kind fields { name type { name kind } } } } }
```

2. **Debug with `--data-raw`:**
```bash
curl -X POST https://api.example.com/graphql \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  --data-raw '{"query":"{ viewer { login } }"}'
```

3. **Common GraphQL errors:**
   - `"errors"` array in response → check each error message and path
   - `"Cannot query field 'X'"` → field doesn't exist or is deprecated
   - `"Variable $X of type Y is required"` → missing required variable
   - `"Field 'X' doesn't accept argument 'Y'"` → wrong argument name

### Phase 5 — Fix and Verify

1. Identify the root cause
2. Propose the fix (update headers, fix body schema, handle rate limits, etc.)
3. Test with the isolated curl command
4. Produce the corrected code
5. Add error handling for this failure mode going forward

## Tools

Use these built-in tools during debugging:
- `web_fetch` — test GET endpoints
- `bash` with `curl` — test any HTTP method
- `jq` — parse and inspect JSON responses
- `python3 -m json.tool` — format JSON when jq unavailable
