---
name: system-diag
description: "Linux system diagnostics: CPU, memory, disk, processes, kernel info."
version: 1.0.0
author: Covo Agent
platforms: [linux]
prerequisites:
  commands: [bash]
metadata:
  tags: [linux, diagnostics, cpu, memory, disk, procfs]
---

# Linux System Diagnostics

```bash
# CPU
lscpu | grep "Model name\|CPU(s)"
cat /proc/cpuinfo | grep "model name" | head -1
top -bn1 | head -5

# Memory
free -h
cat /proc/meminfo | head -5

# Disk
df -h
lsblk

# Processes
ps aux --sort=-%mem | head -10

# Network
ip addr
ss -tlnp

# Kernel
uname -a
cat /proc/version

# System info (one-liner)
echo "CPU: $(nproc) cores | Mem: $(free -h | awk '/^Mem/{print $3 "/" $2}') | Disk: $(df -h / | awk 'NR==2{print $3 "/" $2 " (" $5 ")"}')"
```
