---
name: registry-edit
description: "Windows Registry: query, create, modify, delete keys and values."
version: 1.0.0
author: Covo Agent
platforms: [windows]
prerequisites:
  commands: [reg, powershell]
metadata:
  tags: [windows, registry, regedit, configuration]
---

# Windows Registry

```powershell
# Query
reg query "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion"

# Get value
reg query "HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion" /v ProductName

# Set value
reg add "HKCU\Software\MyApp" /v Setting /t REG_SZ /d "value" /f

# Delete
reg delete "HKCU\Software\MyApp" /v Setting /f
reg delete "HKCU\Software\MyApp" /f  # delete entire key

# PowerShell: read
Get-ItemProperty "HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion" |
  Select-Object ProductName, EditionID, CurrentBuild

# PowerShell: set/create
Set-ItemProperty -Path "HKCU:\Software\MyApp" -Name "Setting" -Value "new-value"
New-Item -Path "HKCU:\Software\MyApp" -Force

# Search recursively
Get-ChildItem -Path "HKLM:\SOFTWARE\Microsoft" -Recurse -ErrorAction SilentlyContinue |
  Where-Object { $_.GetValue("DisplayName") -like "*Python*" }

# Export/Import
reg export "HKLM\SOFTWARE\MyApp" backup.reg
reg import backup.reg
```
