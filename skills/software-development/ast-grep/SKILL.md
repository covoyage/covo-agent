---
name: ast-grep
description: "Use when searching or rewriting code by syntax shape (not bytes): ast-grep / sg patterns and codemods."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [sg]
metadata:
  tags: [ast, codemod, refactor, search, tree-sitter]
  related_skills: [simplify-code, systematic-debugging]
---

# ast-grep

`ast-grep` (binary `sg`) matches **code structure**, not text bytes. Use it
when the question depends on the language AST. Use `grep` / `search_files`
for string literals, comments, licenses, and file names.

## When to Use

- Find every function that takes a `Request` parameter
- Rewrite `console.log(x)` → `logger.info(x)` across a repo
- Strip `as any`, empty `catch`, `Optional[X]` → `X | None`
- YAML/JSON structural rules

Do **not** use for: "find the string TODO", import path typos, or binary files.

## Setup

```bash
brew install ast-grep
# or: cargo install ast-grep --locked
sg --version
```

## Search

```bash
sg --lang python -p 'def $NAME($$$ARGS): $$$' 
sg --lang typescript -p 'console.log($X)'
sg -p 'catch ($ERR) { }'          # empty catch
sg --json -p 'foo($A)'            # machine-readable
```

`$NAME` / `$X` are single nodes; `$$$` is a sequence (args, body).

## Rewrite (preview first)

```bash
sg --lang typescript -p 'console.log($X)' -r 'logger.info($X)' --update-all --dry-run
sg --lang typescript -p 'console.log($X)' -r 'logger.info($X)' --update-all
```

Never `--update-all` without a dry-run diff the user can accept. Prefer a
branch or `git diff` after.

## vs grep

| Question | Tool |
|---|---|
| Code shape / codemod | `sg` |
| Exact bytes, comments, paths | `grep` / `search_files` |

## Common Pitfalls

1. Writing a regex and expecting AST match — patterns must parse as that language.
2. Skipping `--lang` in mixed repos.
3. Applying rewrites in the live tree without dry-run.

## Verification

- [ ] Pattern parsed (`sg` did not report a pattern error)
- [ ] Dry-run reviewed before `--update-all`
- [ ] `git diff` looks like the intended codemod
