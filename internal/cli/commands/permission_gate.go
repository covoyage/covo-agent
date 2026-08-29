package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/chat"

	"github.com/covoyage/covo-agent/internal/i18n"
)

// ApprovalChoice represents the user's choice for a dangerous operation.
type ApprovalChoice string

const (
	ChoiceOnce    ApprovalChoice = "once"    // approve this specific call only
	ChoiceSession ApprovalChoice = "session" // approve for the rest of the session
	ChoiceAlways  ApprovalChoice = "always"  // approve permanently
	ChoiceDeny    ApprovalChoice = "deny"    // reject
)

// ApprovalRequest contains context about a pending approval.
type ApprovalRequest struct {
	ToolName    string
	ToolArgs    string
	Summary     string
	PatternKey  string
	Description string
}

var dangerousTools = map[string]bool{
	"bash":           true,
	"process":        true,
	"write_file":     true,
	"edit_block":     true,
	"write":          true,
	"edit":           true,
	"git_commit":     true,
	"git_push":       true,
	"delete_file":    true,
	"execute_code":   true,
	"computer_use":   true,
	"exit_plan_mode": true, // requires user approval before transitioning Plan → Act
}

// ApprovalCategory groups tools into categories for per-category auto-approval.
type ApprovalCategory string

const (
	CatEdit    ApprovalCategory = "edit"
	CatBash    ApprovalCategory = "bash"
	CatGit     ApprovalCategory = "git"
	CatDelete  ApprovalCategory = "delete"
	CatExec    ApprovalCategory = "exec"
	CatDesktop ApprovalCategory = "desktop"
)

// categoryTools maps each category to the set of tool names it covers.
var categoryTools = map[ApprovalCategory]map[string]bool{
	CatEdit:    {"write_file": true, "edit_block": true, "write": true, "edit": true},
	CatBash:    {"bash": true, "process": true},
	CatGit:     {"git_commit": true, "git_push": true},
	CatDelete:  {"delete_file": true},
	CatExec:    {"execute_code": true},
	CatDesktop: {"computer_use": true},
}

// categoryOrder defines the display order for /approve status.
var categoryOrder = []ApprovalCategory{CatEdit, CatBash, CatGit, CatDelete, CatExec, CatDesktop}

// categoryLabel returns a human-readable label for a category.
func categoryLabel(c ApprovalCategory) string {
	switch c {
	case CatEdit:
		return "File Edits (write, edit, edit_block)"
	case CatBash:
		return "Shell Commands (bash, process)"
	case CatGit:
		return "Git Operations (commit, push)"
	case CatDelete:
		return "File Deletion (delete_file)"
	case CatExec:
		return "Code Execution (execute_code)"
	case CatDesktop:
		return "Desktop Control (computer_use)"
	default:
		return string(c)
	}
}

// toolCategory returns the category for a given tool name, or empty string.
func toolCategory(toolName string) ApprovalCategory {
	for cat, tools := range categoryTools {
		if tools[toolName] {
			return cat
		}
	}
	return ""
}

type PermissionGate struct {
	app         *chat.ChatApp
	mu          sync.Mutex
	ch          chan ApprovalChoice
	pendingTool string
	pendingArgs string

	// YoloMode bypasses all tool approval prompts when true.
	YoloMode bool

	// autoApprovedCategories tracks which categories are auto-approved for
	// the current session. Tools in these categories skip the approval prompt.
	autoApprovedCategories map[ApprovalCategory]bool

	// Pattern info from the approval system
	pendingPatternKey  string
	pendingDescription string

	// showApprovalOverlay shows the TUI approval picker.
	showApprovalOverlay func(prompt string, onChoose func(ApprovalChoice), onCancel func())

	// ApprovalSystem provides pattern detection and allowlist management.
	// If set, dangerous bash commands are first checked against patterns.
	ApprovalSystem interface {
		CheckCommand(ctx context.Context, command, sessionKey string) *shared.ApprovalDecision
		ApproveSession(sessionKey, patternKey string)
		ApprovePermanent(patternKey string)
		IsApproved(sessionKey, patternKey string) bool
		HandleUserChoice(sessionKey, patternKey, description, choice string) *shared.ApprovalDecision
		FirePreApproval(command, patternKey, description string)
		FirePostApproval(command, patternKey, description, choice string)
	}

	// PendingPatternProvider returns the description of the last dangerous pattern
	// detected by the approval system, or empty string.
	PendingPatternProvider func() string
}

func NewPermissionGate(app *chat.ChatApp) *PermissionGate {
	return &PermissionGate{
		app:                    app,
		autoApprovedCategories: make(map[ApprovalCategory]bool),
	}
}

func (pg *PermissionGate) Checker() func(ctx context.Context, toolName string, args []byte) bool {
	return func(ctx context.Context, toolName string, args []byte) bool {
		if !dangerousTools[toolName] {
			return true
		}

		// YOLO bypass — skip all tool approval prompts.
		if pg.YoloMode {
			return true
		}

		// Per-category auto-approve: if the tool's category is auto-approved
		// for this session, skip the prompt.
		if cat := toolCategory(toolName); cat != "" {
			pg.mu.Lock()
			autoApproved := pg.autoApprovedCategories[cat]
			pg.mu.Unlock()
			if autoApproved {
				return true
			}
		}

		// Allowlist check for non-bash tools (bash already checked by the approval pattern system)
		if toolName != "bash" && toolName != "process" && pg.ApprovalSystem != nil {
			patternKey := "tool:" + toolName
			if pg.ApprovalSystem.IsApproved("", patternKey) {
				return true
			}
		}

		summary := pg.buildSummary(toolName, args)

		// Show pattern reason if available
		if pg.PendingPatternProvider != nil {
			if pattern := pg.PendingPatternProvider(); pattern != "" {
				pg.mu.Lock()
				pg.pendingPatternKey = pattern
				pg.pendingDescription = pattern
				pg.mu.Unlock()
			}
		}

		pg.mu.Lock()
		pg.pendingTool = toolName
		pg.pendingArgs = string(args)
		pg.ch = make(chan ApprovalChoice, 1)
		showOverlay := pg.showApprovalOverlay
		pg.mu.Unlock()

		// Show TUI approval picker
		if showOverlay != nil {
			showOverlay(summary, func(choice ApprovalChoice) {
				pg.mu.Lock()
				ch := pg.ch
				pg.mu.Unlock()
				if ch != nil {
					ch <- choice
				}
			}, func() {
				pg.mu.Lock()
				ch := pg.ch
				pg.mu.Unlock()
				if ch != nil {
					ch <- ChoiceDeny
				}
			})
		} else {
			pg.app.PrintSystem(i18n.T("approval.text_prompt", "description", summary))
		}

		select {
		case choice := <-pg.ch:
			pg.mu.Lock()
			pendingTool := pg.pendingTool
			pendingArgs := pg.pendingArgs
			pg.ch = nil
			pg.mu.Unlock()

			switch choice {
			case ChoiceDeny:
				pg.app.PrintSystem(i18n.T("approval.denied", "description", pendingTool))
				return false
			case ChoiceSession:
				pg.registerApproval(pendingTool, pendingArgs, "session")
				return true
			case ChoiceAlways:
				pg.registerApproval(pendingTool, pendingArgs, "always")
				return true
			case ChoiceOnce:
				return true
			}
			return false

		case <-ctx.Done():
			pg.mu.Lock()
			pg.ch = nil
			pg.mu.Unlock()
			return false
		}
	}
}

// Respond signals the approval gate with the user's choice.
func (pg *PermissionGate) Respond(choice ApprovalChoice) {
	pg.mu.Lock()
	ch := pg.ch
	pg.mu.Unlock()
	if ch != nil {
		ch <- choice
	}
}

func (pg *PermissionGate) HasPending() bool {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	return pg.ch != nil
}

func (pg *PermissionGate) buildSummary(toolName string, argsJSON []byte) string {
	switch toolName {
	case "bash", "process":
		var args struct {
			Command string `json:"command"`
		}
		json.Unmarshal(argsJSON, &args)
		s := args.Command
		if len(s) > 80 {
			s = s[:77] + "..."
		}
		return fmt.Sprintf("%s: %s", toolName, s)
	case "write_file", "write":
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		json.Unmarshal(argsJSON, &args)
		p := args.Path
		if len(p) > 60 {
			p = "..." + p[len(p)-57:]
		}
		n := len(args.Content)
		return fmt.Sprintf("write %s (%d bytes)", p, n)
	case "edit_block", "edit":
		var args struct {
			Path string `json:"path"`
		}
		json.Unmarshal(argsJSON, &args)
		p := args.Path
		if len(p) > 60 {
			p = "..." + p[len(p)-57:]
		}
		return fmt.Sprintf("edit %s", p)
	case "git_commit":
		var args struct {
			Message string `json:"message"`
		}
		json.Unmarshal(argsJSON, &args)
		m := args.Message
		if len(m) > 60 {
			m = m[:57] + "..."
		}
		return fmt.Sprintf("git commit: %s", m)
	case "git_push":
		return "git push"
	case "delete_file":
		var args struct {
			Path    string   `json:"path"`
			Targets []string `json:"targets"`
		}
		json.Unmarshal(argsJSON, &args)
		if args.Path != "" {
			return fmt.Sprintf("delete %s", args.Path)
		}
		return fmt.Sprintf("delete %d files", len(args.Targets))
	case "execute_code":
		var args struct {
			Code string `json:"code"`
		}
		json.Unmarshal(argsJSON, &args)
		c := args.Code
		if len(c) > 60 {
			c = c[:57] + "..."
		}
		return fmt.Sprintf("execute: %s", c)
	case "exit_plan_mode":
		var args struct {
			Plan string `json:"plan"`
		}
		json.Unmarshal(argsJSON, &args)
		plan := args.Plan
		if len(plan) > 200 {
			plan = plan[:197] + "..."
		}
		return fmt.Sprintf("📋 Plan approval requested:\n%s", plan)
	default:
		return toolName
	}
}

// SetPendingPattern stores the pattern info from the approval system.
func (pg *PermissionGate) SetPendingPattern(patternKey, description string) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	pg.pendingPatternKey = patternKey
	pg.pendingDescription = description
}

// ToggleCategoryAutoApprove toggles auto-approval for a category and returns
// the new state (true = auto-approved, false = requires manual approval).
func (pg *PermissionGate) ToggleCategoryAutoApprove(cat ApprovalCategory) bool {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	pg.autoApprovedCategories[cat] = !pg.autoApprovedCategories[cat]
	return pg.autoApprovedCategories[cat]
}

// IsCategoryAutoApproved returns whether a category is auto-approved.
func (pg *PermissionGate) IsCategoryAutoApproved(cat ApprovalCategory) bool {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	return pg.autoApprovedCategories[cat]
}

// AutoApprovedCategories returns a snapshot of auto-approved categories.
func (pg *PermissionGate) AutoApprovedCategories() []ApprovalCategory {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	var result []ApprovalCategory
	for _, cat := range categoryOrder {
		if pg.autoApprovedCategories[cat] {
			result = append(result, cat)
		}
	}
	return result
}

// FormatApprovalStatus returns a human-readable summary of the current
// approval configuration.
func (pg *PermissionGate) FormatApprovalStatus() string {
	var b strings.Builder
	if pg.YoloMode {
		b.WriteString("🔴 YOLO mode is ON — all approvals bypassed.\n")
	}
	b.WriteString("Per-category auto-approval:\n")
	for _, cat := range categoryOrder {
		status := "❌ manual"
		if pg.IsCategoryAutoApproved(cat) {
			status = "✅ auto"
		}
		fmt.Fprintf(&b, "  %s: %s\n", categoryLabel(cat), status)
	}
	return b.String()
}

// registerApproval processes a session or permanent approval choice.
func (pg *PermissionGate) registerApproval(toolName, argsJSON, choice string) {
	if pg.ApprovalSystem == nil {
		return
	}

	// Extract pattern info for bash commands
	if toolName == "bash" || toolName == "process" {
		var args struct {
			Command string `json:"command"`
		}
		json.Unmarshal([]byte(argsJSON), &args)
		if args.Command != "" {
			pg.mu.Lock()
			patternKey := pg.pendingPatternKey
			description := pg.pendingDescription
			pg.mu.Unlock()

			if patternKey != "" {
				switch choice {
				case "session":
					pg.ApprovalSystem.ApproveSession("", patternKey)
					pg.ApprovalSystem.FirePostApproval(args.Command, patternKey, description, "session")
				case "always":
					pg.ApprovalSystem.ApproveSession("", patternKey)
					pg.ApprovalSystem.ApprovePermanent(patternKey)
					pg.ApprovalSystem.FirePostApproval(args.Command, patternKey, description, "always")
				}
			}
		}
		return
	}

	// Non-bash tools (Write, Edit, etc.): use tool name as approval key
	patternKey := "tool:" + toolName
	switch choice {
	case "session":
		pg.ApprovalSystem.ApproveSession("", patternKey)
	case "always":
		pg.ApprovalSystem.ApproveSession("", patternKey)
		pg.ApprovalSystem.ApprovePermanent(patternKey)
	}
}
