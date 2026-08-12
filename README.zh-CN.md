# covo-agent

<p align="center">
  <img src="logo.png" alt="covo-agent" width="200">
</p>

简体中文 | [English](README.md)

`covo-agent` 是一个运行在终端中的通用型 AI Agent，提供 general 与 code 两种工作模式。它面向日常知识工作、软件开发、自动化、持久上下文管理和外部系统协作，并通过交互式 TUI、工具、记忆、技能、安全控制与可扩展集成提供完整工作流。

## 主要能力

- **终端交互工作流**：流式回复、工具状态、鼠标选择、会话历史、模型选择器、主题和 Shell 补全。
- **多种运行方式**：交互式 TUI、单次输出，以及受策略约束的无头运行。
- **编程工具**：文件搜索与编辑、补丁应用、Shell 执行、代码分析、Review、测试生成、Git Worktree 和检查点。
- **持久上下文**：会话、记忆 Provider、技能、目标、快照、承诺事项、Profile 和项目级配置。
- **灵活的模型接入**：支持 OpenAI、Anthropic、Gemini、Xiaomi、OpenRouter，以及兼容 OpenAI 协议的自定义 Provider。
- **安全控制**：审批门、允许/拒绝策略、敏感信息脱敏、URL/路径检查、系统沙箱、审计日志和死循环恢复。
- **扩展集成**：MCP Server、ACP、LSP、插件、扩展、通信网关和自定义 Provider。

## 环境要求

- 从源码构建需要 Go **1.25.0** 或更高版本。
- 至少配置一个模型 Provider 的 API Key 或登录凭据。
- 推荐安装 Git，以启用检查点、Worktree、Review 和仓库感知能力。
- Chrome 或 Chromium 为可选依赖，用于完整的浏览器工具体验。

macOS、Linux 和 Windows 上的部分能力可能有所不同。可以运行 `covo-agent doctor` 检查当前环境。

## 安装

一条命令安装最新版本：

```bash
curl -fsSL https://raw.githubusercontent.com/covoyage/covo-agent/main/install.sh | bash
```

脚本会根据你的操作系统和架构，从 GitHub Releases 页面下载对应二进制，安装到 `~/.covo-agent/bin` 并加入 `PATH`。如需安装指定版本，可传 `--version <版本号>`；如不想修改 shell 配置文件，可加 `--no-modify-path`。

macOS 或 Linux 可通过 Homebrew 安装：

```bash
brew install --cask covoyage/tap/covo-agent
```

Windows 可通过 Scoop 安装：

```powershell
scoop bucket add covoyage https://github.com/covoyage/scoop-bucket
scoop install covoyage/covo-agent
```

GitHub Releases 页面也会提供归档文件和校验和。

通过 Go 安装最新版本：

```bash
go install github.com/covoyage/covo-agent/cmd/covo-agent@latest
```

二进制会安装到 `GOBIN`；未设置 `GOBIN` 时，则安装到 `$(go env GOPATH)/bin`。请确保对应目录已加入 `PATH`。

### 从源码构建

源码构建使用 `go.mod` 中声明的依赖目录结构。准备好其中引用的本地模块后，再克隆并构建项目：

```bash
mkdir covoyage && cd covoyage
git clone https://github.com/covoyage/covo-agent.git
cd covo-agent

go build -o bin/covo-agent ./cmd/covo-agent
```

可选：将二进制安装到 `PATH`：

```bash
install -m 0755 bin/covo-agent "$HOME/.local/bin/covo-agent"
```

## 快速开始

配置 Provider 和默认模型：

```bash
covo-agent setup
# 后续也可以重新打开 Provider/模型选择器
covo-agent model
```

也可以显式管理凭据：

```bash
covo-agent auth add OPENAI_API_KEY=your-key
covo-agent auth list
```

进入项目目录并启动交互式 TUI：

```bash
cd your-project
covo-agent
```

不启动 TUI，执行一次任务：

```bash
covo-agent --oneshot "总结当前仓库"
covo-agent -z "审查未提交的修改" --json
```

运行受约束的无头任务：

```bash
covo-agent --headless \
  -z "找出测试失败的原因" \
  --tools read,grep,glob,bash \
  --max-turns 8 \
  --allow 'bash:go test *' \
  --deny 'bash:rm *'
```

## 运行模式

| 模式 | 示例 | 适用场景 |
| --- | --- | --- |
| 交互式 TUI | `covo-agent` | 迭代开发、工具调用、审批和会话管理 |
| 单次运行 | `covo-agent -z "prompt"` | 脚本或一次性终端回复 |
| 单次 JSON | `covo-agent -z "prompt" --json` | 结构化自动化输出 |
| 无头模式 | `covo-agent --headless -z "prompt"` | 使用明确策略的非交互 Agent 任务 |

常用全局参数：

```text
--provider <name>          临时覆盖 Provider
--model <name>             临时覆盖模型
--mode general|code        选择 Agent 模式
--session-id <id>          恢复或创建指定会话
--sandbox <profile>        workspace、read-only、strict、devbox、off 或自定义配置
--system-prompt <text>     替换默认系统提示词
--append-system-prompt     追加系统提示词
--yolo                     跳过审批提示（高风险）
```

使用 `covo-agent --help` 查看完整且与当前版本一致的参数列表。

## 命令地图

| 范围 | 命令 |
| --- | --- |
| 初始化与诊断 | `setup`、`model`、`config`、`auth`、`doctor`、`status`、`version`、`update` |
| 会话与知识 | `session`、`memory`、`skill`、`dreaming`、`commitments`、`backup`、`restore`、`migrate` |
| 编程工作流 | `analyze`、`review`、`pr`、`testgen`、`worktree` |
| 集成能力 | `mcp`、`acp`、`lsp`、`gateway`、`pairing`、`plugin`、`ext`、`package` |
| 个性化 | `profile`、`language`、`theme`、`features`、`template` |
| 自动化 | `cron`、`heartbeat`、`completion` |

每个命令都有独立帮助页面：

```bash
covo-agent session --help
covo-agent gateway --help
covo-agent mcp --help
```

支持 Bash、Zsh、Fish 和 PowerShell 补全：

```bash
covo-agent completion zsh > "${fpath[1]}/_covo-agent"
covo-agent completion bash > ~/.local/share/bash-completion/completions/covo-agent
```

## 配置

默认全局配置文件：

```text
~/.covo-agent/config.yaml
```

项目可以通过 `.covo-agent.yaml` 覆盖全局配置。加载器从当前目录向上查找，并在 Git 根目录或用户主目录停止。

最小配置示例：

```yaml
provider: openai
model: gpt-5.6
mode: code
```

YAML 中的环境变量会被展开，因此敏感信息可以保留在配置文件之外：

```yaml
custom_providers:
  - name: Local
    protocol: openai/chat
    base_url: ${CUSTOM_BASE_URL}
    api_key_env: CUSTOM_API_KEY
```

通过 `covo-agent auth` 管理的凭据保存在 `~/.covo-agent/.env`，文件使用受限权限。项目环境变量和进程环境变量可以覆盖配置值。

Profile 使用独立的数据目录：

```text
~/.covo-agent/profiles/<profile>/
```

可以设置 `COVO_PROFILE`，或使用 `profile` 命令选择 Profile。

## 数据目录

| 路径 | 用途 |
| --- | --- |
| `~/.covo-agent/config.yaml` | 全局配置 |
| `~/.covo-agent/.env` | Provider 凭据和环境变量 |
| `.covo-agent.yaml` | 项目级覆盖配置 |
| `~/.covo-agent/sessions/` | 持久会话和生命周期 Sidecar |
| `~/.covo-agent/skills/` | 已安装及用户创建的技能 |
| `~/.covo-agent/covo-agent.log` | 交互运行告警和诊断日志 |
| `~/.covo-agent/profiles/` | 隔离的 Profile 数据 |

## 安全机制

可能修改文件、执行命令或影响外部系统的工具操作，会经过审批和策略层。自动化场景建议使用明确的 allow/deny 规则和沙箱配置。

```bash
covo-agent --headless -z "运行测试并修复一个失败" \
  --sandbox workspace \
  --allow 'edit:*' \
  --allow 'bash:go test *' \
  --deny 'bash:git push*'
```

`--yolo` 会关闭危险操作审批。只应在允许工具不受限制执行的隔离环境中使用。

Telemetry 默认为关闭。设置 `COVO_OTEL_ENDPOINT` 或 `COVO_OTEL_ENABLED=true` 后启用。审计和 Telemetry 数据在导出前会经过敏感信息脱敏。

## 故障排查

检查环境和配置：

```bash
covo-agent doctor
covo-agent doctor --fix
```

检查内容包括：

- Home、配置、凭据、会话和技能目录；
- Provider Key 和模型配置；
- Git 与浏览器可用性；
- 终端颜色、剪贴板、Multiplexer 和键盘能力。

交互模式下的告警会写入 `~/.covo-agent/covo-agent.log`，避免破坏 Alternate Screen TUI 的输入框和布局。

## 开发

构建前，请确保工作区中存在 `go.mod` 声明的本地模块路径。

```bash
go build ./...
go test ./...
go vet ./...
```

修改后构建可执行文件：

```bash
go build -o bin/covo-agent ./cmd/covo-agent
```

提交修改前，请运行上面的构建、测试和检查命令，并重新生成可执行文件。

## 许可证

本项目采用 [GNU Affero General Public License v3.0](LICENSE)（`AGPL-3.0`）。
