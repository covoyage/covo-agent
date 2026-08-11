---
name: system-info
description: "Windows system diagnostics: CPU, RAM, disk, processes, services, network."
version: 1.0.0
author: Covo Agent
platforms: [windows]
prerequisites:
  commands: [powershell]
metadata:
  tags: [windows, diagnostics, cpu, memory, disk, wmi]
---

# Windows System Diagnostics

```powershell
# System info
Get-ComputerInfo | Select-Object WindowsVersion, CsTotalPhysicalMemory, CsProcessors

# CPU
Get-CimInstance Win32_Processor | Select-Object Name, NumberOfCores, MaxClockSpeed

# Memory
Get-CimInstance Win32_ComputerSystem | Select-Object TotalPhysicalMemory
Get-CimInstance Win32_OperatingSystem | Select-Object FreePhysicalMemory

# Disk
Get-PSDrive -PSProvider FileSystem
Get-CimInstance Win32_LogicalDisk | Select-Object DeviceID, @{N="Size(GB)";E={[math]::Round($_.Size/1GB,2)}}, @{N="Free(GB)";E={[math]::Round($_.FreeSpace/1GB,2)}}

# Top processes by memory
Get-Process | Sort-Object WorkingSet64 -Descending | Select-Object Name, @{N="MB";E={[math]::Round($_.WorkingSet64/1MB)}} -First 10

# Services
Get-Service | Where-Object Status -eq "Running" | Select-Object Name, DisplayName

# Network
Get-NetIPAddress -AddressFamily IPv4 | Where-Object InterfaceAlias -notlike "*Loopback*"
Get-NetTCPConnection | Where-Object State -eq "Listen"
Test-NetConnection google.com -Port 443

# Quick overview
@"
CPU: $((Get-CimInstance Win32_Processor).Name)
RAM: $(Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory / 1GB GB
Disk: $($((Get-PSDrive C).Used)/1GB) / $($((Get-PSDrive C).Used + (Get-PSDrive C).Free)/1GB) GB
"@
```
