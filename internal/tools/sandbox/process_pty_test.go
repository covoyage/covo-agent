package sandbox

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSpawnPipeIsNotTTY(t *testing.T) {
	store := NewProcessStore()
	session, err := store.Spawn("test -t 0; echo exit:$?", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if session.PTY {
		t.Fatal("expected pipe spawn")
	}
	got := store.Wait(session.ID, 5)
	out, _ := got["output"].(string)
	if !strings.Contains(out, "exit:1") {
		t.Fatalf("expected stdin not a tty, got %q", out)
	}
}

func TestSpawnPTYSeesTTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		store := NewProcessStore()
		_, err := store.Spawn("echo hi", "", true)
		if err == nil {
			t.Fatal("expected pty spawn to fail on Windows")
		}
		return
	}
	store := NewProcessStore()
	session, err := store.Spawn("test -t 0 && test -t 1 && echo IS_TTY", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !session.PTY {
		t.Fatal("expected pty spawn")
	}
	got := store.Wait(session.ID, 5)
	out, _ := got["output"].(string)
	if !strings.Contains(out, "IS_TTY") {
		t.Fatalf("expected IS_TTY, got %q status=%v", out, got)
	}
}

func TestSendKeysInterruptPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty not supported on Windows")
	}
	store := NewProcessStore()
	session, err := store.Spawn(`trap 'echo GOT_INT; exit 0' INT; sleep 30`, "", true)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if store.Get(session.ID) != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)
	res := store.SendKeys(session.ID, "C-c")
	if res["status"] != "ok" {
		t.Fatalf("send_keys=%v", res)
	}
	got := store.Wait(session.ID, 5)
	if got["status"] != "exited" {
		t.Fatalf("expected process to exit after C-c, got %v", got)
	}
}
