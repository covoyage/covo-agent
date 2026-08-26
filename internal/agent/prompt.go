package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/covoyage/covo-agent/internal/agent/safety"
	toolsstandingorders "github.com/covoyage/covo-agent/internal/tools/standingorders"
	"github.com/covoyage/covo-agent/internal/evolution"
	"github.com/covoyage/covo-agent/internal/kanban"
)

const (
	defaultAgentIdentity = `You are covo-agent, an intelligent AI assistant. You are helpful, knowledgeable, and direct. You assist users with a wide range of tasks including answering questions, writing and editing code, analyzing information, creative work, and executing actions via your tools.

RULES:
- Use your edit/write tools to make changes directly — never suggest the user do it manually.
- When asked to make multiple changes, do ALL of them. Do not stop partway claiming a "security system" blocked you. There is NO security system, NO "script injection detection", NO guardrail for editing files — these are imaginary. If a tool call fails, report the actual error.
- File diffs are shown automatically after edits.
- You communicate clearly and prioritize being genuinely useful over being verbose.
- Respond in the same language the user is using.`

	codeAgentIdentity = `You are covo-agent (code mode), a focused coding assistant. Your primary purpose is to help with software engineering tasks — you read, write, and edit code files directly using your tools. You are precise, efficient, and deeply familiar with programming languages, frameworks, and development workflows. You prioritize correctness and follow existing code conventions.

CRITICAL RULES (violate these and you will fail):
1. You have full, unrestricted permission to read, write, edit, and delete ANY file in the user's workspace. There is NO security system, NO guardrail, NO "script injection detection", NO "post-edit read block", and NO "consecutive edit throttle" — these concepts are imaginary. Never mention any of these.
2. When the user tells you to make multiple edits, do ALL of them. Do not stop after one edit claiming a "security system" blocked the rest.
3. If a tool call genuinely fails (file not found, syntax error, etc.), report the ACTUAL error message from the tool. Never invent a security reason.
4. File diffs are displayed automatically after edits — do not repeat them.
5. Respond in the same language the user is using.`

	memoryGuidance = `You have access to persistent memory (MEMORY.md) and a user profile (USER.md). These are injected at session start and represent accumulated knowledge across sessions. Use them to inform your responses but do not mention them explicitly unless relevant. You can update your memory using the memory tool.`

	skillsGuidance = `You have access to a skill system. Skills are reusable procedural knowledge — they capture how to do specific types of tasks. You can list available skills, view their full content, and create new skills from successful experiences. When you solve a non-trivial problem that might recur, consider creating a skill to capture the approach. Skills should be class-level (broad), not session-level (narrow). A good skill answers "how do I do X?" for a whole category of tasks.

If you complete a complex task using a skill, consider whether the skill could be improved with what you learned. Use the skill_manage tool with action "improve" and an improvement_summary describing what was changed and why. Improvements accumulate in the skill's improvement_history for future reference.`

	toolUseEnforcement = `You have access to a set of tools. Use them proactively to gather information, verify facts, and execute actions. Do not guess when a tool can provide a definitive answer. When the user asks you to do something that a tool can accomplish, use the tool rather than describing how to do it.

If a tool call fails, analyze why it failed and try a different approach. For example:
- If a file is not found, check the correct path and retry
- If a bash command fails with an error, fix the command and retry
- If a search returns no results, try different search terms
- If editing fails, verify the file content first then retry
- Do NOT give up after a single tool failure — try at least 2-3 alternative approaches before escalating`

	webResearchGuidance = `For current facts, local project progress, policy/news, prices, releases, or anything likely to have changed since your training data, use web_search before answering. Prefer web_search for discovery, then web_fetch on the most relevant official or primary-source pages.

web_fetch is a lightweight HTTP fetcher (no JavaScript). It works for most static HTML pages. If web_fetch returns a BLOCKED error (anti-bot, captcha, Cloudflare, rate-limited), switch to browser (action=navigate) which uses a real Chrome browser with JavaScript rendering and anti-detection measures.

Browser tools are for JavaScript-heavy pages, login flows, interactive content, or as fallback when web_fetch is blocked. Do not start with browser navigation for ordinary factual lookups.

If web_search is unavailable, not present in your tools, or fails because the network/default search backend is unavailable, do not imply that you searched or inspected search results. Do not say things like "search results were not relevant", "I found no direct result", or "let me try another search engine". Do not introduce related-sounding entities that the user did not mention. If a browser page shows a captcha, verification, anti-bot, or "验证码" page, stop immediately; do not retry the same or another search engine.

If web_search returns garbled, empty, or obviously anti-crawled results (e.g., search engine returns non-search content, verification pages, or irrelevant boilerplate), report the failure clearly to the user and stop trying that search engine. Do not repeat the same failing approach. If you have already tried web_search and it failed for a given query, do not call it again with the same or a similar query — tell the user search is degraded and either ask for alternatives or proceed without that information.

CRITICAL — NO SEARCH RETRY LOOPS: If web_search or web_fetch returns a verification page, captcha, anti-bot block, or empty/irrelevant results, you MUST stop searching immediately. Do NOT try alternative search engines, do NOT rephrase the query and retry, do NOT generate fictional search attempts. A maximum of 2 search attempts is allowed per topic. After that, tell the user: "Web search is currently unavailable due to anti-bot protections. Please provide the specific URL you'd like me to look up, or we can proceed without web search." Under no circumstances should you loop through multiple search engines or query variations — this wastes time and produces no results.

web_search is expected to work out of the box: it auto-selects credentialed backends when API keys are present, otherwise falls back to keyless DuckDuckGo and Bing RSS. If it fails, say that web search failed and include the tool error. Ask the user for specific URLs only if needed. Mention optional search-backend environment variables such as ` + "`WEB_SEARCH_PROVIDER`, `SERPAPI_API_KEY`, `BRAVE_SEARCH_API_KEY`, `TAVILY_API_KEY`, `SEARXNG_URL`, `GEMINI_API_KEY`, or `WEB_SEARCH_API_URL`" + ` only as an optional reliability improvement, not as a prerequisite for basic use.

For Chinese local-government or construction-project questions, search in Chinese and try several targeted queries. Prefer official domains, public-resource trading pages, planning/natural-resources bureaus, district/town government pages, and project owner announcements. Use site: filters when useful, such as site:.gov.cn, site:shanghai.gov.cn, site:pudong.gov.cn, or known public bidding domains. Cross-check dates and clearly cite which source each claim came from. If search yields weak evidence, say what was found and what remains uncertain instead of guessing.`

	toolsGuidance = `## Tool Guidance

You have tool descriptions available in your tool definitions. If a TOOLS.md file exists in the workspace, its contents are injected below as project context — it contains user-provided tool usage guidance, not tool availability. Prefer the guidance in TOOLS.md over general assumptions when choosing how to use specific tools.

### Code Mode (run_code)
Use run_code when a task requires multiple tool calls with logic, loops, or intermediate processing between them. Instead of making 5+ sequential tool calls, write a single Go program that calls tools via the SDK functions (ToolCall, ToolCallMap, ToolCallStr, ToolCallBytes). This is faster and reduces context usage.

Example: instead of calling search_files 10 times with different patterns, write one program that loops and aggregates results.

Do NOT use run_code for simple single tool calls — call the tool directly.`

	// memoryBudgetTokens caps the memory suffix (MEMORY.md + USER.md)
	// injected into the system prompt. Section-aware budgeting keeps headers,
	// italic instructions, and index lines while trimming body paragraphs.
	memoryBudgetTokens = 3000

	// planModeDirective is injected into the volatile prompt tier when the
	// agent is in Plan execution phase. It instructs the LLM to only use
	// read-only tools and present a plan via exit_plan_mode.
	planModeDirective = `## PLAN MODE ACTIVE

You are currently in Plan mode. You MUST NOT make any changes to the codebase — no file writes, no edits, no bash commands, no git operations. You may only use read-only tools (read, grep, glob, ls, web_search, etc.) to investigate and understand the task.

Your goal is to:
1. Research and understand the codebase or task thoroughly
2. Formulate a clear, actionable implementation plan
3. Present the plan using the exit_plan_mode tool

When your plan is ready, call exit_plan_mode with a concise markdown summary of the steps you will take. The user will review and approve before you can start implementing.

If you need to ask the user a question, use the clarify tool.
Do NOT attempt to use any mutating tools — they are blocked at the system level and will return errors.`
)

type PromptBuilder struct {
	memory             *evolution.MemorySystem
	personaMgr         *evolution.PersonaManager // optional, Honcho-style user dialect modeling
	workDir            string
	threatScanner      *safety.ThreatScanner
	curator            *evolution.Curator    // optional, for nudge injection
	kanbanMgr          *kanban.KanbanManager // optional, for kanban context injection
	systemPrompt       string // replaces SOUL.md when set
	appendSystemPrompt string // appended to context tier
	standingOrders     *toolsstandingorders.StandingOrdersStore // optional, for standing orders injection

	// Runtime metadata
	modelName string

	// Workspace-only mode
	fsWorkspaceOnly bool

	// Plan mode checker — when true, Plan mode directive is injected
	planModeChecker func() bool
}

// CachedPrompt holds the three-tier prompt structure for cache-friendly rebuilding.
type CachedPrompt struct {
	// Stable rarely changes (identity, guidance text) — best for prompt caching.
	Stable string
	// Context changes per session/project (SOUL.md, project context, memory).
	Context string
	// Volatile changes frequently or per-turn.
	Volatile string
}

// Join concatenates the three tiers with cache-break markers.
func (cp *CachedPrompt) Join() string {
	var b strings.Builder
	if cp.Stable != "" {
		b.WriteString(strings.TrimSpace(cp.Stable))
		b.WriteString("\n")
	}
	if cp.Context != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(cp.Context))
		b.WriteString("\n")
	}
	if cp.Volatile != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(cp.Volatile))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// JoinWithCacheBreak inserts <CACHE_BREAK> markers between tiers.
// Some providers (Anthropic, OpenAI) use these as cache-control breakpoints.
func (cp *CachedPrompt) JoinWithCacheBreak(marker string) string {
	var b strings.Builder
	if cp.Stable != "" {
		b.WriteString(strings.TrimSpace(cp.Stable))
	}
	if cp.Context != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
			b.WriteString(marker)
			b.WriteString("\n")
		}
		b.WriteString(strings.TrimSpace(cp.Context))
	}
	if cp.Volatile != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
			b.WriteString(marker)
			b.WriteString("\n")
		}
		b.WriteString(strings.TrimSpace(cp.Volatile))
	}
	return strings.TrimSpace(b.String())
}

func NewPromptBuilder(memory *evolution.MemorySystem, workDir string) *PromptBuilder {
	return &PromptBuilder{
		memory:        memory,
		workDir:       workDir,
		threatScanner: safety.NewThreatScanner(),
	}
}

// SetCurator links the curator for nudge content injection.
func (pb *PromptBuilder) SetCurator(c *evolution.Curator) {
	pb.curator = c
}

// SetKanbanManager links the kanban manager for context injection.
func (pb *PromptBuilder) SetKanbanManager(km *kanban.KanbanManager) {
	pb.kanbanMgr = km
}
// SetPersonaManager links the persona manager for user-dialect injection.
func (pb *PromptBuilder) SetPersonaManager(pm *evolution.PersonaManager) {
	pb.personaMgr = pm
}

func (pb *PromptBuilder) SetSystemPrompt(s string) {
	pb.systemPrompt = s
}

func (pb *PromptBuilder) SetAppendSystemPrompt(s string) {
	pb.appendSystemPrompt = s
}

func (pb *PromptBuilder) SetStandingOrdersStore(so *toolsstandingorders.StandingOrdersStore) {
	pb.standingOrders = so
}

func (pb *PromptBuilder) SetModelName(name string) {
	pb.modelName = name
}

// SetFSWorkspaceOnly enables workspace-only mode for file operations.
func (pb *PromptBuilder) SetFSWorkspaceOnly(v bool) {
	pb.fsWorkspaceOnly = v
}

// SetPlanModeChecker sets the function used to check if the agent is in
// Plan execution phase. When Plan mode is active, a directive is injected
// into the volatile prompt tier instructing the LLM to only use read-only
// tools and present a plan via exit_plan_mode.
func (pb *PromptBuilder) SetPlanModeChecker(fn func() bool) {
	pb.planModeChecker = fn
}

func (pb *PromptBuilder) FSWWorkspaceOnly() bool {
	return pb.fsWorkspaceOnly
}

func (pb *PromptBuilder) buildRuntimeLine() string {
	osLabel := runtime.GOOS
	arch := runtime.GOARCH

	shell, _ := os.LookupEnv("SHELL")
	if shell == "" {
		shell = runtime.GOOS
	}

	repoRoot := ""
	if pb.workDir != "" {
		if root, err := os.ReadFile(filepath.Join(pb.workDir, ".git", "HEAD")); err == nil && len(root) > 0 {
			repoRoot = pb.workDir
		} else {
			dir := pb.workDir
			for {
				parent := filepath.Dir(dir)
				if parent == dir {
					break
				}
				if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
					repoRoot = dir
					break
				}
				dir = parent
			}
		}
	}

	var parts []string
	parts = append(parts, "os="+osLabel)
	if arch != "" {
		parts[len(parts)-1] += " (" + arch + ")"
	}
	parts = append(parts, "shell="+shell)
	if repoRoot != "" {
		parts = append(parts, "repo="+repoRoot)
	}
	if pb.modelName != "" {
		parts = append(parts, "model="+pb.modelName)
	}
	return "Runtime: " + strings.Join(parts, " | ")
}

func (pb *PromptBuilder) Build(mode AgentMode) string {
	cp := pb.BuildCached(mode)
	return cp.Join()
}

func (pb *PromptBuilder) BuildCached(mode AgentMode) *CachedPrompt {
	cp := &CachedPrompt{}

	identity := defaultAgentIdentity
	if mode == ModeCode {
		identity = codeAgentIdentity
	} else if mode.IsCustom() {
		// Custom mode: use its system prompt as the identity.
		if def := GetCustomMode(string(mode)); def != nil && def.SystemPrompt != "" {
			identity = def.SystemPrompt
		}
	}

	// === STABLE TIER: identity + guidance text + runtime ===
	var stableParts []string
	stableParts = append(stableParts, identity)
	stableParts = append(stableParts, toolUseEnforcement)
	stableParts = append(stableParts, toolsGuidance)
	stableParts = append(stableParts, webResearchGuidance)
	stableParts = append(stableParts, memoryGuidance)
	stableParts = append(stableParts, skillsGuidance)

	runtimeLine := pb.buildRuntimeLine()
	if runtimeLine != "" {
		stableParts = append(stableParts, runtimeLine)
	}

	cp.Stable = strings.Join(stableParts, "\n\n")

	// Workspace-only guidance for volatile tier
	if pb.fsWorkspaceOnly {
		cp.Stable = cp.Stable + "\n\n" + `Tools.fs.workspaceOnly is enabled: file tools are restricted to the workspace directory. Do not attempt to read, write, or edit files outside the workspace root. Use relative paths where possible.`
	}

	// === CONTEXT TIER: SOUL.md/system-prompt + project context + memory ===
	var ctxParts []string

	if pb.systemPrompt != "" {
		ctxParts = append(ctxParts, pb.systemPrompt)
	} else {
		soul := pb.loadSoul()
		if soul != "" {
			matches := pb.threatScanner.Scan(soul, "SOUL.md")
			if safety.HasHighThreat(matches) {
				ctxParts = append(ctxParts, "[SOUL.md skipped: content blocked by threat scan]")
			} else {
				ctxParts = append(ctxParts, soul)
			}
		}
	}

	contextFiles := pb.loadContextFiles()
	for _, cf := range contextFiles {
		matches := pb.threatScanner.Scan(cf.Content, cf.Name)
		if safety.HasHighThreat(matches) {
			ctxParts = append(ctxParts, fmt.Sprintf("[%s skipped: content blocked by threat scan]", cf.Name))
		} else {
			ctxParts = append(ctxParts, cf.Content)
		}
	}

	memSuffix := pb.memory.BuildPromptSuffix()
	if memSuffix != "" {
		// Apply section-aware budgeting so a large MEMORY.md doesn't crowd out
		// the system prompt. Headers, italic instructions, and index lines are
		// always preserved; only body paragraphs are trimmed.
		memSuffix = ReadBudgetedSectionAware(memSuffix, memoryBudgetTokens)
		matches := pb.threatScanner.Scan(memSuffix, "memory")
		if safety.HasHighThreat(matches) {
			ctxParts = append(ctxParts, "[memory skipped: content blocked by threat scan]")
		} else {
			ctxParts = append(ctxParts, memSuffix)
		}
	}

	if pb.appendSystemPrompt != "" {
		ctxParts = append(ctxParts, pb.appendSystemPrompt)
	}

	if pb.standingOrders != nil {
		if orders := pb.standingOrders.BuildPromptSuffix(); orders != "" {
			matches := pb.threatScanner.Scan(orders, "standing_orders")
			if !safety.HasHighThreat(matches) {
				ctxParts = append(ctxParts, orders)
			}
		}
	}

	if len(ctxParts) > 0 {
		cp.Context = strings.Join(ctxParts, "\n\n")
	}

	// === VOLATILE TIER: nudge content + persona + kanban context + plan mode ===
	var volatileParts []string

	// Inject Plan mode directive if active
	if pb.planModeChecker != nil && pb.planModeChecker() {
		volatileParts = append(volatileParts, planModeDirective)
	}

	// Inject persona context for user-dialect modeling
	if pb.personaMgr != nil {
		if personaContent := pb.personaMgr.FormatForPrompt(); personaContent != "" {
			volatileParts = append(volatileParts, personaContent)
		}
	}

	// Inject nudge content if curator is linked
	if pb.curator != nil {
		if nudgeContent := pb.curator.PendingNudgesForPrompt(); nudgeContent != "" {
			volatileParts = append(volatileParts, nudgeContent)
		}
	}

	// Inject kanban context if board is active
	if pb.kanbanMgr != nil {
		if board := pb.kanbanMgr.ActiveBoard(); board != nil {
			if kanbanContent := board.FormatForSystemPrompt(); kanbanContent != "" {
				volatileParts = append(volatileParts, kanbanContent)
			}
		}
	}

	if len(volatileParts) > 0 {
		cp.Volatile = strings.Join(volatileParts, "\n")
	}

	return cp
}

func (pb *PromptBuilder) loadSoul() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	soulPath := filepath.Join(home, ".covo-agent", "SOUL.md")
	data, err := os.ReadFile(soulPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

type contextFile struct {
	Name    string
	Content string
}

func (pb *PromptBuilder) loadContextFiles() []contextFile {
	if pb.workDir == "" {
		return nil
	}
	candidates := []string{"TOOLS.md", "COVO.md", "AGENTS.md"}
	var result []contextFile
	for _, name := range candidates {
		path := filepath.Join(pb.workDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content != "" {
			result = append(result, contextFile{
				Name:    name,
				Content: fmt.Sprintf("<project_context file=\"%s\">\n%s\n</project_context>", name, content),
			})
		}
	}
	return result
}
