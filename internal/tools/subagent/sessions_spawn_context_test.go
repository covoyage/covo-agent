package subagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

// --- SummarizeParentState tests ---

func TestSummarizeParentState_Empty(t *testing.T) {
	if got := SummarizeParentState(nil); got != "" {
		t.Errorf("expected empty summary for nil messages, got %q", got)
	}
	if got := SummarizeParentState([]agentcore.Message{}); got != "" {
		t.Errorf("expected empty summary for empty messages, got %q", got)
	}
}

func TestSummarizeParentState_IncludesUserMessages(t *testing.T) {
	msgs := []agentcore.Message{
		{Role: agentcore.RoleUser, Content: "Please fix the login bug"},
		{Role: agentcore.RoleAssistant, Content: "I'll look into that."},
		{Role: agentcore.RoleUser, Content: "Also add a test for it"},
	}

	summary := SummarizeParentState(msgs)

	if !strings.Contains(summary, "PARENT CONTEXT SUMMARY") {
		t.Error("expected summary header")
	}
	if !strings.Contains(summary, "Please fix the login bug") {
		t.Error("expected first user message in summary")
	}
	if !strings.Contains(summary, "Also add a test for it") {
		t.Error("expected second user message in summary")
	}
	if !strings.Contains(summary, "Recent User Requests") {
		t.Error("expected 'Recent User Requests' section")
	}
}

func TestSummarizeParentState_IncludesLastAssistant(t *testing.T) {
	msgs := []agentcore.Message{
		{Role: agentcore.RoleUser, Content: "Do something"},
		{Role: agentcore.RoleAssistant, Content: "I completed the task successfully."},
	}

	summary := SummarizeParentState(msgs)

	if !strings.Contains(summary, "Last Assistant Action") {
		t.Error("expected 'Last Assistant Action' section")
	}
	if !strings.Contains(summary, "I completed the task successfully.") {
		t.Error("expected last assistant content in summary")
	}
}

func TestSummarizeParentState_IncludesToolCalls(t *testing.T) {
	msgs := []agentcore.Message{
		{Role: agentcore.RoleUser, Content: "Read the file"},
		{
			Role: agentcore.RoleAssistant,
			ToolCalls: []agentcore.ToolCall{
				{Name: "read_file"},
				{Name: "edit_block"},
				{Name: "read_file"}, // duplicate
				{Name: "bash"},
			},
		},
	}

	summary := SummarizeParentState(msgs)

	if !strings.Contains(summary, "Tools Used") {
		t.Error("expected 'Tools Used' section")
	}
	// Should be deduplicated
	if strings.Count(summary, "read_file") > 1 {
		t.Error("expected tool names to be deduplicated")
	}
	if !strings.Contains(summary, "edit_block") {
		t.Error("expected edit_block in tools list")
	}
	if !strings.Contains(summary, "bash") {
		t.Error("expected bash in tools list")
	}
}

func TestSummarizeParentState_TruncatesLongContent(t *testing.T) {
	longUser := strings.Repeat("a", 500)
	longAssistant := strings.Repeat("b", 800)

	msgs := []agentcore.Message{
		{Role: agentcore.RoleUser, Content: longUser},
		{Role: agentcore.RoleAssistant, Content: longAssistant},
	}

	summary := SummarizeParentState(msgs)

	// User messages are truncated to 200 chars + "…"
	if strings.Contains(summary, strings.Repeat("a", 300)) {
		t.Error("expected user content to be truncated")
	}
	// Assistant content is truncated to 500 chars + "…"
	if strings.Contains(summary, strings.Repeat("b", 600)) {
		t.Error("expected assistant content to be truncated")
	}
	if !strings.Contains(summary, "…") {
		t.Error("expected truncation marker")
	}
}

func TestSummarizeParentState_LimitsUserMessages(t *testing.T) {
	msgs := make([]agentcore.Message, 0, 10)
	for i := 0; i < 10; i++ {
		msgs = append(msgs, agentcore.Message{
			Role:    agentcore.RoleUser,
			Content: "request " + string(rune('0'+i)),
		})
	}

	summary := SummarizeParentState(msgs)

	// Should only include the last 3 user messages (requests 7, 8, 9)
	if strings.Contains(summary, "request 0") {
		t.Error("expected oldest user messages to be excluded")
	}
	if strings.Contains(summary, "request 6") {
		t.Error("expected request 6 to be excluded (only last 3 kept)")
	}
	if !strings.Contains(summary, "request 7") {
		t.Error("expected request 7 in summary (last 3)")
	}
	if !strings.Contains(summary, "request 8") {
		t.Error("expected request 8 in summary")
	}
	if !strings.Contains(summary, "request 9") {
		t.Error("expected request 9 in summary")
	}
}

func TestSummarizeParentState_SkipsNonStandardUserMessages(t *testing.T) {
	msgs := []agentcore.Message{
		{Role: agentcore.RoleUser, Content: "standard msg", Type: agentcore.MessageTypeCustom},
		{Role: agentcore.RoleUser, Content: "real request"},
	}

	summary := SummarizeParentState(msgs)

	if strings.Contains(summary, "standard msg") {
		t.Error("expected custom-type user message to be skipped")
	}
	if !strings.Contains(summary, "real request") {
		t.Error("expected standard user message in summary")
	}
}

// --- WithParentMessages / ParentMessagesFromContext tests ---

func TestWithParentMessages_RoundTrip(t *testing.T) {
	msgs := []agentcore.Message{
		{Role: agentcore.RoleUser, Content: "hello"},
		{Role: agentcore.RoleAssistant, Content: "hi"},
	}

	ctx := context.Background()
	if got := ParentMessagesFromContext(ctx); got != nil {
		t.Errorf("expected nil from bare context, got %v", got)
	}

	ctx = WithParentMessages(ctx, msgs)
	got := ParentMessagesFromContext(ctx)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[0].Content != "hello" || got[1].Content != "hi" {
		t.Errorf("message content mismatch: %v", got)
	}
}

func TestWithParentMessages_NilReturnsEmpty(t *testing.T) {
	ctx := WithParentMessages(context.Background(), nil)
	got := ParentMessagesFromContext(ctx)
	// nil slice stored — retrieval returns nil (empty)
	if len(got) != 0 {
		t.Errorf("expected 0 messages, got %d", len(got))
	}
}

// --- sessions_spawn tool context mode tests ---

func TestSessionsSpawn_IsolatedMode_NoParentContext(t *testing.T) {
	var capturedTask string
	var capturedCtx context.Context

	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		capturedTask = task
		capturedCtx = ctx
		return "isolated result", nil
	}

	parentMsgs := func() []agentcore.Message {
		return []agentcore.Message{{Role: agentcore.RoleUser, Content: "parent context"}}
	}

	tool := BuildSessionsSpawnTool(runner, nil, nil, parentMsgs)

	args, _ := json.Marshal(map[string]any{
		"task":         "do something",
		"context_mode": "isolated",
	})

	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Task should be unmodified
	if capturedTask != "do something" {
		t.Errorf("expected unmodified task, got %q", capturedTask)
	}

	// No parent messages in context
	if msgs := ParentMessagesFromContext(capturedCtx); msgs != nil {
		t.Errorf("expected no parent messages in isolated mode, got %v", msgs)
	}

	m := result.(map[string]any)
	if m["status"] != "completed" {
		t.Errorf("expected completed status, got %v", m["status"])
	}
}

func TestSessionsSpawn_StateMode_AugmentsTask(t *testing.T) {
	var capturedTask string

	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		capturedTask = task
		return "state result", nil
	}

	parentMsgs := func() []agentcore.Message {
		return []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "Fix the auth bug"},
			{Role: agentcore.RoleAssistant, Content: "I patched auth.go"},
		}
	}

	tool := BuildSessionsSpawnTool(runner, nil, nil, parentMsgs)

	args, _ := json.Marshal(map[string]any{
		"task":         "write tests for auth",
		"context_mode": "state",
	})

	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Task should be augmented with parent summary
	if !strings.Contains(capturedTask, "PARENT CONTEXT SUMMARY") {
		t.Error("expected task to contain parent context summary")
	}
	if !strings.Contains(capturedTask, "Fix the auth bug") {
		t.Error("expected task to contain parent user message")
	}
	if !strings.Contains(capturedTask, "Your Task") {
		t.Error("expected task to contain 'Your Task' section")
	}
	if !strings.Contains(capturedTask, "write tests for auth") {
		t.Error("expected original task to be appended")
	}

	m := result.(map[string]any)
	if m["status"] != "completed" {
		t.Errorf("expected completed status, got %v", m["status"])
	}
}

func TestSessionsSpawn_StateMode_NilParentMessages(t *testing.T) {
	var capturedTask string

	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		capturedTask = task
		return "result", nil
	}

	// No parent messages provider
	tool := BuildSessionsSpawnTool(runner, nil, nil, nil)

	args, _ := json.Marshal(map[string]any{
		"task":         "do something",
		"context_mode": "state",
	})

	_, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Task should be unmodified when no parent messages available
	if capturedTask != "do something" {
		t.Errorf("expected unmodified task, got %q", capturedTask)
	}
}

func TestSessionsSpawn_FullMode_SetsContext(t *testing.T) {
	var capturedCtx context.Context

	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		capturedCtx = ctx
		return "full result", nil
	}

	parentMsgs := func() []agentcore.Message {
		return []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "full parent context"},
			{Role: agentcore.RoleAssistant, Content: "parent response"},
		}
	}

	tool := BuildSessionsSpawnTool(runner, nil, nil, parentMsgs)

	args, _ := json.Marshal(map[string]any{
		"task":         "continue the work",
		"context_mode": "full",
	})

	_, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Parent messages should be in context
	msgs := ParentMessagesFromContext(capturedCtx)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 parent messages in context, got %d", len(msgs))
	}
	if msgs[0].Content != "full parent context" {
		t.Errorf("expected first parent message, got %q", msgs[0].Content)
	}
}

func TestSessionsSpawn_DefaultContextMode_Isolated(t *testing.T) {
	var capturedTask string
	var capturedCtx context.Context

	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		capturedTask = task
		capturedCtx = ctx
		return "result", nil
	}

	parentMsgs := func() []agentcore.Message {
		return []agentcore.Message{{Role: agentcore.RoleUser, Content: "should not appear"}}
	}

	tool := BuildSessionsSpawnTool(runner, nil, nil, parentMsgs)

	// No context_mode specified — should default to isolated
	args, _ := json.Marshal(map[string]any{
		"task": "do something",
	})

	_, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedTask != "do something" {
		t.Errorf("expected unmodified task in default mode, got %q", capturedTask)
	}
	if msgs := ParentMessagesFromContext(capturedCtx); msgs != nil {
		t.Errorf("expected no parent messages in default mode, got %v", msgs)
	}
}

// --- sessions_spawn_batch tool context mode tests ---

func TestSessionsSpawnBatch_PerTaskContextModes(t *testing.T) {
	type capture struct {
		task string
		ctx  context.Context
	}
	var captures struct {
		mu  chan struct{}
		got map[string]capture
	}
	captures.mu = make(chan struct{}, 1)
	captures.got = make(map[string]capture)

	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		// Extract task ID from task content to identify which task this is.
		// We use the context to store the task ID via a simple convention:
		// the batch tool passes the original task as Goal, but the taskText
		// may be augmented. We detect mode by checking task content.
		captures.mu <- struct{}{}
		var id string
		if strings.Contains(task, "isolated task") {
			id = "isolated"
		} else if strings.Contains(task, "state task") {
			id = "state"
		} else if strings.Contains(task, "full task") {
			id = "full"
		} else {
			id = "unknown"
		}
		captures.got[id] = capture{task: task, ctx: ctx}
		<-captures.mu
		return "done", nil
	}

	parentMsgs := func() []agentcore.Message {
		return []agentcore.Message{
			{Role: agentcore.RoleUser, Content: "parent request for context"},
		}
	}

	tool := BuildSessionsSpawnBatchTool(runner, nil, nil, parentMsgs)

	args, _ := json.Marshal(map[string]any{
		"tasks": []map[string]any{
			{"id": "isolated", "task": "isolated task", "context_mode": "isolated"},
			{"id": "state", "task": "state task", "context_mode": "state"},
			{"id": "full", "task": "full task", "context_mode": "full"},
		},
		"max_parallel": 3,
	})

	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := result.(map[string]any)
	if m["completed"].(int) != 3 {
		t.Fatalf("expected 3 completed, got %v", m["completed"])
	}

	// Isolated: task unmodified, no parent messages in context
	if c, ok := captures.got["isolated"]; ok {
		if c.task != "isolated task" {
			t.Errorf("isolated: expected unmodified task, got %q", c.task)
		}
		if msgs := ParentMessagesFromContext(c.ctx); msgs != nil {
			t.Errorf("isolated: expected no parent messages, got %v", msgs)
		}
	} else {
		t.Error("isolated task not captured")
	}

	// State: task augmented with summary
	if c, ok := captures.got["state"]; ok {
		if !strings.Contains(c.task, "PARENT CONTEXT SUMMARY") {
			t.Error("state: expected task to contain parent summary")
		}
		if !strings.Contains(c.task, "parent request for context") {
			t.Error("state: expected parent user message in task")
		}
		if !strings.Contains(c.task, "state task") {
			t.Error("state: expected original task appended")
		}
	} else {
		t.Error("state task not captured")
	}

	// Full: parent messages in context
	if c, ok := captures.got["full"]; ok {
		if c.task != "full task" {
			t.Errorf("full: expected unmodified task, got %q", c.task)
		}
		msgs := ParentMessagesFromContext(c.ctx)
		if len(msgs) != 1 {
			t.Errorf("full: expected 1 parent message in context, got %d", len(msgs))
		}
	} else {
		t.Error("full task not captured")
	}
}

func TestSessionsSpawnBatch_DefaultContextMode_Isolated(t *testing.T) {
	var capturedTask string
	var capturedCtx context.Context
	var gotMu chan struct{} = make(chan struct{}, 1)

	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		gotMu <- struct{}{}
		capturedTask = task
		capturedCtx = ctx
		<-gotMu
		return "done", nil
	}

	parentMsgs := func() []agentcore.Message {
		return []agentcore.Message{{Role: agentcore.RoleUser, Content: "should not appear"}}
	}

	tool := BuildSessionsSpawnBatchTool(runner, nil, nil, parentMsgs)

	// No context_mode specified — should default to isolated
	args, _ := json.Marshal(map[string]any{
		"tasks": []map[string]any{
			{"id": "t1", "task": "simple task"},
		},
	})

	_, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedTask != "simple task" {
		t.Errorf("expected unmodified task, got %q", capturedTask)
	}
	if msgs := ParentMessagesFromContext(capturedCtx); msgs != nil {
		t.Errorf("expected no parent messages in default mode, got %v", msgs)
	}
}
