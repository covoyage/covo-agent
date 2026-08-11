---
name: journal-log
description: "View and filter systemd journal logs with journalctl."
version: 1.0.0
author: Covo Agent
platforms: [linux]
prerequisites:
  commands: [journalctl]
metadata:
  tags: [linux, journald, logs, debugging]
---

# Journal Logs

```bash
# Recent logs
journalctl -n 50

# Follow
journalctl -f

# By service
journalctl -u nginx -n 30

# By time range
journalctl --since "1 hour ago"
journalctl --since "2026-01-01" --until "2026-01-02"

# By priority
journalctl -p err  # errors and worse
journalctl -p 3    # err/2=crit/1=alert/0=emerg

# Kernel logs
journalctl -k

# Boot logs
journalctl -b

# Disk usage
journalctl --disk-usage

# Vacuum (keep last 7 days)
sudo journalctl --vacuum-time=7d
```
