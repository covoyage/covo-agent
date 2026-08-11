package cli

import (
	"os/exec"
)

// copyToClipboard copies text to the system clipboard.
func CopyToClipboard(text string) {
	if text == "" {
		return
	}
	var cmd *exec.Cmd
	if _, err := exec.LookPath("pbcopy"); err == nil {
		cmd = exec.Command("pbcopy")
	} else if _, err := exec.LookPath("xclip"); err == nil {
		cmd = exec.Command("xclip", "-selection", "clipboard")
	} else if _, err := exec.LookPath("wl-copy"); err == nil {
		cmd = exec.Command("wl-copy")
	} else {
		return
	}
	w, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}
	w.Write([]byte(text))
	w.Close()
	cmd.Wait()
}
