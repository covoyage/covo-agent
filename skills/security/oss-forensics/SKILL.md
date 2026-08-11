---
name: oss-forensics
description: "Open source forensic analysis: dependency audit, license scanning, supply chain analysis."
version: 1.1.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [forensics, audit, dependencies, license, supply-chain, oss]
  related_skills: [codebase-inspection, web-pentest]
---

# OSS Forensics — Open Source Analysis

Audit open source projects for security, license compliance, and supply
chain risks before integrating them.

## Dependency Audit

```bash
# npm audit
npm audit
npm audit --json > audit-report.json

# Python safety check
pip install safety
safety check
safety check --json

# Go vulnerability check
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...

# Rust cargo audit
cargo install cargo-audit
cargo audit
```

## License Scanning

```bash
# Scan for licenses
pip install licensecheck
licensecheck

# npm license check
npx license-checker --summary

# Go license check
go install github.com/google/go-licenses@latest
go-licenses check ./...
go-licenses csv ./...

# General: scan for LICENSE files
find . -name "LICENSE*" -o -name "COPYING*"
```

## Code Quality Audit

```bash
# Git history analysis
git log --format="%ae: %s" | head -50
git shortlog -sn --all

# Bus factor (top contributors)
git shortlog -sn | awk '{s+=$1}END{print NR, s/NR}'

# Commit frequency
git log --since="1 year ago" --format="%ad" --date=short | sort | uniq -c

# File age / staleness
git ls-files | while read f; do
  echo "$(git log -1 --format="%ai" -- "$f") $f"
done | sort
```

## Supply Chain Analysis

### Package Health Check

```python
import requests

def check_package_health(pkg, ecosystem="pypi"):
    """Check package metadata for trust signals."""
    if ecosystem == "pypi":
        resp = requests.get(f"https://pypi.org/pypi/{pkg}/json", timeout=10)
        data = resp.json()
        info = data["info"]
        return {
            "name": pkg,
            "version": info["version"],
            "author": info.get("author"),
            "license": info.get("license"),
            "requires_python": info.get("requires_python"),
            "maintainers": len(info.get("maintainers", [])),
            "releases": len(data["releases"]),
        }
    elif ecosystem == "go":
        # Go modules: check pkg.go.dev
        resp = requests.get(f"https://pkg.go.dev/{pkg}?tab=overview", timeout=10)
        return {"name": pkg, "url": f"https://pkg.go.dev/{pkg}"}
    
print(check_package_health("requests"))
```

### Go Module Security

```bash
# Verify go.sum integrity
# go.sum contains cryptographic hashes — tampering = red flag
go mod verify

# Check for dependency changes in git
git diff go.mod go.sum

# List all dependencies with versions
go list -m all

# Check for replace directives (local/dev paths)
grep -n 'replace' go.mod

# Find indirect dependencies
go list -m -json all | jq 'select(.Indirect == true)'

# Check for pre-release versions (higher risk)
grep -E 'v0\.0\.0-|alpha|beta|rc' go.mod
```

### Go-Specific Supply Chain Red Flags

```bash
# 1. init() with network calls (import-time C2 beacon)
grep -rn 'func init()' --include='*.go' | xargs grep -l 'http\.\(Get\|Post\|Do\)\|net\.Dial'

# 2. go:generate with remote code fetch
grep -rn '//go:generate.*curl\|wget\|pip\|go install' --include='*.go'

# 3. go:linkname overriding security packages
grep -rn '//go:linkname.*crypto/\|os/exec' --include='*.go'

# 4. Dynamic library loading
grep -rn 'plugin\.Open\|syscall\.LoadLibrary\|dlopen' --include='*.go'

# 5. Replace directives pointing to local paths (dev bypass)
grep -n 'replace.*=>' go.mod | grep -v '// indirect'

# 6. Pre-release pseudo-versions (unreviewed code)
grep -E 'v0\.0\.0-[0-9]{14}-[a-f0-9]{12}' go.mod
```

### Go CVE Scanning

```bash
# Install govulncheck
go install golang.org/x/vuln/cmd/govulncheck@latest

# Scan for known vulnerabilities
govulncheck ./...

# Scan specific package
govulncheck -mode binary ./myapp

# JSON output for CI integration
govulncheck -json ./... > vuln-report.json
```

## Git History Forensics

```bash
# Find secrets in commit history
git log -p | grep -i "password\|secret\|token\|api_key\|private_key"

# Large files in history
git rev-list --objects --all | git cat-file --batch-check='%(objecttype) %(objectsize) %(rest)' | awk '/^blob/ {print $2, $3}' | sort -rn | head -20

# Deleted files audit
git log --diff-filter=D --summary | grep "delete mode"

# Unusual commit patterns (same timestamp)
git log --format="%ai %ae %s" | sort | uniq -c -w20 | sort -rn
```

## Security Scan Quick Script

```bash
#!/bin/bash
echo "=== OSS Security Scan: $(pwd) ==="

# Dependencies
echo -e "\n--- Dependencies ---"
[ -f package.json ] && npx license-checker --summary 2>/dev/null
[ -f requirements.txt ] && safety check --file requirements.txt 2>/dev/null
[ -f go.mod ] && go list -m all 2>/dev/null

# Secrets check
echo -e "\n--- Secrets Scan ---"
rg -i "password|secret|token|api[_-]?key|private[_-]?key" --glob '!node_modules' --glob '!.git' -l

# Large files
echo -e "\n--- Large Files ---"
find . -type f -size +1M -not -path './node_modules/*' -not -path './.git/*' | head -10

# Suspicious patterns
echo -e "\n--- Suspicious Patterns ---"
rg "eval\(|exec\(|child_process|os\.system|subprocess" --glob '*.{js,py,go}' -l | head -10
```
