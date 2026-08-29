package evolution

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

type EvolutionExtension struct {
	agentcore.BaseLifecycleHook
	memory    *MemorySystem
	skillMgr  *SkillManager
	bundleMgr *BundleManager
	usage     *SkillUsageTracker
	tools     []*agentcore.Tool
	// sessionID lazily resolves the current session id so skill preprocessing
	// can substitute ${COVO_SESSION_ID}. May be nil.
	sessionID func() string
}

func NewEvolutionExtension(memory *MemorySystem, skillMgr *SkillManager, bundleMgr *BundleManager, usage *SkillUsageTracker, sessionID func() string) *EvolutionExtension {
	return &EvolutionExtension{
		memory:    memory,
		skillMgr:  skillMgr,
		bundleMgr: bundleMgr,
		usage:     usage,
		sessionID: sessionID,
	}
}

func (e *EvolutionExtension) Name() string { return "evolution" }

func (e *EvolutionExtension) Init(_ context.Context, agent *agentcore.Agent) error {
	e.tools = []*agentcore.Tool{
		e.buildMemoryTool(),
		e.buildSkillTool(),
		e.buildSkillManageTool(),
		e.buildSkillBundleTool(),
		e.buildSkillConfigTool(),
		e.buildSkillScriptTool(),
	}
	agent.RegisterTools(e.tools...)
	return nil
}

func (e *EvolutionExtension) Dispose() error { return nil }

func (e *EvolutionExtension) Tools() []*agentcore.Tool { return e.tools }

func (e *EvolutionExtension) buildMemoryTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "memory",
		Description: strings.Join([]string{
			"Manage persistent memory that survives across sessions. Two stores are available:",
			"- agent: your personal notes and observations (environment facts, project conventions, tool quirks, things learned)",
			"- user: what you know about the user (preferences, communication style, expectations, workflow habits)",
			"",
			"Entries are separated by § (section sign). Each entry can be multiline.",
			"Memory is injected into the system prompt at session start as a frozen snapshot.",
			"Mid-session writes update files on disk immediately but do NOT change the current system prompt.",
			"",
			"Actions:",
			"- add: append a new entry to the store",
			"- replace: find an entry by substring and replace it entirely",
			"- remove: remove an entry matching a substring",
			"- read: read all entries from a store",
		}, "\n"),
		Parameters: map[string]any{
			"type":       "object",
			"properties": e.memoryParams(),
			"required":   []string{"action", "store"},
		},
		Func: e.handleMemory,
	}
}

func (e *EvolutionExtension) memoryParams() map[string]any {
	return map[string]any{
		"action": map[string]any{
			"type":        "string",
			"description": "Action to perform: add, replace, remove, or read",
			"enum":        []string{"add", "replace", "remove", "read"},
		},
		"store": map[string]any{
			"type":        "string",
			"description": "Memory store to operate on: agent (your knowledge) or user (user profile)",
			"enum":        []string{"agent", "user"},
		},
		"content": map[string]any{
			"type":        "string",
			"description": "Content for add action, or new content for replace action",
		},
		"old_substr": map[string]any{
			"type":        "string",
			"description": "Substring to find for replace or remove actions",
		},
	}
}

func (e *EvolutionExtension) handleMemory(ctx context.Context, args json.RawMessage) (any, error) {
	var params struct {
		Action    string `json:"action"`
		Store     string `json:"store"`
		Content   string `json:"content"`
		OldSubstr string `json:"old_substr"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	store := MemoryAgent
	if strings.ToLower(params.Store) == "user" {
		store = MemoryUser
	}

	switch strings.ToLower(params.Action) {
	case "add":
		if params.Content == "" {
			return nil, fmt.Errorf("content is required for add action")
		}
		if err := e.memory.Add(store, params.Content); err != nil {
			return nil, fmt.Errorf("add memory: %w", err)
		}
		return map[string]string{"status": "added", "store": string(store)}, nil

	case "replace":
		if params.OldSubstr == "" || params.Content == "" {
			return nil, fmt.Errorf("old_substr and content are required for replace action")
		}
		if err := e.memory.Replace(store, params.OldSubstr, params.Content); err != nil {
			return nil, fmt.Errorf("replace memory: %w", err)
		}
		return map[string]string{"status": "replaced", "store": string(store)}, nil

	case "remove":
		if params.OldSubstr == "" {
			return nil, fmt.Errorf("old_substr is required for remove action")
		}
		if err := e.memory.Remove(store, params.OldSubstr); err != nil {
			return nil, fmt.Errorf("remove memory: %w", err)
		}
		return map[string]string{"status": "removed", "store": string(store)}, nil

	case "read":
		entries, err := e.memory.Read(store)
		if err != nil {
			return nil, fmt.Errorf("read memory: %w", err)
		}
		if len(entries) == 0 {
			return map[string]string{"status": "empty", "store": string(store)}, nil
		}
		var items []map[string]any
		for _, entry := range entries {
			items = append(items, map[string]any{
				"index":   entry.Index,
				"content": entry.Content,
			})
		}
		return map[string]any{"store": string(store), "entries": items}, nil

	default:
		return nil, fmt.Errorf("unknown action %q: must be add, replace, remove, or read", params.Action)
	}
}

// buildSkillTool builds the `skill` tool: loads a skill's preprocessed
// content into context so the model can follow it. This is the real
// implementation behind the <available_skills> index protocol.
func (e *EvolutionExtension) buildSkillTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "skill",
		Description: strings.Join([]string{
			"Load a skill's full instructions into context. Skills are reusable",
			"procedural knowledge listed in <available_skills> of the system prompt.",
			"Call this when a task matches a skill's description, then follow the",
			"returned instructions.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Skill name (max 64 chars, see available_skills)",
				},
			},
			"required": []string{"name"},
		},
		Func: e.handleSkillLoad,
	}
}

func (e *EvolutionExtension) handleSkillLoad(_ context.Context, args json.RawMessage) (any, error) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("parse skill arguments: %w", err)
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	cfg := PreprocessConfig{}
	if e.sessionID != nil {
		cfg.SessionID = e.sessionID()
	}
	content, err := e.skillMgr.ReadPreprocessed(name, cfg)
	if err != nil {
		return nil, fmt.Errorf("load skill %q: %w", name, err)
	}
	if skill, ok := e.skillMgr.Find(name); ok {
		e.usage.RecordView(skill.ID)
	} else {
		e.usage.RecordView(name)
	}
	return map[string]any{
		"name":    name,
		"content": content,
	}, nil
}

func (e *EvolutionExtension) buildSkillManageTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "skill_manage",
		Description: strings.Join([]string{
			"Create, update, and delete skills. Skills are reusable procedural knowledge —",
			"they capture how to do specific types of tasks based on proven experience.",
			"Skills should be class-level (broad), not session-level (narrow).",
			"",
			"Actions:",
			"- read: Read the content of an existing skill's SKILL.md.",
			"- create: Create a new skill with a SKILL.md file. Requires name, description, and body.",
			"- edit: Replace the entire body of an existing skill's SKILL.md.",
			"- patch: Find and replace a substring within a skill's SKILL.md.",
			"- improve: Record an improvement to a skill. Adds an improvement_history entry to the frontmatter and optionally updates the body (if body is provided). Minimally requires name and improvement_summary.",
			"- delete: Archive a skill (moves to .archive/, not permanent deletion).",
			"- write_file: Add or overwrite a supporting file (references/, templates/, scripts/, assets/).",
			"- remove_file: Remove a supporting file from a skill.",
			"",
			"Skill body format: Markdown with YAML frontmatter is auto-generated.",
			"Write clear, actionable instructions. Include examples where helpful.",
			"Skills are stored in ~/.covo-agent/skills/<name>/SKILL.md.",
			"",
			"For sensitive or large changes, prefer the skill_workshop proposal flow:",
			"create a proposal there and let the user apply it after review; use",
			"this tool for routine edits and improvements.",
		}, "\n"),
		Parameters: map[string]any{
			"type":       "object",
			"properties": e.skillManageParams(),
			"required":   []string{"action", "name"},
		},
		Func: e.handleSkillManage,
	}
}

func (e *EvolutionExtension) skillManageParams() map[string]any {
	return map[string]any{
		"action": map[string]any{
			"type":        "string",
			"description": "Action to perform: read, create, edit, patch, improve, delete, write_file, remove_file",
			"enum":        []string{"read", "create", "edit", "patch", "improve", "delete", "write_file", "remove_file"},
		},
		"name": map[string]any{
			"type":        "string",
			"description": "Skill name (max 64 chars, lowercase with hyphens recommended)",
		},
		"description": map[string]any{
			"type":        "string",
			"description": "Short description of the skill (max 1024 chars). Required for create.",
		},
		"body": map[string]any{
			"type":        "string",
			"description": "Full SKILL.md body content (without frontmatter). Required for create and edit.",
		},
		"find_str": map[string]any{
			"type":        "string",
			"description": "Substring to find for patch action",
		},
		"replace_str": map[string]any{
			"type":        "string",
			"description": "Replacement string for patch action",
		},
		"rel_path": map[string]any{
			"type":        "string",
			"description": "Relative path within the skill directory for write_file/remove_file (e.g. references/api.md)",
		},
		"file_content": map[string]any{
			"type":        "string",
			"description": "Content to write for write_file action",
		},
		"improvement_summary": map[string]any{
			"type":        "string",
			"description": "Summary of what was improved and why. Required for improve action.",
		},
	}
}

func (e *EvolutionExtension) handleSkillManage(ctx context.Context, args json.RawMessage) (any, error) {
	var params struct {
		Action             string `json:"action"`
		Name               string `json:"name"`
		Description        string `json:"description"`
		Body               string `json:"body"`
		FindStr            string `json:"find_str"`
		ReplaceStr         string `json:"replace_str"`
		RelPath            string `json:"rel_path"`
		FileContent        string `json:"file_content"`
		ImprovementSummary string `json:"improvement_summary"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	switch strings.ToLower(params.Action) {
	case "read":
		content, err := e.skillMgr.ReadPreprocessed(params.Name, PreprocessConfig{})
		if err != nil {
			return nil, fmt.Errorf("read skill: %w", err)
		}
		return map[string]string{
			"status":  "read",
			"name":    params.Name,
			"content": content,
		}, nil

	case "create":
		if params.Description == "" || params.Body == "" {
			return nil, fmt.Errorf("description and body are required for create action")
		}
		path, err := e.skillMgr.Create(params.Name, params.Description, params.Body)
		if err != nil {
			return nil, fmt.Errorf("create skill: %w", err)
		}
		return map[string]string{
			"status": "created",
			"name":   params.Name,
			"path":   path,
		}, nil

	case "edit":
		if params.Body == "" {
			return nil, fmt.Errorf("body is required for edit action")
		}
		if err := e.skillMgr.Edit(params.Name, params.Body); err != nil {
			return nil, fmt.Errorf("edit skill: %w", err)
		}
		return map[string]string{"status": "edited", "name": params.Name}, nil

	case "patch":
		if params.FindStr == "" {
			return nil, fmt.Errorf("find_str is required for patch action")
		}
		if err := e.skillMgr.Patch(params.Name, params.FindStr, params.ReplaceStr); err != nil {
			return nil, fmt.Errorf("patch skill: %w", err)
		}
		return map[string]string{"status": "patched", "name": params.Name}, nil

	case "improve":
		if params.ImprovementSummary == "" {
			return nil, fmt.Errorf("improvement_summary is required for improve action")
		}
		var newBody []string
		if params.Body != "" {
			newBody = []string{params.Body}
		}
		if err := e.skillMgr.RecordImprovement(params.Name, params.ImprovementSummary, newBody...); err != nil {
			return nil, fmt.Errorf("improve skill: %w", err)
		}
		return map[string]string{"status": "improved", "name": params.Name}, nil

	case "delete":
		if err := e.skillMgr.Delete(params.Name); err != nil {
			return nil, fmt.Errorf("delete skill: %w", err)
		}
		return map[string]string{"status": "archived", "name": params.Name}, nil

	case "write_file":
		if params.RelPath == "" || params.FileContent == "" {
			return nil, fmt.Errorf("rel_path and file_content are required for write_file action")
		}
		if err := e.skillMgr.WriteFile(params.Name, params.RelPath, params.FileContent); err != nil {
			return nil, fmt.Errorf("write file: %w", err)
		}
		return map[string]string{"status": "written", "name": params.Name, "path": params.RelPath}, nil

	case "remove_file":
		if params.RelPath == "" {
			return nil, fmt.Errorf("rel_path is required for remove_file action")
		}
		if err := e.skillMgr.RemoveFile(params.Name, params.RelPath); err != nil {
			return nil, fmt.Errorf("remove file: %w", err)
		}
		return map[string]string{"status": "removed", "name": params.Name, "path": params.RelPath}, nil

	default:
		return nil, fmt.Errorf("unknown action %q", params.Action)
	}
}

func (e *EvolutionExtension) buildSkillBundleTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "skill_bundle",
		Description: strings.Join([]string{
			"Manage skill bundles — aliases that load multiple skills together.",
			"",
			"Bundles are YAML files stored in ~/.covo-agent/skill-bundles/.",
			"When you invoke a bundle, all referenced skills are loaded at once.",
			"",
			"Actions:",
			"- list: List all available bundles",
			"- view: Show a bundle's definition",
			"- create: Create a new bundle (YAML with name, description, skills list)",
			"- delete: Delete a bundle",
			"- load: Load all skills in a bundle into context",
			"",
			"Bundle YAML format:",
			"  name: backend-dev",
			"  description: Backend feature work",
			"  skills:",
			"    - github-code-review",
			"    - test-driven-development",
			"  instruction: |",
			"    Optional extra guidance.",
			"",
			"Use bundles to quickly load related skill sets for common workflows.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Action: list, view, create, delete, load",
					"enum":        []string{"list", "view", "create", "delete", "load"},
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Bundle name (required for view, create, delete, load).",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Bundle description (for create).",
				},
				"skills": map[string]any{
					"type":        "array",
					"description": "List of skill names to include (for create).",
					"items":       map[string]any{"type": "string"},
				},
				"instruction": map[string]any{
					"type":        "string",
					"description": "Optional extra guidance injected above skill bodies.",
				},
			},
			"required": []string{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Action      string   `json:"action"`
				Name        string   `json:"name"`
				Description string   `json:"description"`
				Skills      []string `json:"skills"`
				Instruction string   `json:"instruction"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			switch params.Action {
			case "list":
				bundles, err := e.bundleMgr.List()
				if err != nil {
					return nil, fmt.Errorf("list bundles: %w", err)
				}
				names := make([]string, 0, len(bundles))
				for _, b := range bundles {
					names = append(names, b.Name)
				}
				return map[string]any{
					"action":  "list",
					"bundles": bundles,
					"count":   len(bundles),
				}, nil

			case "view":
				if params.Name == "" {
					return nil, fmt.Errorf("name is required for view")
				}
				bundle, err := e.bundleMgr.Get(params.Name)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"action": "view",
					"bundle": bundle,
				}, nil

			case "create":
				if params.Name == "" {
					return nil, fmt.Errorf("name is required for create")
				}
				if len(params.Skills) == 0 {
					return nil, fmt.Errorf("skills list is required for create")
				}
				bundle := SkillBundle{
					Name:        params.Name,
					Description: params.Description,
					Skills:      params.Skills,
					Instruction: params.Instruction,
				}
				if err := e.bundleMgr.Save(bundle); err != nil {
					return nil, fmt.Errorf("save bundle: %w", err)
				}
				return map[string]any{
					"action": "created",
					"name":   params.Name,
				}, nil

			case "delete":
				if params.Name == "" {
					return nil, fmt.Errorf("name is required for delete")
				}
				if err := e.bundleMgr.Delete(params.Name); err != nil {
					return nil, err
				}
				return map[string]any{
					"action": "deleted",
					"name":   params.Name,
				}, nil

			case "load":
				if params.Name == "" {
					return nil, fmt.Errorf("name is required for load")
				}
				cfg := PreprocessConfig{}
				content, err := e.bundleMgr.BuildInvocation(e.skillMgr, params.Name, cfg)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"action":  "loaded",
					"name":    params.Name,
					"content": content,
				}, nil

			default:
				return nil, fmt.Errorf("unknown action: %s", params.Action)
			}
		},
	}
}

func (e *EvolutionExtension) buildSkillConfigTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "skill_config",
		Description: strings.Join([]string{
			"Manage skill configuration variables. Skills can declare config vars they need",
			"(e.g. API keys, file paths). Use this tool to view or resolve their values.",
			"",
			"Actions:",
			"- discover: List all config vars declared across all skills",
			"- resolve: Show current resolved values (from config.yaml or defaults)",
			"- set: Set a config var value (stored in config.yaml under skills.config.<key>)",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Action: discover, resolve, or set",
					"enum":        []string{"discover", "resolve", "set"},
				},
				"key": map[string]any{
					"type":        "string",
					"description": "Config variable key (required for set)",
				},
				"value": map[string]any{
					"type":        "string",
					"description": "Config variable value (required for set)",
				},
			},
			"required": []string{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Action string `json:"action"`
				Key    string `json:"key"`
				Value  string `json:"value"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			switch params.Action {
			case "discover":
				vars := e.skillMgr.DiscoverSkillConfigVars()
				return map[string]any{
					"action": "discover",
					"count":  len(vars),
					"vars":   vars,
				}, nil
			case "resolve":
				vars := e.skillMgr.DiscoverSkillConfigVars()
				values := make(map[string]string)
				for _, v := range vars {
					values[v.Key] = v.Default
				}
				return map[string]any{
					"action": "resolve",
					"vars":   vars,
					"values": values,
				}, nil
			case "set":
				if params.Key == "" || params.Value == "" {
					return nil, fmt.Errorf("key and value are required for set action")
				}
				return map[string]any{
					"action": "set",
					"key":    params.Key,
					"value":  params.Value,
					"status": "acknowledged",
					"note":   "To persist this value, add it to config.yaml under: skills.config." + params.Key,
				}, nil
			default:
				return nil, fmt.Errorf("unknown action: %s", params.Action)
			}
		},
	}
}

func (e *EvolutionExtension) buildSkillScriptTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "skill_script",
		Description: strings.Join([]string{
			"Execute a script file from a skill's scripts/ directory, or list available scripts.",
			"Scripts are companion executables (Python, shell, etc.) bundled with a skill.",
			"",
			"Actions:",
			"- run: Execute a script (requires name, script, and optionally args)",
			"- list: List available scripts in a skill (requires name)",
			"",
			"Usage examples:",
			`  skill_script {"action": "run", "name": "arxiv", "script": "search_arxiv.py", "args": ["transformer"]}`,
			`  skill_script {"action": "list", "name": "arxiv"}`,
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Action: run or list",
					"enum":        []string{"run", "list"},
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Skill name",
				},
				"script": map[string]any{
					"type":        "string",
					"description": "Relative path to script under the skill's scripts/ directory (required for run)",
				},
				"args": map[string]any{
					"type":        "array",
					"description": "Command-line arguments for the script",
					"items":       map[string]any{"type": "string"},
				},
			},
			"required": []string{"action", "name"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Action string   `json:"action"`
				Name   string   `json:"name"`
				Script string   `json:"script"`
				Args   []string `json:"args"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			switch params.Action {
			case "list":
				scripts, err := e.skillMgr.ListScripts(params.Name)
				if err != nil {
					return nil, fmt.Errorf("list scripts: %w", err)
				}
				return map[string]any{
					"status":  "listed",
					"name":    params.Name,
					"scripts": scripts,
					"count":   len(scripts),
				}, nil
			case "run":
				if params.Script == "" {
					return nil, fmt.Errorf("script is required for run action")
				}
				output, err := e.skillMgr.ExecuteScript(params.Name, params.Script, params.Args)
				if err != nil {
					return nil, fmt.Errorf("execute script: %w", err)
				}
				return map[string]any{
					"status": "executed",
					"name":   params.Name,
					"script": params.Script,
					"output": output,
				}, nil
			default:
				return nil, fmt.Errorf("unknown action: %s", params.Action)
			}
		},
	}
}
