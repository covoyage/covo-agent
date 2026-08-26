package tools

import "github.com/covoyage/covonaut/agentcore"

// profileCategories maps tool names to the profiles where they're available.
var profileCategories = map[string][]string{
	// --- minimal: core tools available in every profile ---
	"session_search": {"minimal", "coding", "messaging"},
	"send_message":   {"minimal", "coding", "messaging"},
	"todo":           {"minimal", "coding", "messaging"},
	"update_plan":    {"minimal", "coding", "messaging"},
	"exit_plan_mode": {"minimal", "coding", "messaging"},
	"clarify":        {"minimal", "coding", "messaging"},

	// --- coding: code-editing, execution, debugging ---
	"diffs":                  {"coding"},
	"edit_block":             {"coding"},
	"apply_patch":            {"coding"},
	"mcp":                    {"coding"},
	"process":                {"coding"},
	"monitor":                {"coding"},
	"sandbox":                {"coding"},
	"sessions_spawn":         {"coding"},
	"sessions_spawn_batch":   {"coding"},
	"sessions_yield":         {"coding"},
	"sessions_send":          {"coding"},
	"pdf":                    {"coding"},
	"transcribe":             {"coding"},
	"secrets":                {"coding"},
	"tool_search":            {"coding"},
	"tool_describe":          {"coding"},
	"tool_call":              {"coding"},
	"llm_task":               {"coding"},
	"human_handoff":          {"coding"},
	"moa":                    {"coding"},
	"get_goal":               {"coding"},
	"create_goal":            {"coding"},
	"update_goal":            {"coding"},
	"memory_recall":          {"coding"},
	"memory_store":           {"coding", "messaging"},
	"memory_forget":          {"coding"},
	"memory_semantic_search": {"coding"},
	"memory_semantic_store":  {"coding"},
	"memory_semantic_forget": {"coding"},
	"memory_semantic_stats":  {"coding"},

	// --- feishu tools ---
	"feishu_doc_read":        {"coding", "messaging"},
	"feishu_doc_create":      {"coding", "messaging"},
	"feishu_wiki_list":       {"coding", "messaging"},
	"feishu_wiki_nodes":      {"coding", "messaging"},
	"feishu_bitable_list":    {"coding", "messaging"},
	"feishu_bitable_records": {"coding", "messaging"},
	"feishu_drive_list":      {"coding", "messaging"},
	"feishu_drive_download":  {"coding", "messaging"},

	// --- creative / visualization ---
	"canvas":      {"coding", "messaging"},
	"live_canvas": {"coding", "messaging"},
	"stop_canvas": {"coding", "messaging"},

	// --- messaging: communication and channels ---
	"cronjob":           {"messaging"},
	"swarm":             {"messaging"},
	"swarm_orchestrate": {"messaging"},
	"kanban":            {"messaging"},

	// --- creative: coding + messaging ---
	"tts":            {"coding", "messaging"},
	"image_generate": {"coding", "messaging"},
	"video_generate": {"coding", "messaging"},
	"music_generate": {"coding", "messaging"},

	// --- voice interaction ---
	"voice_listen": {"messaging"},
	"voice_record": {"messaging"},

	// --- remote & audit ---
	"remote_exec":       {"coding"},
	"audit_read":        {"coding"},
	"deploy":            {"coding"},
	"structured_output": {"minimal", "coding", "messaging"},
	"parse_duration":    {"coding"},

	// --- web ---
	"web_fetch":   {"coding"},
	"reaction":    {"coding", "messaging"},
	"append_file": {"coding"},

	// --- context compression ---
	"context_compress":        {"coding"},
	"context_compress_config": {"coding"},

	// --- orchestration ---
	"dashboard":          {"coding"},
	"workflow":           {"coding"},
	"sessions_fork":      {"coding"},
	"memory_extract":     {"coding"},
	"memory_dream":       {"coding"},
	"memory_dream_stats": {"coding"},

	// --- worktree ---
	"enter_worktree": {"coding"},
	"exit_worktree":  {"coding"},

	// --- computer use ---
	"computer_use": {"coding"},

	// --- hardware ---
	"i2c":    {"coding"},
	"spi":    {"coding"},
	"serial": {"coding"},

	// --- agent coordination ---
	"interrupt_agent": {"coding"},
	"sessions_wait":   {"coding"},
	"close_agent":     {"coding"},
	"send_input":      {"coding"},

	// --- test generation ---
	"generate_tests": {"coding"},

	// --- standing orders ---
	"standing_orders": {"coding", "messaging"},

	// --- skill workshop ---
	"skill_workshop": {"coding", "messaging"},

	// --- unified search ---
	"search_files": {"coding"},

	// --- code mode ---
	"run_code": {"coding"},
}

func filterToolsByProfile(tools []*agentcore.Tool, profile string) []*agentcore.Tool {
	if profile == "full" || profile == "" {
		return tools
	}
	filtered := make([]*agentcore.Tool, 0, len(tools))
	for _, t := range tools {
		profiles, ok := profileCategories[t.Name]
		if !ok {
			continue // unknown tool — skip for safety in restricted profiles
		}
		for _, p := range profiles {
			if p == profile {
				filtered = append(filtered, t)
				break
			}
		}
	}
	return filtered
}
