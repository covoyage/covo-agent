---
name: systemd-service
description: "Manage systemd services: enable/disable/start/stop/status/logs on Linux."
version: 1.0.0
author: Covo Agent
platforms: [linux]
prerequisites:
  commands: [systemctl]
metadata:
  tags: [linux, systemd, service, daemon]
---

# systemd — Service Management

```bash
# Status
systemctl status nginx
systemctl is-active nginx

# Start/stop/restart
sudo systemctl start nginx
sudo systemctl stop nginx
sudo systemctl restart nginx

# Enable at boot
sudo systemctl enable nginx
sudo systemctl disable nginx

# List all services
systemctl list-units --type=service
systemctl list-units --type=service --state=failed

# Logs
journalctl -u nginx -n 50
journalctl -u nginx -f  # follow

# Reload
sudo systemctl daemon-reload
sudo systemctl reload nginx  # graceful config reload

# Mask (prevent starting)
sudo systemctl mask bad-service
sudo systemctl unmask bad-service

# Create a service
sudo bash -c 'cat > /etc/systemd/system/my-app.service << EOF
[Unit]
Description=My App
After=network.target

[Service]
Type=simple
ExecStart=/usr/bin/my-app
Restart=on-failure
User=nobody

[Install]
WantedBy=multi-user.target
EOF'
sudo systemctl daemon-reload
sudo systemctl enable --now my-app
```
