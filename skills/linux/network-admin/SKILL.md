---
name: network-admin
description: "Linux networking: firewall, DNS, routing, interface config."
version: 1.0.0
author: Covo Agent
platforms: [linux]
prerequisites:
  commands: [bash]
metadata:
  tags: [linux, network, firewall, dns, iptables, ufw]
---

# Linux Network Administration

```bash
# Interfaces
ip addr show
ip link set eth0 up/down

# Routing
ip route show
ip route add default via 192.168.1.1

# DNS
cat /etc/resolv.conf
resolvectl status
nslookup example.com

# Firewall (ufw)
sudo ufw status
sudo ufw enable
sudo ufw allow 22/tcp
sudo ufw deny 80

# Firewall (iptables)
sudo iptables -L -n -v
sudo iptables -A INPUT -p tcp --dport 80 -j ACCEPT

# Listening ports
ss -tlnp
ss -ulnp

# Connections
ss -s  # summary
ss -tanp  # all TCP

# Public IP
curl -s ifconfig.me
```
