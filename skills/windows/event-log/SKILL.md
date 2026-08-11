---
name: event-log
description: "Windows Event Viewer: query, filter, and export system/application/security logs."
version: 1.0.0
author: Covo Agent
platforms: [windows]
prerequisites:
  commands: [wevtutil, powershell]
metadata:
  tags: [windows, event-log, diagnostics]
---

# Windows Event Logs

```powershell
# Recent errors (last hour)
Get-WinEvent -FilterHashtable @{LogName='System'; Level=2; StartTime=(Get-Date).AddHours(-1)} |
  Select-Object TimeCreated, Id, Message -First 10

# Application logs
Get-WinEvent -LogName Application -MaxEvents 20 |
  Select-Object TimeCreated, LevelDisplayName, Message

# Security logs
Get-WinEvent -LogName Security -MaxEvents 10

# Search by event ID
Get-WinEvent -FilterHashtable @{LogName='System'; Id=41}  # Unexpected shutdown

# Export (wevtutil)
wevtutil qe System /c:20 /rd:true /f:text
wevtutil epl System backup.evtx

# Tail-like continuous view
Get-WinEvent -LogName System -MaxEvents 5 | Sort-Object TimeCreated -Descending
```
