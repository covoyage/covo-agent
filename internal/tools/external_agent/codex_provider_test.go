package externalagent

import (
	"context"
	"strings"
	"testing"
	"time"
)

// codexFakeScript is a minimal Codex CLI that speaks enough of the
// app-server JSON-RPC protocol to exercise the Go provider. $VERSION is the
// --version reply and the initialize userAgent; $THREAD_EPHEMERAL controls
// the thread/start reply; BODY is inserted after the turn/start reply.
const codexFakeScript = `case "$1" in
  --version) echo "$VERSION"; exit 0 ;;
  app-server)
    while IFS= read -r line; do
      case "$line" in
        *'"method":"initialize"'*)
          id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
          echo "{\"id\":$id,\"result\":{\"userAgent\":\"codex-cli/$VERSION\",\"codexHome\":\"/tmp/codex\",\"platformFamily\":\"unix\",\"platformOs\":\"darwin\"}}"
          ;;
        *'"method":"thread/start"'*)
          id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
          echo "{\"id\":$id,\"result\":{\"thread\":{\"id\":\"thr_1\",\"ephemeral\":$THREAD_EPHEMERAL}}}"
          ;;
        *'"method":"turn/start"'*)
          id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
          echo "{\"id\":$id,\"result\":{\"turn\":{\"id\":\"turn_1\"}}}"
          $BODY
          ;;
        *'"method":"turn/interrupt"'*)
          echo '{"method":"turn/completed","params":{"threadId":"thr_1","turn":{"id":"turn_1","status":"interrupted"}}}'
          ;;
      esac
    done
    exit 0
    ;;
esac
`

func writeCodexFake(t *testing.T, dir, version, body string) {
	t.Helper()
	script := strings.Replace(codexFakeScript, "$VERSION", version, 1)
	script = strings.Replace(script, "$THREAD_EPHEMERAL", "true", 1)
	script = strings.Replace(script, "$BODY", body, 1)
	writeFakeCLI(t, dir, "codex", script)
	withPath(t, dir)
}

const finalAnswerCompleted = `
      echo '{"method":"item/completed","params":{"threadId":"thr_1","turnId":"turn_1","item":{"type":"agentMessage","id":"a1","text":"final-ok","phase":"final_answer"}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thr_1","turn":{"id":"turn_1","status":"completed"}}}'
`

func TestCodexProviderRun(t *testing.T) {
	dir := t.TempDir()
	writeCodexFake(t, dir, "0.147.0", finalAnswerCompleted)

	p := CodexProvider()
	out, err := p.Run(context.Background(), "do it", "")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if out != "final-ok" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestCodexProviderUnphasedFallback(t *testing.T) {
	dir := t.TempDir()
	writeCodexFake(t, dir, "0.147.0", `
      echo '{"method":"item/completed","params":{"threadId":"thr_1","turnId":"turn_1","item":{"type":"agentMessage","id":"a1","text":"unphased-ok","phase":null}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thr_1","turn":{"id":"turn_1","status":"completed"}}}'
`)

	p := CodexProvider()
	out, err := p.Run(context.Background(), "do it", "")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if out != "unphased-ok" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestCodexProviderCommentaryIgnored(t *testing.T) {
	dir := t.TempDir()
	writeCodexFake(t, dir, "0.147.0", `
      echo '{"method":"item/completed","params":{"threadId":"thr_1","turnId":"turn_1","item":{"type":"agentMessage","id":"a1","text":"thinking aloud","phase":"commentary"}}}'
      echo '{"method":"item/completed","params":{"threadId":"thr_1","turnId":"turn_1","item":{"type":"agentMessage","id":"a2","text":"real-answer","phase":null}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thr_1","turn":{"id":"turn_1","status":"completed"}}}'
`)

	p := CodexProvider()
	out, err := p.Run(context.Background(), "do it", "")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if out != "real-answer" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestCodexProviderVersionTooOld(t *testing.T) {
	dir := t.TempDir()
	writeCodexFake(t, dir, "0.130.0", "")

	p := CodexProvider()
	_, err := p.Run(context.Background(), "do it", "")
	if err == nil {
		t.Fatal("expected version error")
	}
	if !strings.Contains(err.Error(), "too old") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCodexProviderContextWindowExceeded(t *testing.T) {
	dir := t.TempDir()
	writeCodexFake(t, dir, "0.147.0", `
      echo '{"method":"turn/completed","params":{"threadId":"thr_1","turn":{"id":"turn_1","status":"failed","error":{"codexErrorInfo":"contextWindowExceeded","message":"context window exceeded"}}}}'
`)

	p := CodexProvider()
	_, err := p.Run(context.Background(), "do it", "")
	if err == nil {
		t.Fatal("expected error for context window overflow")
	}
	if !strings.Contains(err.Error(), "context window exceeded") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCodexProviderTurnFailed(t *testing.T) {
	dir := t.TempDir()
	writeCodexFake(t, dir, "0.147.0", `
      echo '{"method":"turn/completed","params":{"threadId":"thr_1","turn":{"id":"turn_1","status":"failed","error":{"message":"model blew up"}}}}'
`)

	p := CodexProvider()
	_, err := p.Run(context.Background(), "do it", "")
	if err == nil {
		t.Fatal("expected error from failed turn")
	}
	if !strings.Contains(err.Error(), "model blew up") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCodexProviderNoAnswer(t *testing.T) {
	dir := t.TempDir()
	writeCodexFake(t, dir, "0.147.0", `
      echo '{"method":"turn/completed","params":{"threadId":"thr_1","turn":{"id":"turn_1","status":"completed"}}}'
`)

	p := CodexProvider()
	_, err := p.Run(context.Background(), "do it", "")
	if err == nil {
		t.Fatal("expected error when turn completes without an answer")
	}
	if !strings.Contains(err.Error(), "without a final answer") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCodexProviderApprovalCancel(t *testing.T) {
	dir := t.TempDir()
	writeCodexFake(t, dir, "0.147.0", `
      echo '{"id":99,"method":"item/commandExecution/requestApproval","params":{"threadId":"thr_1","turnId":"turn_1","availableDecisions":["cancel","approve"]}}'
      IFS= read -r resp
      case "$resp" in
        *'"decision":"cancel"'*) answer="cancel-ok" ;;
        *) answer="wrong-decision" ;;
      esac
      echo '{"method":"item/completed","params":{"threadId":"thr_1","turnId":"turn_1","item":{"type":"agentMessage","id":"a1","text":"'"$answer"'","phase":"final_answer"}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thr_1","turn":{"id":"turn_1","status":"completed"}}}'
`)

	p := CodexProvider()
	out, err := p.Run(context.Background(), "do it", "")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if out != "cancel-ok" {
		t.Fatalf("expected cancel decision, got %q", out)
	}
}

func TestCodexProviderApprovalNoDecisions(t *testing.T) {
	dir := t.TempDir()
	writeCodexFake(t, dir, "0.147.0", `
      echo '{"id":99,"method":"item/fileChange/requestApproval","params":{"threadId":"thr_1","turnId":"turn_1"}}'
      IFS= read -r resp
      case "$resp" in
        *'"decision":"decline"'*) answer="decline-ok" ;;
        *) answer="wrong-decision" ;;
      esac
      echo '{"method":"item/completed","params":{"threadId":"thr_1","turnId":"turn_1","item":{"type":"agentMessage","id":"a1","text":"'"$answer"'","phase":"final_answer"}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thr_1","turn":{"id":"turn_1","status":"completed"}}}'
`)

	p := CodexProvider()
	out, err := p.Run(context.Background(), "do it", "")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if out != "decline-ok" {
		t.Fatalf("expected decline decision, got %q", out)
	}
}

func TestCodexProviderBypassApproves(t *testing.T) {
	dir := t.TempDir()
	writeCodexFake(t, dir, "0.147.0", `
      echo '{"id":99,"method":"item/commandExecution/requestApproval","params":{"threadId":"thr_1","turnId":"turn_1","availableDecisions":["cancel","approve"]}}'
      IFS= read -r resp
      case "$resp" in
        *'"decision":"allow"'*) answer="allow-ok" ;;
        *) answer="wrong-decision" ;;
      esac
      echo '{"method":"item/completed","params":{"threadId":"thr_1","turnId":"turn_1","item":{"type":"agentMessage","id":"a1","text":"'"$answer"'","phase":"final_answer"}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thr_1","turn":{"id":"turn_1","status":"completed"}}}'
`)

	p := CodexProvider()
	out, err := p.RunOpt(context.Background(), "do it", "", RunOptions{
		PermissionMode: "bypassPermissions",
	})
	if err != nil {
		t.Fatalf("RunOpt failed: %v", err)
	}
	if out != "allow-ok" {
		t.Fatalf("expected allow decision under bypassPermissions, got %q", out)
	}
}

func TestCodexProviderBypassSetsApprovalPolicy(t *testing.T) {
	dir := t.TempDir()
	writeFakeCLI(t, dir, "codex", `
case "$1" in
  --version) echo "0.147.0"; exit 0 ;;
  app-server)
    while IFS= read -r line; do
      case "$line" in
        *'"method":"initialize"'*)
          id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
          echo "{\"id\":$id,\"result\":{\"userAgent\":\"codex-cli/0.147.0\",\"codexHome\":\"/tmp/codex\",\"platformFamily\":\"unix\",\"platformOs\":\"darwin\"}}"
          ;;
        *'"method":"thread/start"'*)
          id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
          echo "{\"id\":$id,\"result\":{\"thread\":{\"id\":\"thr_1\",\"ephemeral\":true}}}"
          ;;
        *'"method":"turn/start"'*)
          id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
          case "$line" in
            *'"approvalPolicy":"onRequest"'*) policy="with-policy" ;;
            *) policy="missing-policy" ;;
          esac
          echo "{\"id\":$id,\"result\":{\"turn\":{\"id\":\"turn_1\"}}}"
          echo '{"method":"item/completed","params":{"threadId":"thr_1","turnId":"turn_1","item":{"type":"agentMessage","id":"a1","text":"'"$policy"'","phase":"final_answer"}}}'
          echo '{"method":"turn/completed","params":{"threadId":"thr_1","turn":{"id":"turn_1","status":"completed"}}}'
          ;;
      esac
    done
    exit 0
    ;;
esac
`)
	withPath(t, dir)

	p := CodexProvider()
	out, err := p.RunOpt(context.Background(), "do it", "", RunOptions{
		PermissionMode: "bypassPermissions",
	})
	if err != nil {
		t.Fatalf("RunOpt failed: %v", err)
	}
	if out != "with-policy" {
		t.Fatalf("expected approvalPolicy onRequest under bypassPermissions, got %q", out)
	}
}

func TestCodexProviderUserInputRequest(t *testing.T) {
	dir := t.TempDir()
	writeCodexFake(t, dir, "0.147.0", `
      echo '{"id":99,"method":"item/tool/requestUserInput","params":{"threadId":"thr_1","turnId":"turn_1","questions":[{"id":"q1","prompt":"how many?"}]}}'
      IFS= read -r resp
      case "$resp" in
        *'"answers":{}'*) answer="no-input-ok" ;;
        *) answer="wrong-answer" ;;
      esac
      echo '{"method":"item/completed","params":{"threadId":"thr_1","turnId":"turn_1","item":{"type":"agentMessage","id":"a1","text":"'"$answer"'","phase":"final_answer"}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thr_1","turn":{"id":"turn_1","status":"completed"}}}'
`)

	p := CodexProvider()
	out, err := p.Run(context.Background(), "do it", "")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if out != "no-input-ok" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestCodexProviderUnknownRequest(t *testing.T) {
	dir := t.TempDir()
	writeCodexFake(t, dir, "0.147.0", `
      echo '{"id":99,"method":"some/unknown/serverRequest","params":{"threadId":"thr_1","turnId":"turn_1"}}'
      IFS= read -r resp
      echo '{"method":"turn/completed","params":{"threadId":"thr_1","turn":{"id":"turn_1","status":"completed"}}}'
`)

	p := CodexProvider()
	_, err := p.Run(context.Background(), "do it", "")
	if err == nil {
		t.Fatal("expected error from unknown server request")
	}
	if !strings.Contains(err.Error(), "unsupported server request") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCodexProviderInterrupt(t *testing.T) {
	dir := t.TempDir()
	writeCodexFake(t, dir, "0.147.0", `
      sleep 3
`)

	p := CodexProvider()
	// 1.5s leaves headroom for the handshake under load while still firing
	// before the fake app-server's 3s sleep finishes.
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := p.Run(ctx, "slow task", "")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected interrupt error")
	}
	if elapsed > 8*time.Second {
		t.Fatalf("interrupt not honoured, took %v", elapsed)
	}
	if !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCodexProviderExitsEarly(t *testing.T) {
	dir := t.TempDir()
	writeCodexFake(t, dir, "0.147.0", `
      exit 1
`)

	p := CodexProvider()
	_, err := p.Run(context.Background(), "do it", "")
	if err == nil {
		t.Fatal("expected error when app-server exits early")
	}
	if !strings.Contains(err.Error(), "without producing a result") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCodexProviderDiesDuringHandshake(t *testing.T) {
	// The app-server dies before the initialize reply; the run must fail
	// promptly rather than hang until the init timeout.
	dir := t.TempDir()
	writeFakeCLI(t, dir, "codex", `
case "$1" in
  --version) echo "0.147.0"; exit 0 ;;
  app-server) echo "fatal: no access token" >&2; exit 1 ;;
esac
`)
	withPath(t, dir)

	p := CodexProvider()
	start := time.Now()
	_, err := p.Run(context.Background(), "do it", "")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error when app-server dies during handshake")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("handshake failure not reported promptly, took %v", elapsed)
	}
	if !strings.Contains(err.Error(), "without producing a result") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "no access token") {
		t.Fatalf("expected stderr tail in error: %v", err)
	}
}

func TestCodexProviderThreadNotEphemeral(t *testing.T) {
	dir := t.TempDir()
	script := strings.Replace(codexFakeScript, "$VERSION", "0.147.0", 1)
	script = strings.Replace(script, "$THREAD_EPHEMERAL", "false", 1)
	script = strings.Replace(script, "$BODY", "", 1)
	writeFakeCLI(t, dir, "codex", script)
	withPath(t, dir)

	p := CodexProvider()
	_, err := p.Run(context.Background(), "do it", "")
	if err == nil {
		t.Fatal("expected error when thread is not ephemeral")
	}
	if !strings.Contains(err.Error(), "ephemeral") {
		t.Fatalf("unexpected error: %v", err)
	}
}
