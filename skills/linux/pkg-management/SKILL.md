---
name: pkg-management
description: "Package management: apt, dnf, pacman, apk — install/remove/search/update."
version: 1.0.0
author: Covo Agent
platforms: [linux]
prerequisites:
  commands: [apt, dnf, pacman, apk]
metadata:
  tags: [linux, package, apt, dnf, pacman, apk, install]
---

# Linux Package Management

```bash
# apt (Debian/Ubuntu)
sudo apt update && sudo apt upgrade -y
sudo apt install nginx
sudo apt remove nginx
apt search "python"
apt list --installed | grep nginx

# dnf (Fedora/RHEL)
sudo dnf upgrade -y
sudo dnf install nginx
sudo dnf remove nginx
dnf search python
dnf list installed | grep nginx

# pacman (Arch)
sudo pacman -Syu
sudo pacman -S nginx
sudo pacman -R nginx
pacman -Ss python

# apk (Alpine)
sudo apk update && sudo apk upgrade
sudo apk add nginx
sudo apk del nginx
apk search python

# Auto-detect and run
detect() {
  command -v apt >/dev/null && apt $@ && return
  command -v dnf >/dev/null && dnf $@ && return
  command -v pacman >/dev/null && pacman $@ && return
  command -v apk >/dev/null && apk $@ && return
}
```
