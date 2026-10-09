# Tool Catalog

Auto-checked by TestToolCatalogCoversRegistry: every registered tool must have a row here.
Regenerate with: go test ./internal/agent -run TestToolCatalog -update-tool-catalog

Cyber HTTP evidence storage is capped at 500 exchanges. Retention preserves finding references, pending/running intake references, and the baseline chains of retained exchanges before filling remaining capacity with recent records. A new record is rejected if preserving referenced evidence would exceed the cap; completed/failed intake jobs alone do not pin evidence. Finding validation and intake enqueueing serialize with retention within one process, not across separate processes.

Cyber ledgers, reports, and full-output spills use directory-confined filesystem operations. Storage directories use mode `0700` and files use `0600`; initialization also tightens existing output files. Symbolic links in the storage tree are rejected rather than read or migrated. Spill filenames are unique, so repeated output from the same tool does not overwrite prior evidence. This storage boundary does not sandbox external scanner processes.

HTTP capture and its cache are local to each tool instance, and captured request headers are preserved for authenticated replay. Exchanges persist header/body truncation flags. Replaying incomplete or redacted requests requires explicit complete replacements (`replace_headers: true` and/or `body` as appropriate); CSRF retests require complete original requests. Identity comparisons preflight both sides and reject recognized credentials in URLs/bodies, unclassified captured headers, and opaque bodies instead of silently reusing baseline authentication. Application-specific authentication still needs independently verified test identities. CSRF retests use a public-suffix-aware cookie jar for new, rotated, deleted, and expired cookies; recorded Cookie headers have no original attributes, so their initial scope is the recorded origin with path `/`.

Finding updates validate merged evidence and cannot change the target, established endpoint, or method under the same ID. Use a new finding for a different identity.

| 工具 | 功能 | 并发声明 |
|------|------|----------|
| `advanced_workflow` | Orchestrate a multi-phase workflow with advanced features: - Conditional phase execution (skip phases based on prior ... | undeclared |
| `append_file` | Append content to a file without overwriting existing content. | undeclared |
| `apply_patch` | Apply a structured V4A format patch to modify files in the workspace. | undeclared |
| `ask_user` | Ask the user a question and block until they answer. | undeclared |
| `asset_upsert` | Record or update an asset discovered during recon. | undeclared |
| `audit_query` | Query the persistent audit log of agent tool calls and lifecycle events. | safe |
| `audit_read` | Read file contents like 'read', but with automatic sensitive-path blocking. | safe |
| `bash` | Execute a bash command in the current working directory. | undeclared |
| `browser` | Control the agent's Chromium session for page interaction: navigate, snapshot, click, type, scroll. | undeclared |
| `canvas` | Render HTML, SVG, or Mermaid content in a browser for visual inspection. | undeclared |
| `clarify` | Ask the user a clarifying question and wait for their answer before proceeding. | undeclared |
| `close_agent` | Explicitly close a completed or interrupted sub-agent. | undeclared |
| `code_graph` | Build a dependency graph of Go packages in the workspace (Go only, not a general code graph). | safe |
| `code_navigate` | Precise code navigation and diagnostics via the language server (LSP). | undeclared |
| `computer_use` | Control visible desktop apps via accessibility APIs. | undeclared |
| `context_compress` | Check if the conversation context is approaching token limits and trigger automatic summarization of older messages. | undeclared |
| `context_compress_config` | View or update the context compression threshold. | undeclared |
| `create_goal` | Create a new session-level goal. | undeclared |
| `cronjob` | Manage scheduled cron jobs that run agent prompts on a recurring schedule. | undeclared |
| `dashboard` | Show a live monitoring panel of all subagents: active, completed, and failed. | undeclared |
| `delete` | Delete a file or directory. Files and directories are moved to the platform trash so they can be restored. | undeclared |
| `deploy` | Deploy a static site or application to a target environment. | undeclared |
| `diffs` | Generate a unified diff between two texts, or apply a unified patch. | undeclared |
| `diff_scope` | Configure or inspect a git diff-scoped security review. | undeclared |
| `edit` | Edit a single file using exact text replacement. | undeclared |
| `edit_block` | Apply targeted text replacements to a file using block matching. | undeclared |
| `enter_worktree` | Create and switch to an isolated git worktree. | undeclared |
| `execute_code` | Execute Python code in a subprocess and return the output. | undeclared |
| `exit_plan_mode` | Use this tool when you have finished presenting your plan and are ready to code. | undeclared |
| `exit_worktree` | Remove the current worktree and return to the original working copy. | undeclared |
| `external_agent` | Delegate a standalone, self-contained task to an external coding agent that runs as its own process (Claude Code, Codex, or opencode). | undeclared |
| `find` | Search for files by glob pattern. | safe |
| `finding_upsert` | Record or update a security finding. | undeclared |
| `finding_triage` | Approve a similar finding as distinct or link it to an existing finding as a duplicate. | undeclared |
| `coverage_upsert` | Record which surface was tested, the outcome, and why it stopped. | undeclared |
| `exchange_list` | List recorded HTTP exchanges. | undeclared |
| `exchange_get` | Fetch one recorded HTTP exchange by id. | undeclared |
| `web_context` | Summarize bounded endpoint evidence, coverage, and intake status without raw requests or credentials. | undeclared |
| `csrf_retest` | Refresh a CSRF token via recorded GET and submit a same-origin recorded form POST without returning or storing the new token. | undeclared |
| `exchange_label` | Attach identity, role, tenant, resource, owner, or baseline labels to a recorded HTTP exchange. | undeclared |
| `exchange_search` | Search recorded HTTP exchanges by method, host, path, parameter, status, or text. | undeclared |
| `exchange_replay` | Replay a recorded HTTP exchange from a spawned hands-on cyber role and save the response as new evidence. | undeclared |
| `exchange_compare` | Compare a captured request under two authentication contexts and save both responses as evidence. | undeclared |
| `web_param_probe` | Check one captured GET query parameter for reflection with an inert marker; returns evidence, not a vulnerability verdict. | undeclared |
| `request_intake` | Enable exact-host request-shape intake and view verifier dispatch jobs. | undeclared |
| `assessment` | View asset-by-risk coverage and unconfirmed chain candidates. | undeclared |
| `remote_nodes` | Register, remove, or list named SSH execution nodes. | undeclared |
| `remote_node_exec` | Run a command on a registered SSH node from a hands-on cyber role. | undeclared |
| `scan_profile` | Set scan depth (quick, standard, deep) and whether the target is whitebox. | undeclared |
| `finish_scan` | Close recon and emit JSON, Markdown, CSV, and SARIF reports with coverage and completeness. | undeclared |
| `generate_tests` | Suggest the conventional test file path for a source file. | undeclared |
| `get_goal` | Retrieve the current goal state for this session. | safe |
| `git_diff` | Show changes between commits, commit and working tree, etc. | safe |
| `git_log` | Show commit logs. | safe |
| `git_status` | Show the working tree status. | safe |
| `glob` | Find files matching a glob pattern. | safe |
| `grep` | Search file contents for a pattern. | safe |
| `human_handoff` | Pause and wait for the user to provide input or instructions. | undeclared |
| `inbox_check` | Check and drain the current session's persistent inbox. | undeclared |
| `inbox_send` | Send an asynchronous message to another session's persistent inbox. | undeclared |
| `interrupt_agent` | Interrupt a running sub-agent by cancelling its context. | undeclared |
| `kanban` | Persistent kanban board (survives restarts). | undeclared |
| `live_canvas` | Start or update a Live Canvas -- a real-time visual workspace that updates in the browser without page reloads. | undeclared |
| `llm_task` | Call a language model independently with a custom prompt. | undeclared |
| `ls` | List directory contents. | safe |
| `mcp` | Manage MCP (Model Context Protocol) server connections and call their tools. | undeclared |
| `memory` | Primary long-term memory. | undeclared |
| `memory_dream` | Consolidate scattered memory entries into higher-level insights and store them. | undeclared |
| `memory_dream_stats` | View dream memory lifecycle statistics. | safe |
| `memory_extract` | Extract structured facts from recent conversation turns and store them as insights. | undeclared |
| `memory_forget` | Delete an in-session memory by ID (not MEMORY.md). | undeclared |
| `memory_recall` | Search this session's in-memory notes (lost on restart). | safe |
| `memory_semantic_forget` | Delete an embedding knowledge-base record by id. | undeclared |
| `memory_semantic_search` | Search the embedding knowledge base (not session memory, not MEMORY.md). | undeclared |
| `memory_semantic_stats` | Stats for the embedding knowledge base (count and file size). | undeclared |
| `memory_semantic_store` | Store text in the embedding knowledge base. | undeclared |
| `memory_store` | Save a note in this session only (lost on restart). | undeclared |
| `moa` | Mixture of Agents — queries one or more LLMs in parallel and synthesizes the best answer. | undeclared |
| `monitor` | Monitor a long-running process and stream its output line-by-line. | undeclared |
| `move` | Move or rename a file or directory. | undeclared |
| `open_url` | Open a URL in the user's default system browser (visible window: Safari/Chrome/Firefox). | undeclared |
| `parse_duration` | Parse human-readable duration strings into milliseconds. | undeclared |
| `patch` | Apply a patch to a file by replacing an exact string with another. | undeclared |
| `process` | Manage background processes (spawned via the 'spawn' action). | undeclared |
| `reaction` | Show an emoji reaction in this conversation UI. | undeclared |
| `read` | Read a file, image, PDF, Jupyter notebook, or video. | safe |
| `remote_exec` | Execute commands on a remote machine via SSH. | undeclared |
| `run_code` | Execute a complete Go program with access to all agent tools. | undeclared |
| `scan` | Run a scan recipe; locally, a missing curl binary falls back to one scoped native GET without extra flags. Other recipes still need their binaries. | undeclared |
| `script_workflow` | Compile and run a checkable workflow script. | undeclared |
| `script_workflow` | Compile and run a checkable workflow script. | undeclared |
| `sandbox` | Run shell commands in the configured sandbox. | undeclared |
| `search_files` | Preferred workspace search: find files by name pattern AND/OR search file contents. | safe |
| `secrets` | Resolve a named secret into the process environment without putting the value into the conversation. | undeclared |
| `send_input` | Send a message to a running sub-agent while it's executing. | undeclared |
| `send_message` | Send a desktop notification to the user's screen. | undeclared |
| `serial` | Serial/UART communication for embedded hardware. | undeclared |
| `session_search` | Search past conversation sessions (not the workspace). | safe |
| `sessions_fork` | Fork the current conversation into a subagent that inherits the FULL parent conversation history. | undeclared |
| `sessions_history` | Read bounded, redacted conversation history from past sessions. | safe |
| `sessions_list` | List recent sessions with title, start time, and preview. | safe |
| `sessions_send` | Send a message to another session or notification channel. | undeclared |
| `sessions_spawn` | Spawn a child agent session that works on a subtask independently and reports back. | undeclared |
| `sessions_spawn_batch` | Spawn MULTIPLE child agent sessions in PARALLEL to execute independent subtasks. | undeclared |
| `sessions_wait` | Wait for sub-agent activity. | undeclared |
| `sessions_yield` | End the current turn after background sessions_spawn calls. | undeclared |
| `skill` | Load a skill's full instructions into context. | undeclared |
| `skill_bundle` | Manage skill bundles — aliases that load multiple skills together. | undeclared |
| `skill_config` | Manage skill configuration variables. | undeclared |
| `skill_manage` | Create, update, and delete skills. | undeclared |
| `skill_script` | Execute a script file from a skill's scripts/ directory, or list available scripts. | undeclared |
| `skill_workshop` | Create and inspect Skill Workshop proposals. | undeclared |
| `spill` | Store a block of text to disk (in the spill store) and return its path. | undeclared |
| `spill_list` | List text previously stored with the spill tool, newest first, for the current session. | undeclared |
| `standing_orders` | Manage persistent behavioral instructions that are injected into every session. | undeclared |
| `stop_canvas` | Stop the Live Canvas server and close all WebSocket connections. | undeclared |
| `structured_output` | Return a final, structured answer when the task is complete. | undeclared |
| `subagents` | List active and recent subagent runs spawned by this session. | undeclared |
| `swarm` | Manage a kanban-style task board for multi-agent swarm orchestration. | undeclared |
| `swarm_orchestrate` | Orchestrate a swarm of sub-agents to work on tasks in parallel. | undeclared |
| `terminal_close` | Close a persistent terminal session. | undeclared |
| `terminal_list` | List persistent terminal sessions and their status (running or exited). | safe |
| `terminal_open` | Start a persistent interactive shell session in a PTY. | undeclared |
| `terminal_read` | Read new output from a persistent terminal session since the last read. | safe |
| `terminal_send` | Write input to a persistent terminal session. | undeclared |
| `terminal_signal` | Send a signal (SIGINT, SIGTERM, SIGHUP, SIGQUIT, SIGKILL) to a persistent terminal session's foreground process group. | undeclared |
| `todo` | Session-only task list (lost on restart). | undeclared |
| `tool_call` | Invoke a tool discovered via 'tool_search' by its name with the given arguments. | undeclared |
| `tool_describe` | Load the full JSON schema for a tool returned by 'tool_search'. | safe |
| `tool_search` | Search for available tools by keyword or capability description. | safe |
| `threat_model` | Read, save, amend, or list the engagement's shared threat model. | undeclared |
| `transcribe` | Transcribe speech from audio or video files to text. | undeclared |
| `update_goal` | Update the status of the current session goal. | undeclared |
| `update_plan` | Ordered plan for the current task (not a durable tracker; use todo for this turn, kanban/goal to persist). | undeclared |
| `view` | View directory structure as a tree. | safe |
| `vision_analyze` | Analyze an image using a vision-capable LLM. | safe |
| `web_fetch` | Fetch content from a URL with HTTP GET and return readable text. | safe |
| `web_search` | Search the web. | safe |
| `workflow` | Orchestrate a sequential pipeline of subagent phases. | undeclared |
| `write_file` | Write content to a file. | undeclared |
