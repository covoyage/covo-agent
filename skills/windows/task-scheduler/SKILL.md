---
name: task-scheduler
description: "Windows Task Scheduler: create, list, run, delete scheduled tasks via schtasks."
version: 1.0.0
author: Covo Agent
platforms: [windows]
prerequisites:
  commands: [schtasks, powershell]
metadata:
  tags: [windows, scheduler, task, cron]
---

# Windows Task Scheduler

```powershell
# List tasks
schtasks /query /fo LIST /v

# Create daily task
schtasks /create /tn "MyTask" /tr "powershell.exe -File C:\script.ps1" /sc daily /st 09:00

# Run once
schtasks /create /tn "OneTime" /tr "notepad.exe" /sc once /st 14:00

# Run task immediately
schtasks /run /tn "MyTask"

# Delete task
schtasks /delete /tn "MyTask" /f

# PowerShell: create task
$action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-File C:\backup.ps1"
$trigger = New-ScheduledTaskTrigger -Daily -At 2am
Register-ScheduledTask -TaskName "NightlyBackup" -Action $action -Trigger $trigger

# List all scheduled tasks (PowerShell)
Get-ScheduledTask | Where-Object State -ne "Disabled" |
  Select-Object TaskName, State
```
