package evolution

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxScriptOutput = 64000
	scriptTimeout   = 30 * time.Second
)

// ExecuteScript runs a script file from a skill's scripts/ directory.
// Only allows files under skills/<name>/scripts/.
func (sm *SkillManager) ExecuteScript(name, scriptRelPath string, args []string) (string, error) {
	var err error
	name, err = validateSkillName(name)
	if err != nil {
		return "", err
	}

	relPath, err := validateSkillRelPath(scriptRelPath)
	if err != nil {
		return "", err
	}

	skillDir := filepath.Join(sm.skillsDir, name)
	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		return "", fmt.Errorf("skill %q not found", name)
	}

	scriptPath := filepath.Join(skillDir, "scripts", relPath)

	realSkillDir, err := filepath.EvalSymlinks(skillDir)
	if err != nil {
		return "", fmt.Errorf("resolve skill dir: %w", err)
	}
	realScriptDir, err := filepath.EvalSymlinks(filepath.Dir(scriptPath))
	if err != nil {
		return "", fmt.Errorf("resolve script dir: %w", err)
	}

	scriptsDir := filepath.Join(realSkillDir, "scripts")
	if !strings.HasPrefix(realScriptDir, scriptsDir+string(filepath.Separator)) && realScriptDir != scriptsDir {
		return "", fmt.Errorf("script must be under scripts/ directory")
	}

	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return "", fmt.Errorf("script %q not found in skill %q", relPath, name)
	}

	return runScript(scriptPath, args, skillDir)
}

// ListScripts returns the names of all executable files in a skill's scripts/ directory.
func (sm *SkillManager) ListScripts(name string) ([]string, error) {
	var err error
	name, err = validateSkillName(name)
	if err != nil {
		return nil, err
	}

	scriptsDir := filepath.Join(sm.skillsDir, name, "scripts")
	if _, err := os.Stat(scriptsDir); os.IsNotExist(err) {
		return nil, nil
	}

	entries, err := os.ReadDir(scriptsDir)
	if err != nil {
		return nil, fmt.Errorf("read scripts dir: %w", err)
	}

	var scripts []string
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			scripts = append(scripts, e.Name())
		}
	}
	sort.Strings(scripts)
	return scripts, nil
}

func runScript(scriptPath string, args []string, workDir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), scriptTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, scriptPath, args...)
	cmd.Dir = workDir
	cmd.Env = os.Environ()

	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("script timed out after %v", scriptTimeout)
		}
		if len(out) > 0 {
			return "", fmt.Errorf("script failed: %s", string(out))
		}
		return "", fmt.Errorf("script failed: %w", err)
	}

	result := strings.TrimSpace(string(out))
	if len(result) > maxScriptOutput {
		result = result[:maxScriptOutput] + "...[truncated]"
	}
	return result, nil
}
