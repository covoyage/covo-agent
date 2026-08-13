# covo-agent

<p align="center">
  <img src="logo.png" alt="covo-agent" width="200">
</p>

[简体中文](README.zh-CN.md) | English

`covo-agent` is a general-purpose AI agent for the terminal, with both general and code-focused modes. It supports everyday knowledge work, software development, automation, persistent context, and external-system collaboration through an interactive TUI, tools, memory, skills, safety controls, and extensible integrations.

## Highlights

- **Interactive terminal workflow**: streaming responses, tool activity, mouse selection, session history, model picker, themes, and shell completion.
- **Multiple execution modes**: interactive TUI, one-shot output, and policy-controlled headless runs.
- **Coding tools**: file search and editing, patch application, shell execution, code analysis, review, test generation, Git worktrees, and checkpoints.
- **Persistent context**: sessions, memory providers, skills, goals, snapshots, commitments, profiles, and project-level configuration.
- **Provider flexibility**: OpenAI, Anthropic, Gemini, Xiaomi, OpenRouter, and OpenAI-compatible custom providers.
- **Safety controls**: approval gates, allow/deny policies, secret redaction, URL/path checks, OS sandbox profiles, audit logs, and doom-loop recovery.
- **Extensible integrations**: MCP servers, ACP, LSP, plugins, extensions, communication gateways, and custom providers.

## Requirements

- Go **1.25.0** or newer for source builds.
- An API key or login for at least one supported model provider.
- Git is recommended for checkpoints, worktrees, review, and repository-aware features.
- Chrome or Chromium is optional and enables the full browser tool experience.

Feature availability can vary across macOS, Linux, and Windows. Run `covo-agent doctor` to inspect the current environment.

## Install

Install the latest release with a single command:

```bash
curl -fsSL https://raw.githubusercontent.com/covoyage/covo-agent/main/install.sh | bash
```

The script downloads the binary for your OS and architecture from the GitHub Releases page, installs it to `~/.covo-agent/bin`, and adds it to your `PATH`. Pass `--version <version>` to install a specific release, or `--no-modify-path` to skip the shell-config edit.

Install with Homebrew on macOS or Linux:

```bash
brew install --cask covoyage/tap/covo-agent
```

Install with Scoop on Windows:

```powershell
scoop bucket add covoyage https://github.com/covoyage/scoop-bucket
scoop install covoyage/covo-agent
```

Release archives and checksums are also published on the GitHub Releases page.

Install the latest version with Go:

```bash
go install github.com/covoyage/covo-agent/cmd/covo-agent@latest
```

The binary is installed to `GOBIN`, or to `$(go env GOPATH)/bin` when `GOBIN` is not set. Make sure that directory is on your `PATH`.

### Build From Source

Source builds use the dependency layout declared in `go.mod`. Prepare the referenced local modules, then clone and build the project:

```bash
mkdir covoyage && cd covoyage
git clone https://github.com/covoyage/covo-agent.git
cd covo-agent

go build -o bin/covo-agent ./cmd/covo-agent
```

Optionally install the binary on your `PATH`:

```bash
install -m 0755 bin/covo-agent "$HOME/.local/bin/covo-agent"
```

## Quick Start

Configure a provider and model:

```bash
covo-agent setup
# or reopen the model/provider picker later
covo-agent model
```

Credentials can also be managed explicitly:

```bash
covo-agent auth add OPENAI_API_KEY=your-key
covo-agent auth list
```

Start the interactive TUI in a project directory:

```bash
cd your-project
covo-agent
```

Run a single prompt without the TUI:

```bash
covo-agent --oneshot "summarize the current repository"
covo-agent -z "review the uncommitted changes" --json
```

Run a constrained headless task:

```bash
covo-agent --headless \
  -z "find the cause of the failing tests" \
  --tools read,grep,glob,bash \
  --max-turns 8 \
  --allow 'bash:go test *' \
  --deny 'bash:rm *'
```

## Execution Modes

| Mode | Example | Use case |
| --- | --- | --- |
| Interactive TUI | `covo-agent` | Iterative coding, tools, approvals, and session navigation |
| One-shot | `covo-agent -z "prompt"` | Scripts and a single terminal response |
| One-shot JSON | `covo-agent -z "prompt" --json` | Structured automation output |
| Headless | `covo-agent --headless -z "prompt"` | Non-interactive agent runs with explicit policies |

Useful root flags include:

```text
--provider <name>          Override the configured provider
--model <name>             Override the configured model
--mode general|code        Select the agent mode
--session-id <id>          Resume or create a named session
--sandbox <profile>        workspace, read-only, strict, devbox, off, or custom
--system-prompt <text>     Replace the default system prompt
--append-system-prompt     Append text to the default system prompt
--yolo                     Bypass approval prompts (high risk)
```

Use `covo-agent --help` for the complete and current flag list.

## Command Map

| Area | Commands |
| --- | --- |
| Setup and health | `setup`, `model`, `config`, `auth`, `doctor`, `status`, `version`, `update` |
| Sessions and knowledge | `session`, `memory`, `skill`, `dreaming`, `commitments`, `backup`, `restore`, `migrate` |
| Coding workflow | `analyze`, `review`, `pr`, `testgen`, `worktree` |
| Integrations | `mcp`, `acp`, `lsp`, `gateway`, `pairing`, `plugin`, `ext`, `package` |
| Personalization | `profile`, `language`, `theme`, `features`, `template` |
| Automation | `cron`, `heartbeat`, `completion` |

Every command has its own help page:

```bash
covo-agent session --help
covo-agent gateway --help
covo-agent mcp --help
```

Shell completion is available for Bash, Zsh, Fish, and PowerShell:

```bash
covo-agent completion zsh > "${fpath[1]}/_covo-agent"
covo-agent completion bash > ~/.local/share/bash-completion/completions/covo-agent
```

## Configuration

The default global configuration is stored in:

```text
~/.covo-agent/config.yaml
```

A project can override global values with `.covo-agent.yaml`. The loader searches upward from the current directory and stops at the Git root or the user home directory.

Minimal configuration:

```yaml
provider: openai
model: gpt-5.6
mode: code
```

Environment variables in YAML are expanded, so secrets can remain outside the config file:

```yaml
custom_providers:
  - name: Local
    protocol: openai/chat
    base_url: ${CUSTOM_BASE_URL}
    api_key_env: CUSTOM_API_KEY
```

Credentials managed by `covo-agent auth` are stored in `~/.covo-agent/.env` with restrictive file permissions. Project and process environment variables may override configured values.

Profiles use isolated data directories under:

```text
~/.covo-agent/profiles/<profile>/
```

Set `COVO_PROFILE` or use the `profile` command to select one.

Set `COVO_USER_AGENT` to override the `User-Agent` header sent to LLM providers (defaults to `covo-agent/<version>`).

## Data Locations

| Path | Purpose |
| --- | --- |
| `~/.covo-agent/config.yaml` | Global configuration |
| `~/.covo-agent/.env` | Provider credentials and environment values |
| `.covo-agent.yaml` | Project-level overrides |
| `~/.covo-agent/sessions/` | Persistent sessions and lifecycle sidecars |
| `~/.covo-agent/skills/` | Installed and user-created skills |
| `~/.covo-agent/covo-agent.log` | Interactive runtime warnings and diagnostics |
| `~/.covo-agent/profiles/` | Isolated profile data |

## Safety

Tool actions that can mutate files, execute commands, or affect external systems pass through approval and policy layers. For automation, use explicit allow and deny rules and a sandbox profile.

```bash
covo-agent --headless -z "run the test suite and fix one failure" \
  --sandbox workspace \
  --allow 'edit:*' \
  --allow 'bash:go test *' \
  --deny 'bash:git push*'
```

`--yolo` disables dangerous-operation approval prompts. Use it only in an environment where unrestricted tool execution is acceptable.

Telemetry export is opt-in. Set `COVO_OTEL_ENDPOINT` or `COVO_OTEL_ENABLED=true` to enable it. Audit and telemetry payloads pass through secret redaction before export.

## Troubleshooting

Run the environment and configuration checks:

```bash
covo-agent doctor
covo-agent doctor --fix
```

Common checks include:

- home, configuration, credential, session, and skill paths;
- provider key and model configuration;
- Git and browser availability;
- terminal color, clipboard, multiplexer, and keyboard capabilities.

Interactive warnings are written to `~/.covo-agent/covo-agent.log` so they do not corrupt the alternate-screen TUI.

## Development

Before building, make sure the local module paths declared in `go.mod` are available in the workspace.

```bash
go build ./...
go test ./...
go vet ./...
```

Build the runnable binary after making changes:

```bash
go build -o bin/covo-agent ./cmd/covo-agent
```

Before submitting changes, run the build, test, and vet commands above and rebuild the executable.

## License

This project is licensed under the [GNU Affero General Public License v3.0](LICENSE) (`AGPL-3.0`).
