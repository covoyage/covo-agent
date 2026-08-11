---
name: winget-choco
description: "Windows package management: winget, Chocolatey — install/search/update/remove."
version: 1.0.0
author: Covo Agent
platforms: [windows]
prerequisites:
  commands: [winget, choco]
metadata:
  tags: [windows, package, winget, chocolatey, install]
---

# Windows Package Management

```powershell
# winget (built-in)
winget search python
winget install Python.Python.3.12
winget list
winget upgrade --all
winget uninstall Python.Python.3.12

# Chocolatey
choco search python
choco install python
choco list --local-only
choco upgrade all
choco uninstall python

# Windows Update
Get-WindowsUpdate
Install-WindowsUpdate -AcceptAll

# Check installed apps via registry
Get-ItemProperty HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\* |
  Select-Object DisplayName, DisplayVersion |
  Where-Object {$_.DisplayName}
```
