---
name: mcporter
description: "MCP server management: list, configure, authenticate, and call servers over HTTP or stdio."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [npx]
metadata:
  tags: [mcp, server, tool, management, protocol]
  related_skills: [mcp]
---

# MCPorter — MCP Server Management

Manage MCP servers: list configured servers, authenticate, call tools,
and inspect capabilities over HTTP or stdio transport.

## Installation

```bash
npm i -g mcporter
# Or direct
npx mcporter <command>
```

## List Servers & Tools

```bash
# List configured MCP servers
npx mcporter list

# List tools on a specific server
npx mcporter tools <server-name>

# List all tools across all servers
npx mcporter tools --all
```

## Call a Tool

```bash
# Call a tool on an HTTP server
npx mcporter call <server-name> <tool-name> '{"arg": "value"}'

# Call with stdio transport
npx mcporter call --transport stdio <command> <tool-name> '{}'

# Example: call web search
npx mcporter call brave-search brave_web_search '{"query": "MCP protocol"}'
```

## Authenticate

```bash
# Set auth token
npx mcporter auth <server-name> --token "sk-..."

# Set OAuth credentials
npx mcporter auth <server-name> --client-id xxx --client-secret yyy
```

## Inspect Server

```bash
# Get server capabilities
npx mcporter inspect <server-name>

# Show tool schema
npx mcporter inspect <server-name> <tool-name>

# Test connection
npx mcporter ping <server-name>
```

## Ad-Hoc Servers

```bash
# Call a tool on an unregistered server
npx mcporter adhoc http://localhost:3000/sse <tool-name> '{}'

# stdio mode
npx mcporter adhoc --transport stdio -- "python mcp_server.py" <tool-name> '{}'
```

## Generate Code

```bash
# Generate TypeScript types from MCP schemas
npx mcporter types <server-name> > mcp-types.ts

# Generate CLI wrapper
npx mcporter cli <server-name> > mcp-cli.sh
chmod +x mcp-cli.sh
```

## Configuration Management

```bash
# Add server
npx mcporter config add <name> --url https://server/sse

# Add stdio server
npx mcporter config add <name> --command "python" --args "mcp_server.py"

# Show config
npx mcporter config show

# Edit config
npx mcporter config edit
```
