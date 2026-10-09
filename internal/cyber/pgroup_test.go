package cyber

import (
	"context"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestConfigureProcessGroupSetsPgid(t *testing.T) {
	cmd := exec.Command("true")
	ConfigureProcessGroup(cmd)
	if runtime.GOOS == "windows" {
		return
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("Setpgid not configured")
	}
}

// Regression: a grandchild inheriting the stdout pipe must not hang Wait.
// "sleep 30 & wait" leaves the sleep holding the pipe; without the process
// group kill, Wait blocks for 30s after the shell dies. The group kill ends
// both, so Run returns shortly after cancellation.
func TestProcessGroupKillUnblocksWait(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups are unix-only")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", "sleep 30 & wait")
	ConfigureProcessGroup(cmd)
	cmd.Cancel = KillTree(cmd)
	cmd.WaitDelay = time.Second

	start := time.Now()
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	_ = cmd.Run()
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("process group kill did not unblock Wait: %v", elapsed)
	}
}
