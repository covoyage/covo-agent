---
name: pinggy-tunnel
description: "Zero-install localhost tunnels over SSH — no signup, no binary, just SSH."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [ssh]
metadata:
  tags: [tunnel, localhost, ssh, expose, webhook, debug, public]
  related_skills: [docker-management]
---

# Pinggy Tunnel — Zero-Install Localhost Tunnels

Expose local services to the internet instantly using SSH. No signup,
no binary download, just a single SSH command.

## When to Use

- "expose my local server to the internet"
- "share my localhost with someone"
- "test a webhook on my dev machine"
- "debug a public callback URL"
- "demo my local app remotely"

## Basic Tunnel

```bash
# Expose local port 8080
ssh -p 443 -R0:localhost:8080 a.pinggy.io

# Output will show the public URL:
# Public URL: https://xxxx-xxx-xxx.pinggy.link
```

## Named Subdomain

```bash
# Persistent subdomain
ssh -p 443 -R0:localhost:3000 token@a.pinggy.io

# Custom subdomain (with login)
ssh -p 443 -Rmyapp.a:localhost:3000 user@a.pinggy.io
```

## HTTP Basic Auth

```bash
ssh -p 443 -R0:localhost:8080 a.pinggy.io \
  "Header:Authorization Basic $(echo -n 'user:pass' | base64)"
```

## Custom Headers

```bash
# Add CORS headers
ssh -p 443 -R0:localhost:3000 a.pinggy.io \
  "Header:Access-Control-Allow-Origin *"

# Multiple headers
ssh -p 443 -R0:localhost:8080 a.pinggy.io \
  "Header:X-Custom-Header value" \
  "Header:X-Another another"
```

## IP Whitelist

```bash
# Restrict access to specific IPs
ssh -p 443 -R0:localhost:8080 a.pinggy.io \
  "IpWhitelist:1.2.3.4 5.6.7.8"
```

## Webhook Debugging

```bash
# Expose webhook endpoint and see requests
ssh -p 443 -R0:localhost:4567 a.pinggy.io

# Then configure webhook URL as:
# https://xxxx-xxx-xxx.pinggy.link/webhook
```

## VS Code / Any TCP Service

```bash
# TCP tunnel (not HTTP)
ssh -p 443 -R0:localhost:22 a.pinggy.io \
  "TunnelType:tcp"

# For SSH itself
ssh -p 443 -R0:localhost:22 a.pinggy.io \
  "TunnelType:tcp"
```

## Background Mode

```bash
# Run in background (screen/tmux recommended)
screen -dmS pinggy ssh -p 443 -R0:localhost:8080 a.pinggy.io

# Or nohup
nohup ssh -p 443 -R0:localhost:8080 a.pinggy.io > /tmp/pinggy.log 2>&1 &
```

## Tunneling Docker Containers

```bash
# Expose a running container
docker run -p 8080:80 nginx
ssh -p 443 -R0:localhost:8080 a.pinggy.io
```

## Alternatives

| Tool | Install | Signup | Free? |
|------|---------|--------|-------|
| Pinggy | SSH only | No | Yes |
| ngrok | Download | Yes | Free tier |
| Cloudflare Tunnel | cloudflared | Yes | Free |
| localhost.run | SSH only | No | Yes |
| bore | Download | No | Self-hosted |
