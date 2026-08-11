---
name: 1password
description: "1Password CLI: sign-in, read secrets, inject into commands, manage vaults."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [op]
metadata:
  tags: [password, secrets, vault, 1password, security]
  related_skills: [secrets]
---

# 1Password CLI

Manage secrets, environment variables, and credentials with 1Password CLI.

## Setup

```bash
# Install
brew install 1password-cli  # macOS

# Sign in
eval $(op signin)

# Or with account
op signin my.1password.com email@example.com
```

## Reading Secrets

```bash
# Get a password
op read "op://vault/item/field"

# Get full item as JSON
op item get "item-name" --format json

# Read credential
op read "op://Private/GitHub Token/credential"

# List items
op item list --vault "Private"
```

## Injecting into Commands

```bash
# Inject into env var
export GITHUB_TOKEN=$(op read "op://Private/GitHub Token/credential")

# Inject inline
op run -- gh auth status  # reads GITHUB_TOKEN from 1Password

# Run command with secrets
op run --env-file=.env -- npm start
```

## Creating & Updating

```bash
# Create login item
op item create \
  --category login \
  --title "My Service" \
  --url "https://service.com" \
  username="myuser" \
  password="$(op item create --generate-password)"

# Create a note
op item create --category secure-note --title "API Notes" \
  notes="Important notes here"

# Update field
op item edit "item-name" "field=value"
```

## Vault Management

```bash
op vault list
op vault create "Project X" --description "Secrets for Project X"
op vault user list "Project X"
```

## Environment Files

```bash
# Create .env from 1Password
cat > .env << 'EOF'
API_KEY=op://Private/My API/credential
DB_PASSWORD=op://Private/Database/password
EOF

# Run with injected secrets
op run --env-file=.env -- python main.py
```

## Service Accounts (Automation)

```bash
# Create service account token
op service-account create "CI Bot" --vault "Project X"

# Use token
export OP_SERVICE_ACCOUNT_TOKEN="..."
op read "op://Project X/DB/credential"
```
