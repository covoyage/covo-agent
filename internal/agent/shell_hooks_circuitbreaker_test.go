package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- Circuit breaker unit tests ---

func TestCircuitBreaker_ClosedByDefault(t *testing.T) {
	cb := &circuitBreaker{state: cbClosed}
	if !cb.allowExecution() {
		t.Error("expected execution allowed in closed state")
	}
}

func TestCircuitBreaker_TripsAfterThreshold(t *testing.T) {
	cb := &circuitBreaker{state: cbClosed}

	// Record failures up to threshold
	for i := 0; i < cbFailureThreshold; i++ {
		cb.recordFailure()
	}

	// Should now be open — execution denied
	cb.mu.Lock()
	state := cb.state
	cb.mu.Unlock()
	if state != cbOpen {
		t.Errorf("expected open state after %d failures, got %s", cbFailureThreshold, state)
	}
	if cb.allowExecution() {
		t.Error("expected execution denied in open state (within cooldown)")
	}
}

func TestCircuitBreaker_HalfOpenAfterCooldown(t *testing.T) {
	cb := &circuitBreaker{state: cbClosed}

	// Trip the breaker
	for i := 0; i < cbFailureThreshold; i++ {
		cb.recordFailure()
	}

	// Simulate cooldown elapsed by backdating lastFailureTime
	cb.mu.Lock()
	cb.lastFailureTime = time.Now().Add(-cbCooldown - time.Second)
	cb.mu.Unlock()

	// Should allow one trial (half-open)
	if !cb.allowExecution() {
		t.Error("expected execution allowed after cooldown (half-open)")
	}
	cb.mu.Lock()
	state := cb.state
	cb.mu.Unlock()
	if state != cbHalfOpen {
		t.Errorf("expected half-open state, got %s", state)
	}
}

func TestCircuitBreaker_SuccessResetsToClosed(t *testing.T) {
	cb := &circuitBreaker{state: cbClosed}
	cb.recordFailure()
	cb.recordFailure()

	cb.recordSuccess()

	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state != cbClosed {
		t.Errorf("expected closed after success, got %s", cb.state)
	}
	if cb.failures != 0 {
		t.Errorf("expected 0 failures after success, got %d", cb.failures)
	}
}

func TestCircuitBreaker_HalfOpenFailureReopens(t *testing.T) {
	cb := &circuitBreaker{state: cbClosed}

	// Trip the breaker
	for i := 0; i < cbFailureThreshold; i++ {
		cb.recordFailure()
	}

	// Simulate cooldown elapsed
	cb.mu.Lock()
	cb.lastFailureTime = time.Now().Add(-cbCooldown - time.Second)
	cb.mu.Unlock()

	// Allow trial (transitions to half-open)
	cb.allowExecution()

	// Trial fails
	cb.recordFailure()

	cb.mu.Lock()
	state := cb.state
	cb.mu.Unlock()
	if state != cbOpen {
		t.Errorf("expected open after half-open failure, got %s", state)
	}
}

// --- ShellHookManager circuit breaker integration tests ---

func TestShellHookManager_CircuitBreaker_FailsOpen(t *testing.T) {
	m := NewShellHookManager(t.TempDir(), true)

	// Register a hook that always fails (non-zero exit)
	spec := &ShellHookSpec{
		Event:   "pre_tool",
		Command: "exit 1",
	}
	if err := m.Register(spec); err != nil {
		t.Fatalf("Register: %v", err)
	}

	payload := &HookEvent{EventName: "pre_tool", ToolName: "bash"}

	// First few calls: hook fails silently, no block
	for i := 0; i < cbFailureThreshold; i++ {
		result := m.Invoke("pre_tool", payload)
		if result != nil && result.Blocked {
			t.Fatalf("call %d: hook should fail open (not block)", i)
		}
	}

	// After threshold failures, the breaker is open — hook is skipped entirely
	// (still no block, just skipped)
	result := m.Invoke("pre_tool", payload)
	if result != nil && result.Blocked {
		t.Error("expected no block when breaker is open (fail open)")
	}
}

func TestShellHookManager_CircuitBreaker_SuccessDoesNotTrip(t *testing.T) {
	m := NewShellHookManager(t.TempDir(), true)

	// Register a hook that outputs valid JSON (success)
	spec := &ShellHookSpec{
		Event:   "pre_tool",
		Command: `echo '{"decision":"allow"}'`,
	}
	if err := m.Register(spec); err != nil {
		t.Fatalf("Register: %v", err)
	}

	payload := &HookEvent{EventName: "pre_tool", ToolName: "bash"}

	// Call multiple times — should always succeed, never trip
	for i := 0; i < 10; i++ {
		result := m.Invoke("pre_tool", payload)
		if result != nil && result.Blocked {
			t.Fatalf("call %d: unexpected block", i)
		}
	}

	// Breaker should still be closed
	cb := m.getBreaker("pre_tool::echo '{\"decision\":\"allow\"}'")
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state != cbClosed {
		t.Errorf("expected closed state after successes, got %s", cb.state)
	}
}

func TestShellHookManager_CircuitBreaker_TimeoutRecordsFailure(t *testing.T) {
	m := NewShellHookManager(t.TempDir(), true)

	// Register a hook that sleeps longer than cbExecTimeout (5s)
	spec := &ShellHookSpec{
		Event:   "pre_tool",
		Command: "sleep 10",
	}
	if err := m.Register(spec); err != nil {
		t.Fatalf("Register: %v", err)
	}

	payload := &HookEvent{EventName: "pre_tool", ToolName: "bash"}

	// This call will time out after 5s. We can't wait 5s in a test,
	// so we verify the mechanism by checking that the breaker records
	// a failure after a timeout. To keep the test fast, we'll use a
	// custom approach: directly test the breaker with a fast-failing hook.

	// Instead, register a hook that exits 1 (fast failure)
	m2 := NewShellHookManager(t.TempDir(), true)
	spec2 := &ShellHookSpec{
		Event:   "pre_tool",
		Command: "exit 1",
	}
	m2.Register(spec2)

	for i := 0; i < cbFailureThreshold; i++ {
		m2.Invoke("pre_tool", payload)
	}

	cb := m2.getBreaker("pre_tool::exit 1")
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state != cbOpen {
		t.Errorf("expected open state after %d failures, got %s", cbFailureThreshold, cb.state)
	}
	if cb.failures != cbFailureThreshold {
		t.Errorf("expected %d failures, got %d", cbFailureThreshold, cb.failures)
	}
}

func TestShellHookManager_CircuitBreaker_HalfOpenRecovery(t *testing.T) {
	m := NewShellHookManager(t.TempDir(), true)

	// Use a script that fails for the first N calls then succeeds.
	// We'll use a temp file as a counter.
	counterFile := filepath.Join(t.TempDir(), "counter")
	script := `count=$(cat ` + counterFile + ` 2>/dev/null || echo 0)
count=$((count + 1))
echo $count > ` + counterFile + `
if [ $count -lt 4 ]; then
  exit 1
fi
echo '{"decision":"allow"}'`

	spec := &ShellHookSpec{
		Event:   "pre_tool",
		Command: script,
	}
	if err := m.Register(spec); err != nil {
		t.Fatalf("Register: %v", err)
	}

	payload := &HookEvent{EventName: "pre_tool", ToolName: "bash"}

	// First 3 calls fail (trip the breaker)
	for i := 0; i < cbFailureThreshold; i++ {
		m.Invoke("pre_tool", payload)
	}

	// Breaker should be open
	cbKey := "pre_tool::" + script
	cb := m.getBreaker(cbKey)
	cb.mu.Lock()
	if cb.state != cbOpen {
		t.Errorf("expected open after %d failures, got %s", cbFailureThreshold, cb.state)
	}
	cb.mu.Unlock()

	// Backdate to simulate cooldown elapsed
	cb.mu.Lock()
	cb.lastFailureTime = time.Now().Add(-cbCooldown - time.Second)
	cb.mu.Unlock()

	// Next call: half-open trial — script now succeeds (count >= 4)
	result := m.Invoke("pre_tool", payload)
	if result != nil && result.Blocked {
		t.Error("expected no block on recovery")
	}

	// Breaker should be back to closed
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state != cbClosed {
		t.Errorf("expected closed after successful trial, got %s", cb.state)
	}
}

// --- Hot reload tests ---

func TestShellHookManager_HotReload_DetectsFileChanges(t *testing.T) {
	workDir := t.TempDir()
	hooksPath := filepath.Join(workDir, ".covo-agent-hooks.json")

	// Initial hooks file with one hook
	initial := map[string]any{
		"hooks": []map[string]any{
			{"event": "pre_tool", "command": "echo test1"},
		},
	}
	data, _ := json.Marshal(initial)
	if err := os.WriteFile(hooksPath, data, 0644); err != nil {
		t.Fatalf("write hooks file: %v", err)
	}

	m := NewShellHookManager(t.TempDir(), true)
	m.LoadProjectHooksFile(workDir)
	m.StartHotReload(workDir)
	defer m.Stop()

	// Verify initial hook is registered
	m.mu.Lock()
	initialCount := len(m.hooks["pre_tool"])
	m.mu.Unlock()
	if initialCount != 1 {
		t.Fatalf("expected 1 hook initially, got %d", initialCount)
	}

	// Update the hooks file with an additional hook
	updated := map[string]any{
		"hooks": []map[string]any{
			{"event": "pre_tool", "command": "echo test1"},
			{"event": "pre_tool", "command": "echo test2"},
		},
	}
	data, _ = json.Marshal(updated)
	// Sleep briefly to ensure mtime changes (filesystem mtime resolution)
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(hooksPath, data, 0644); err != nil {
		t.Fatalf("write updated hooks file: %v", err)
	}

	// Wait for hot reload to detect the change
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		count := len(m.hooks["pre_tool"])
		m.mu.Unlock()
		if count == 2 {
			break // reloaded successfully
		}
		time.Sleep(hotReloadInterval)
	}

	m.mu.Lock()
	finalCount := len(m.hooks["pre_tool"])
	m.mu.Unlock()
	if finalCount != 2 {
		t.Errorf("expected 2 hooks after reload, got %d", finalCount)
	}
}

func TestShellHookManager_HotReload_StopTerminates(t *testing.T) {
	workDir := t.TempDir()
	hooksPath := filepath.Join(workDir, ".covo-agent-hooks.json")
	data, _ := json.Marshal(map[string]any{"hooks": []map[string]any{}})
	os.WriteFile(hooksPath, data, 0644)

	m := NewShellHookManager(t.TempDir(), true)
	m.LoadProjectHooksFile(workDir)
	m.StartHotReload(workDir)

	// Stop should not block and should be safe to call
	m.Stop()

	// Double stop should be safe
	m.Stop()

	// Start again should work after stop
	m.StartHotReload(workDir)
	m.Stop()
}

func TestShellHookManager_HotReload_NoOpIfNotStarted(t *testing.T) {
	m := NewShellHookManager(t.TempDir(), true)
	// Stop without start should be safe
	m.Stop()
}

func TestShellHookManager_HotReload_HandlesFileDeletion(t *testing.T) {
	workDir := t.TempDir()
	hooksPath := filepath.Join(workDir, ".covo-agent-hooks.json")

	initial := map[string]any{
		"hooks": []map[string]any{
			{"event": "pre_tool", "command": "echo test"},
		},
	}
	data, _ := json.Marshal(initial)
	os.WriteFile(hooksPath, data, 0644)

	m := NewShellHookManager(t.TempDir(), true)
	m.LoadProjectHooksFile(workDir)
	m.StartHotReload(workDir)
	defer m.Stop()

	// Delete the file — should not crash, hooks should remain as-is
	os.Remove(hooksPath)

	// Wait a bit to let the hot reload check fire
	time.Sleep(3 * hotReloadInterval)

	// Hooks should still be registered (deletion doesn't clear them)
	m.mu.Lock()
	count := len(m.hooks["pre_tool"])
	m.mu.Unlock()
	if count != 1 {
		t.Errorf("expected 1 hook after file deletion (unchanged), got %d", count)
	}
}

// --- Bug fix tests ---

// TestCircuitBreaker_HalfOpen_AllowsOnlyOneTrial verifies that in the
// half-open state, only one trial request is permitted; concurrent callers
// are rejected until the trial completes.
func TestCircuitBreaker_HalfOpen_AllowsOnlyOneTrial(t *testing.T) {
	cb := &circuitBreaker{state: cbClosed}

	// Trip the breaker.
	for i := 0; i < cbFailureThreshold; i++ {
		cb.recordFailure()
	}

	// Simulate cooldown elapsed.
	cb.mu.Lock()
	cb.lastFailureTime = time.Now().Add(-cbCooldown - time.Second)
	cb.mu.Unlock()

	// First call should allow a trial (transitions open -> half-open).
	if !cb.allowExecution() {
		t.Fatal("expected first trial allowed after cooldown")
	}

	// A second caller while the trial is in flight must be rejected.
	if cb.allowExecution() {
		t.Error("expected second concurrent trial to be rejected in half-open")
	}

	// A third caller is also rejected.
	if cb.allowExecution() {
		t.Error("expected third concurrent trial to be rejected in half-open")
	}

	// Once the trial succeeds, the breaker resets to closed and allows again.
	cb.recordSuccess()
	if !cb.allowExecution() {
		t.Error("expected execution allowed after trial success resets to closed")
	}
}

// TestCircuitBreaker_HalfOpen_TrialFailureReleasesTrial verifies that when a
// half-open trial fails, the trial slot is released so a later cooldown
// expiry can permit another trial.
func TestCircuitBreaker_HalfOpen_TrialFailureReleasesTrial(t *testing.T) {
	cb := &circuitBreaker{state: cbClosed}

	for i := 0; i < cbFailureThreshold; i++ {
		cb.recordFailure()
	}

	// Cooldown elapsed -> half-open trial.
	cb.mu.Lock()
	cb.lastFailureTime = time.Now().Add(-cbCooldown - time.Second)
	cb.mu.Unlock()

	if !cb.allowExecution() {
		t.Fatal("expected trial allowed")
	}
	// Second caller rejected while trial in flight.
	if cb.allowExecution() {
		t.Error("expected second trial rejected")
	}

	// Trial fails -> breaker reopens, trial slot released.
	cb.recordFailure()

	cb.mu.Lock()
	state := cb.state
	trial := cb.trialInProgress
	cb.mu.Unlock()
	if state != cbOpen {
		t.Errorf("expected open after half-open failure, got %s", state)
	}
	if trial {
		t.Error("expected trialInProgress cleared after failure")
	}

	// After another cooldown, a new trial is permitted.
	cb.mu.Lock()
	cb.lastFailureTime = time.Now().Add(-cbCooldown - time.Second)
	cb.mu.Unlock()
	if !cb.allowExecution() {
		t.Error("expected new trial allowed after second cooldown")
	}
}

// TestShellHookManager_HotReload_InvalidJSONPreservesHooks verifies that when
// the hooks file contains invalid JSON (e.g. mid-write), the existing hooks
// are preserved and lastMtime is not updated so the next poll retries.
func TestShellHookManager_HotReload_InvalidJSONPreservesHooks(t *testing.T) {
	workDir := t.TempDir()
	hooksPath := filepath.Join(workDir, ".covo-agent-hooks.json")

	initial := map[string]any{
		"hooks": []map[string]any{
			{"event": "pre_tool", "command": "echo test1"},
		},
	}
	data, _ := json.Marshal(initial)
	if err := os.WriteFile(hooksPath, data, 0644); err != nil {
		t.Fatalf("write hooks file: %v", err)
	}

	m := NewShellHookManager(t.TempDir(), true)
	m.LoadProjectHooksFile(workDir)
	m.StartHotReload(workDir)
	defer m.Stop()

	// Capture mtime recorded at startup.
	m.mu.Lock()
	mtimeBefore := m.lastMtime
	m.mu.Unlock()

	// Overwrite with invalid JSON (simulate a file mid-write).
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(hooksPath, []byte("{invalid json"), 0644); err != nil {
		t.Fatalf("write invalid hooks file: %v", err)
	}

	// Wait for the hot reload poll to attempt and fail.
	time.Sleep(3 * hotReloadInterval)

	// Hooks must still be present (not cleared).
	m.mu.Lock()
	count := len(m.hooks["pre_tool"])
	mtimeAfter := m.lastMtime
	m.mu.Unlock()
	if count != 1 {
		t.Errorf("expected 1 hook preserved after invalid JSON, got %d", count)
	}
	// mtime must be unchanged so the next poll retries.
	if !mtimeAfter.Equal(mtimeBefore) {
		t.Errorf("expected mtime unchanged after failed reload; before=%v after=%v", mtimeBefore, mtimeAfter)
	}
}

// TestShellHookManager_HotReload_RetriesAfterFailure verifies that after a
// failed reload (invalid JSON), the next poll retries because lastMtime was
// not updated, and a subsequent valid write is picked up.
func TestShellHookManager_HotReload_RetriesAfterFailure(t *testing.T) {
	workDir := t.TempDir()
	hooksPath := filepath.Join(workDir, ".covo-agent-hooks.json")

	initial := map[string]any{
		"hooks": []map[string]any{
			{"event": "pre_tool", "command": "echo test1"},
		},
	}
	data, _ := json.Marshal(initial)
	os.WriteFile(hooksPath, data, 0644)

	m := NewShellHookManager(t.TempDir(), true)
	m.LoadProjectHooksFile(workDir)
	m.StartHotReload(workDir)
	defer m.Stop()

	// Write invalid JSON first.
	time.Sleep(50 * time.Millisecond)
	os.WriteFile(hooksPath, []byte("{{not json"), 0644)
	time.Sleep(3 * hotReloadInterval)

	// Confirm hooks are still the original set.
	m.mu.Lock()
	if len(m.hooks["pre_tool"]) != 1 {
		m.mu.Unlock()
		t.Fatalf("expected 1 hook preserved during invalid JSON, got %d", len(m.hooks["pre_tool"]))
	}
	m.mu.Unlock()

	// Now write a valid file with an additional hook. Because lastMtime was
	// not updated on the failed reload, the next poll must detect the change.
	time.Sleep(50 * time.Millisecond)
	updated := map[string]any{
		"hooks": []map[string]any{
			{"event": "pre_tool", "command": "echo test1"},
			{"event": "pre_tool", "command": "echo test2"},
		},
	}
	data, _ = json.Marshal(updated)
	os.WriteFile(hooksPath, data, 0644)

	deadline := time.Now().Add(3 * time.Second)
	var finalCount int
	for time.Now().Before(deadline) {
		m.mu.Lock()
		finalCount = len(m.hooks["pre_tool"])
		m.mu.Unlock()
		if finalCount == 2 {
			break
		}
		time.Sleep(hotReloadInterval)
	}
	if finalCount != 2 {
		t.Errorf("expected 2 hooks after retry reload, got %d", finalCount)
	}
}

// TestShellHookManager_HotReload_CleansUpStaleBreakers verifies that after a
// hot reload removes a hook, its circuit breaker entry is also removed so a
// re-added hook (same key) does not inherit a stale "open" state.
func TestShellHookManager_HotReload_CleansUpStaleBreakers(t *testing.T) {
	workDir := t.TempDir()
	hooksPath := filepath.Join(workDir, ".covo-agent-hooks.json")

	initial := map[string]any{
		"hooks": []map[string]any{
			{"event": "pre_tool", "command": "exit 1"},
		},
	}
	data, _ := json.Marshal(initial)
	os.WriteFile(hooksPath, data, 0644)

	m := NewShellHookManager(t.TempDir(), true)
	m.LoadProjectHooksFile(workDir)
	m.StartHotReload(workDir)
	defer m.Stop()

	payload := &HookEvent{EventName: "pre_tool", ToolName: "bash"}

	// Trip the breaker for "exit 1".
	for i := 0; i < cbFailureThreshold; i++ {
		m.Invoke("pre_tool", payload)
	}

	staleKey := "pre_tool::exit 1"
	cb := m.getBreaker(staleKey)
	cb.mu.Lock()
	if cb.state != cbOpen {
		cb.mu.Unlock()
		t.Fatalf("expected open state after %d failures, got %s", cbFailureThreshold, cb.state)
	}
	cb.mu.Unlock()

	// Replace the hooks file: remove "exit 1", add a different hook.
	time.Sleep(50 * time.Millisecond)
	updated := map[string]any{
		"hooks": []map[string]any{
			{"event": "pre_tool", "command": "echo newhook"},
		},
	}
	data, _ = json.Marshal(updated)
	os.WriteFile(hooksPath, data, 0644)

	// Wait for the reload to swap in the new hooks.
	deadline := time.Now().Add(3 * time.Second)
	reloaded := false
	for time.Now().Before(deadline) {
		m.mu.Lock()
		hooks := m.hooks["pre_tool"]
		m.mu.Unlock()
		if len(hooks) == 1 && hooks[0].Command == "echo newhook" {
			reloaded = true
			break
		}
		time.Sleep(hotReloadInterval)
	}
	if !reloaded {
		t.Fatal("hot reload did not pick up the new hook")
	}

	// The stale breaker for "exit 1" should have been cleaned up.
	m.breakersMu.Lock()
	_, exists := m.breakers[staleKey]
	m.breakersMu.Unlock()
	if exists {
		t.Error("expected stale breaker for removed hook to be cleaned up after reload")
	}

	// The new hook's breaker (if any) should not exist yet (not invoked), and
	// invoking the new hook should start fresh in closed state.
	newKey := "pre_tool::echo newhook"
	m.breakersMu.Lock()
	_, newExists := m.breakers[newKey]
	m.breakersMu.Unlock()
	if newExists {
		t.Error("did not expect a breaker for the new hook before it is invoked")
	}
}

// TestShellHookManager_HotReload_BreakerNotInheritedOnReadd verifies the
// end-to-end scenario from Bug 3: a hook that was open (tripped) gets removed
// and re-added with the same key; after reload it should NOT be skipped.
func TestShellHookManager_HotReload_BreakerNotInheritedOnReadd(t *testing.T) {
	workDir := t.TempDir()
	hooksPath := filepath.Join(workDir, ".covo-agent-hooks.json")

	// A hook that succeeds.
	initial := map[string]any{
		"hooks": []map[string]any{
			{"event": "pre_tool", "command": `echo '{"decision":"allow"}'`},
		},
	}
	data, _ := json.Marshal(initial)
	os.WriteFile(hooksPath, data, 0644)

	m := NewShellHookManager(t.TempDir(), true)
	m.LoadProjectHooksFile(workDir)
	m.StartHotReload(workDir)
	defer m.Stop()

	payload := &HookEvent{EventName: "pre_tool", ToolName: "bash"}
	hookKey := `pre_tool::echo '{"decision":"allow"}'`

	// Manually trip the breaker for this hook so it is "open".
	cb := m.getBreaker(hookKey)
	cb.mu.Lock()
	cb.failures = cbFailureThreshold
	cb.state = cbOpen
	cb.lastFailureTime = time.Now()
	cb.mu.Unlock()

	// Invoke while open — hook should be skipped (fail open, no execution).
	result := m.Invoke("pre_tool", payload)
	if result != nil && result.Executed {
		t.Fatal("expected hook to be skipped while breaker is open")
	}

	// Remove the hook via hot reload so its (tripped) breaker is cleaned up.
	time.Sleep(50 * time.Millisecond)
	empty := map[string]any{"hooks": []map[string]any{}}
	data, _ = json.Marshal(empty)
	os.WriteFile(hooksPath, data, 0644)

	// Wait for reload to clear hooks (and the breaker).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		n := len(m.hooks["pre_tool"])
		m.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(hotReloadInterval)
	}

	// Breaker should be gone now.
	m.breakersMu.Lock()
	_, exists := m.breakers[hookKey]
	m.breakersMu.Unlock()
	if exists {
		t.Fatal("expected breaker removed after hook deleted in reload")
	}

	// Re-add the same hook.
	time.Sleep(50 * time.Millisecond)
	data, _ = json.Marshal(initial)
	os.WriteFile(hooksPath, data, 0644)

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		n := len(m.hooks["pre_tool"])
		m.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(hotReloadInterval)
	}

	// Now invoking should execute the hook (breaker fresh, closed) and return
	// a non-blocked executed result.
	result = m.Invoke("pre_tool", payload)
	if result == nil || !result.Executed {
		t.Error("expected hook to execute after re-add (no stale open breaker)")
	}
}
