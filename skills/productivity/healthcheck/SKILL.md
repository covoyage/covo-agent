---
name: healthcheck
description: "Host security audit: OS, SSH, firewall, updates, backups, disk encryption."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [bash]
metadata:
  tags: [health, audit, security, host, diagnostic, ssh, firewall]
  related_skills: [stuck]
---

# Healthcheck — Host Security Audit

Read-only audit of host security posture. Identifies risks and proposes
staged hardening without breaking access.

## When to Use

- "audit my system security"
- "check if my server is hardened"
- "what should I fix on this machine?"
- "security health check"

## Quick Audit Script

```bash
#!/bin/bash
echo "=== Host Audit: $(hostname) @ $(date) ==="

echo -e "\n--- OS & Updates ---"
uname -a
[ -f /etc/os-release ] && . /etc/os-release && echo "$NAME $VERSION"
# macOS
[ "$(uname)" = "Darwin" ] && softwareupdate -l 2>/dev/null | grep -c "Label"
# Linux
[ -f /etc/debian_version ] && apt list --upgradable 2>/dev/null | wc -l

echo -e "\n--- SSH ---"
if [ -f /etc/ssh/sshd_config ]; then
    echo "Password auth: $(grep -i '^PasswordAuthentication' /etc/ssh/sshd_config | awk '{print $2}')"
    echo "Root login: $(grep -i '^PermitRootLogin' /etc/ssh/sshd_config | awk '{print $2}')"
    echo "Port: $(grep -i '^Port' /etc/ssh/sshd_config | awk '{print $2}')"
fi
# Authorized keys
[ -f ~/.ssh/authorized_keys ] && echo "SSH keys: $(wc -l < ~/.ssh/authorized_keys)"

echo -e "\n--- Firewall ---"
# macOS
[ "$(uname)" = "Darwin" ] && /usr/libexec/ApplicationFirewall/socketfilterfw --getglobalstate 2>/dev/null
# Linux (ufw)
command -v ufw >/dev/null && ufw status verbose 2>/dev/null | head -5

echo -e "\n--- Disk & Encryption ---"
df -h / | tail -1
# macOS FileVault
[ "$(uname)" = "Darwin" ] && fdesetup status 2>/dev/null
# Linux LUKS
command -v lsblk >/dev/null && lsblk -o NAME,SIZE,TYPE,MOUNTPOINT,FSTYPE | grep -E "crypt|luks"

echo -e "\n--- Backups ---"
# Time Machine
[ "$(uname)" = "Darwin" ] && tmutil destinationinfo 2>/dev/null | grep "Name"

echo -e "\n--- Users ---"
echo "Users with shell: $(grep -E '(:/bin/.*sh$)' /etc/passwd | wc -l)"
echo "Sudoers: $(grep -c '^[^#]' /etc/sudoers 2>/dev/null || echo '0')"

echo -e "\n--- Listening Ports ---"
# macOS
[ "$(uname)" = "Darwin" ] && lsof -iTCP -sTCP:LISTEN -P -n 2>/dev/null | awk '{print $1, $9}' | tail -n +2 | sort -u
# Linux
command -v ss >/dev/null && ss -tlnp 2>/dev/null | grep LISTEN

echo -e "\n--- Exposure Check ---"
if command -v curl >/dev/null; then
    public_ip=$(curl -s ifconfig.me 2>/dev/null)
    [ -n "$public_ip" ] && echo "Public IP: $public_ip"
fi

echo -e "\n--- Git Config Safety ---"
git config --global user.name 2>/dev/null
[ -f ~/.gitconfig ] && grep -c "signingkey" ~/.gitconfig >/dev/null && echo "✅ GPG signing configured" || echo "⚠️  No GPG signing"

echo -e "\n=== Audit Complete ==="
```

## Staged Hardening

| Stage | Changes | Risk |
|-------|---------|------|
| **Safe** | Enable firewall, disable root SSH, auto-updates | None |
| **Moderate** | Key-only SSH, auditd, fail2ban | Low |
| **Aggressive** | SELinux enforcing, remove compilers, read-only root | Breaks things |

Start with safe changes, test, then proceed.
