package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"

	"github.com/covoyage/covo-agent/internal/tools/capability"
	"github.com/covoyage/covo-agent/internal/tools/fileops"
	"github.com/covoyage/covonaut/agentcore"
)

// SideEffectScope is the declared blast radius of a tool. Permission,
// sandbox, and plan-mode gates should read this instead of guessing from
// the tool name.
type SideEffectScope string

const (
	ScopeNone      SideEffectScope = "none"
	ScopeWorkspace SideEffectScope = "workspace"
	ScopeGit       SideEffectScope = "git"
	ScopeNetwork   SideEffectScope = "network"
	ScopeSystem    SideEffectScope = "system"
)

// ToolContract is the static declaration for a registered tool.
type ToolContract struct {
	ReadOnly        bool
	Destructive     bool
	ConcurrentSafe  bool
	SideEffectScope SideEffectScope
	RiskLevel       string // low, medium, high
	MaxOutputBytes  int    // 0 = use the global persist threshold
	TimeoutMs       int
	RetrievalTerms  []string
}

var toolContracts = map[string]ToolContract{
	"read":               {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low", RetrievalTerms: []string{"show display inspect view file contents", "source code document image photo screenshot png jpeg line excerpt"}},
	"ls":                 {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low", RetrievalTerms: []string{"list directory folder entries children files sizes flat listing"}},
	"glob":               {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"grep":               {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"find":               {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"view":               {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low", RetrievalTerms: []string{"explore nested directories folders project hierarchy tree structure"}},
	"glob_files":         {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"search_files":       {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low", RetrievalTerms: []string{"locate source code text matches", "find content occurrences regular expression"}},
	"git_status":         {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeGit, RiskLevel: "low", RetrievalTerms: []string{"repository working tree status changes uncommitted modified tracked staged unstaged untracked conflicted paths clean"}},
	"git_diff":           {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeGit, RiskLevel: "low", RetrievalTerms: []string{"compare revisions commits staged unstaged patch added removed lines changes"}},
	"git_log":            {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeGit, RiskLevel: "low", RetrievalTerms: []string{"repository commit history revisions recent authors subjects messages hashes"}},
	"web_search":         {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNetwork, RiskLevel: "low", MaxOutputBytes: 32_000, RetrievalTerms: []string{"online internet information lookup research external websites sources pages"}},
	"web_fetch":          {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNetwork, RiskLevel: "low", MaxOutputBytes: 20_000},
	"vision_analyze":     {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"terminal_list":      {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"terminal_read":      {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"code_graph":         {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"session_search":     {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"sessions_list":      {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"sessions_history":   {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"audit_read":         {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"audit_query":        {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"get_goal":           {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"turn_history":       {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"skill_instructions": {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"tool_search":        {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"tool_describe":      {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"tool_descriptions":  {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"memory_recall":      {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"memory_context":     {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"memory_dream_stats": {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"diffs":              {ReadOnly: true, ConcurrentSafe: true, SideEffectScope: ScopeNone, RiskLevel: "low", RetrievalTerms: []string{"compare before after text strings side by side memory unified diff patch"}},
	"todo":               {ReadOnly: false, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"update_plan":        {ReadOnly: false, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"clarify":            {ReadOnly: true, SideEffectScope: ScopeNone, RiskLevel: "low", RetrievalTerms: []string{"ask question clarification missing value preference choices user answer"}},
	"ask_user":           {ReadOnly: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"exit_plan_mode":     {ReadOnly: true, SideEffectScope: ScopeNone, RiskLevel: "low"},
	"edit":               {Destructive: true, SideEffectScope: ScopeWorkspace, RiskLevel: "medium", RetrievalTerms: []string{"modify revise replace text", "targeted source code changes"}},
	"edit_block":         {Destructive: true, SideEffectScope: ScopeWorkspace, RiskLevel: "medium"},
	"write_file":         {Destructive: true, SideEffectScope: ScopeWorkspace, RiskLevel: "medium", RetrievalTerms: []string{"save create overwrite file contents"}},
	"append_file":        {Destructive: true, SideEffectScope: ScopeWorkspace, RiskLevel: "medium", RetrievalTerms: []string{"append add extend attach text lines end tail existing preserve content"}},
	"apply_patch":        {Destructive: true, SideEffectScope: ScopeWorkspace, RiskLevel: "medium"},
	"delete":             {Destructive: true, SideEffectScope: ScopeWorkspace, RiskLevel: "high", RetrievalTerms: []string{"remove trash discard erase files directories obsolete backup"}},
	"bash":               {SideEffectScope: ScopeSystem, RiskLevel: "high", MaxOutputBytes: 32_000, TimeoutMs: 120_000, RetrievalTerms: []string{"run execute shell terminal command pipeline stdout stderr exit code timeout environment"}},
	"execute_code":       {SideEffectScope: ScopeSystem, RiskLevel: "high", MaxOutputBytes: 32_000},
	"tool_call":          {SideEffectScope: ScopeSystem, RiskLevel: "high"},
	"script_workflow":    {SideEffectScope: ScopeWorkspace, RiskLevel: "medium"},
}

var additionalToolRetrievalTerms = map[string][]string{
	"memory":        {"persist remember recall forget durable user project facts preferences notes"},
	"move":          {"relocate rename change file directory path preserve unchanged contents"},
	"human_handoff": {"pause block wait person operator human manual action completion confirmation"},
}

// LookupContract returns the declared contract for name.
func LookupContract(name string) (ToolContract, bool) {
	c, ok := toolContracts[name]
	c.RetrievalTerms = slices.Clone(c.RetrievalTerms)
	return c, ok
}

// ContractReadOnly reports whether name is declared read-only.
func ContractReadOnly(name string) bool {
	c, ok := toolContracts[name]
	return ok && c.ReadOnly
}

// ApplyToolContracts stamps concurrency safety from contracts onto the
// live registry. Unknown tools stay undeclared (fail-closed in mixed mode).
func ApplyToolContracts(agent *agentcore.Agent) {
	if agent == nil {
		return
	}
	for _, name := range agent.ToolNames() {
		c, hasContract := toolContracts[name]
		if tool, ok := agent.GetTool(name); ok {
			for _, terms := range [][]string{c.RetrievalTerms, additionalToolRetrievalTerms[name]} {
				for _, term := range terms {
					if !slices.Contains(tool.Tags, term) {
						tool.Tags = append(tool.Tags, term)
					}
				}
			}
			if _, declared := capability.Lookup(tool); !declared {
				capability.DeclareBuiltin(tool)
				if c.ReadOnly && name != "exit_plan_mode" {
					if _, declared := capability.Lookup(tool); !declared {
						capability.Declare(tool, capability.Metadata{Kind: capability.Read})
					}
				}
				if c.SideEffectScope == ScopeSystem {
					if _, declared := capability.Lookup(tool); !declared {
						capability.Declare(tool, capability.Metadata{Kind: capability.Execute})
					}
				}
			}
			metadata, declared := capability.Lookup(tool)
			if declared && metadata.Kind != capability.Read {
				safe := false
				tool.ConcurrencySafe = &safe
			}
			if hasContract && c.ConcurrentSafe && (!declared || metadata.Kind == capability.Read) {
				safe := true
				tool.ConcurrencySafe = &safe
			}
		}
	}
}

func ApplyMutationLocks(agent *agentcore.Agent, workDir string) {
	for _, name := range []string{"move", "delete", "write", "edit"} {
		tool, ok := agent.GetTool(name)
		if !ok || tool.Func == nil {
			continue
		}
		metadata, declared := capability.Lookup(tool)
		if !declared || len(metadata.WriteParams) == 0 {
			continue
		}
		if !markToolWrapper(tool, "covo-mutation-lock") {
			continue
		}
		original := tool.Func
		tool.Func = func(ctx context.Context, args json.RawMessage) (any, error) {
			paths, err := metadata.Paths(args)
			if err != nil {
				return nil, err
			}
			for index, path := range paths {
				if !filepath.IsAbs(path) {
					paths[index] = filepath.Join(workDir, path)
				}
			}
			var result any
			err = fileops.WithFileMutationLocks(paths, func() error { var err error; result, err = original(ctx, args); return err })
			return result, err
		}
	}
}
