package commands

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
	"net/url"
	"strings"
	"sync"

	"github.com/covoyage/covo-agent/internal/agent/approval"

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

var dangerousTools = approval.DangerousTools()

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

// scanRecipeName returns the recipe a scan call asks for, or "" if absent.
func scanRecipeName(argsJSON []byte) string {
	var args struct {
		Recipe string `json:"recipe"`
	}
	json.Unmarshal(argsJSON, &args)
	return strings.ToLower(strings.TrimSpace(args.Recipe))
}

// approvalPatternKey binds active network approvals to the exact call arguments.
func approvalPatternKey(toolName string, argsJSON []byte) string {
	switch toolName {
	case "scan", "scan_profile", "request_intake", "web_param_probe", "exchange_replay", "exchange_compare", "csrf_retest", "remote_node_exec":
		sum := sha256.Sum256(argsJSON)
		return fmt.Sprintf("tool:%s:%x", toolName, sum[:12])
	}
	return "tool:" + toolName
}

func readOnlyCyberToolCall(name string, args []byte) bool {
	switch name {
	case "scan":
		return scanRecipeName(args) == "list"
	case "scan_profile":
		trimmed := strings.TrimSpace(string(args))
		return trimmed == "" || trimmed == "{}"
	case "request_intake":
		var params struct {
			Action string `json:"action"`
		}
		return json.Unmarshal(args, &params) == nil && (params.Action == "" || params.Action == "status")
	}
	return false
}

// safePatternToken keeps only characters a recipe name can legitimately
// contain, so model-supplied text cannot reshape the allowlist key.
func safePatternToken(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() > 32 {
		return b.String()[:32]
	}
	return b.String()
}

type PermissionGate struct {
	app         *chat.ChatApp
	mu          sync.Mutex
	requestOnce sync.Once
	requestSlot chan struct{}
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
		if ctx.Err() != nil {
			return false
		}
		required := approval.ApprovalRequired(ctx)
		if !required && !approval.RequiresApproval(ctx, toolName, args) {
			return true
		}

		// YOLO bypass — skip all tool approval prompts.
		if !required && (pg.YoloMode || shared.RuntimeState.SessionYolo()) {
			return true
		}

		// Per-category auto-approve: if the tool's category is auto-approved
		// for this session, skip the prompt.
		if cat := toolCategory(toolName); !required && cat != "" {
			pg.mu.Lock()
			autoApproved := pg.autoApprovedCategories[cat]
			pg.mu.Unlock()
			if autoApproved {
				return true
			}
		}

		// Allowlist check for non-bash tools (bash already checked by the approval pattern system)
		if !required && toolName != "bash" && toolName != "process" {
			// recipe "list" only reads the catalog — no process is spawned.
			if pg.ApprovalSystem != nil && pg.ApprovalSystem.IsApproved("", approvalPatternKey(toolName, args)) {
				return true
			}
		}

		pg.requestOnce.Do(func() { pg.requestSlot = make(chan struct{}, 1) })
		select {
		case pg.requestSlot <- struct{}{}:
		case <-ctx.Done():
			return false
		}
		defer func() { <-pg.requestSlot }()
		if ctx.Err() != nil {
			return false
		}
		summary := buildSummary(toolName, args)

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
		ch := make(chan ApprovalChoice, 1)
		pg.ch = ch
		showOverlay := pg.showApprovalOverlay
		pg.mu.Unlock()
		defer func() {
			pg.mu.Lock()
			if pg.ch == ch {
				pg.ch = nil
			}
			pg.mu.Unlock()
		}()
		respond := func(choice ApprovalChoice) {
			pg.mu.Lock()
			defer pg.mu.Unlock()
			if pg.ch != ch || ctx.Err() != nil {
				return
			}
			select {
			case ch <- choice:
			default:
			}
		}

		// Show TUI approval picker
		if showOverlay != nil {
			showOverlay(summary, respond, func() { respond(ChoiceDeny) })
		} else {
			if pg.app == nil {
				return false
			}
			pg.app.PrintSystem(i18n.T("approval.text_prompt", "description", summary))
		}

		select {
		case choice := <-ch:
			if ctx.Err() != nil {
				return false
			}
			pg.mu.Lock()
			pendingTool := pg.pendingTool
			pendingArgs := pg.pendingArgs
			pg.ch = nil
			pg.mu.Unlock()

			switch choice {
			case ChoiceDeny:
				if pg.app != nil {
					pg.app.PrintSystem(i18n.T("approval.denied", "description", pendingTool))
				}
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
		select {
		case ch <- choice:
		default:
		}
	}
}

func (pg *PermissionGate) HasPending() bool {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	return pg.ch != nil
}

func buildSummary(toolName string, argsJSON []byte) string {
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
	case "scan":
		var args struct {
			Recipe  string            `json:"recipe"`
			Target  string            `json:"target"`
			Options map[string]string `json:"options"`
		}
		json.Unmarshal(argsJSON, &args)
		target := strings.TrimSpace(args.Target)
		if r := []rune(target); len(r) > 60 {
			target = string(r[:57]) + "..."
		}
		if target == "" {
			return fmt.Sprintf("scan recipe %s", args.Recipe)
		}
		for _, key := range []string{"RHOSTS", "RHOST", "dc", "domain", "url"} {
			if host := strings.TrimSpace(args.Options[key]); host != "" {
				if len(host) > 60 {
					host = host[:60]
				}
				return fmt.Sprintf("scan %s: %s (%s=%s)", args.Recipe, target, key, strings.Join(strings.Fields(host), " "))
			}
		}
		return fmt.Sprintf("scan %s: %s", args.Recipe, target)
	case "csrf_retest":
		var args struct {
			PageID   string `json:"page_id"`
			SubmitID string `json:"submit_id"`
		}
		json.Unmarshal(argsJSON, &args)
		return fmt.Sprintf("csrf_retest GET %s → POST %s", safePatternToken(args.PageID), safePatternToken(args.SubmitID))
	case "scan_profile", "request_intake":
		var args struct {
			Action       string   `json:"action"`
			AllowedHosts []string `json:"allowed_hosts"`
			Hosts        []string `json:"hosts"`
		}
		json.Unmarshal(argsJSON, &args)
		hosts := args.AllowedHosts
		if toolName == "request_intake" {
			hosts = args.Hosts
		}
		return fmt.Sprintf("%s %.16s hosts=%.100s", toolName, args.Action, strings.Join(hosts, ","))
	case "exchange_replay", "exchange_compare":
		var args struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		}
		json.Unmarshal(argsJSON, &args)
		destination := ""
		if parsed, err := url.Parse(args.URL); err == nil && parsed.Hostname() != "" {
			destination = parsed.Hostname()
		}
		return fmt.Sprintf("%s exchange=%.32s host=%.80s", toolName, args.ID, destination)
	case "remote_node_exec":
		var args struct {
			ID string `json:"id"`
		}
		json.Unmarshal(argsJSON, &args)
		return fmt.Sprintf("remote_node_exec node=%.32s", args.ID)
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
	if pg.YoloMode || shared.RuntimeState.SessionYolo() {
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

	// Non-bash tools use the same key that the checker tests before execution.
	patternKey := approvalPatternKey(toolName, []byte(argsJSON))
	switch choice {
	case "session":
		pg.ApprovalSystem.ApproveSession("", patternKey)
	case "always":
		pg.ApprovalSystem.ApproveSession("", patternKey)
		pg.ApprovalSystem.ApprovePermanent(patternKey)
	}
}
