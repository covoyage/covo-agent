package codemode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultTimeout = 60 * time.Second
	maxOutputBytes = 100 * 1024 // 100KB
)

// ToolExecutor is a function that executes a tool by name with the given arguments.
type ToolExecutor func(ctx context.Context, name string, args json.RawMessage) (any, error)

// RunResult holds the outcome of a code execution.
type RunResult struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	Duration string `json:"duration"`
	TimedOut bool   `json:"timed_out,omitempty"`
}

// Execute writes a Go program with SDK tool bindings to a temp directory,
// runs it via `go run`, and handles IPC for tool calls.
//
// IPC protocol:
//   - Child writes tool requests to stderr (JSON lines)
//   - Child writes user output to stdout (fmt.Println etc.)
//   - Parent reads requests from stderr pipe, writes responses to stdin
//   - Parent captures stdout to a strings.Builder
func Execute(ctx context.Context, tools []ToolInfo, userCode string, executor ToolExecutor, timeout time.Duration) (*RunResult, error) {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dir, err := os.MkdirTemp("", "codemode-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	sdkCode := GenerateSDK(tools, userCode)

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(sdkCode), 0644); err != nil {
		return nil, fmt.Errorf("write main.go: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module codemode-run\n\ngo 1.21\n"), 0644); err != nil {
		return nil, fmt.Errorf("write go.mod: %w", err)
	}

	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = dir

	// Stderr pipe: child writes tool requests here
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	// Stdin pipe: parent writes tool responses here
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	// Stdout: capture user output (fmt.Println etc.)
	var out strings.Builder
	cmd.Stdout = &out

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start go run: %w", err)
	}

	// Read tool requests from stderr, handle IPC
	stderrScanner := bufio.NewScanner(stderrPipe)
	stderrScanner.Buffer(make([]byte, maxOutputBytes), maxOutputBytes)
	stdinWriter := bufio.NewWriter(stdin)

	ipcErr, stderrOutput := handleToolCalls(ctx, stderrScanner, stdinWriter, executor)
	_ = ipcErr // non-nil only on scanner errors, not compilation errors

	// Force-kill on timeout
	if ctx.Err() == context.DeadlineExceeded {
		_ = cmd.Process.Kill()
		<-waitDone(cmd)
		return &RunResult{
			Output:   out.String(),
			ExitCode: -1,
			Duration: timeout.String(),
			TimedOut: true,
		}, nil
	}

	waitErr := cmd.Wait()
	_ = stdin.Close()

	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	stderr := strings.TrimSpace(stderrOutput)

	result := &RunResult{
		Output:   strings.TrimSpace(out.String()),
		ExitCode: exitCode,
		Duration: time.Since(time.Now().Add(-timeout)).String(),
	}
	if stderr != "" {
		result.Output += "\n--- stderr ---\n" + stderr
	}

	// Compilation errors: go run exits non-zero with no stdout
	if exitCode != 0 && out.Len() == 0 && stderr != "" {
		return result, fmt.Errorf("compilation failed — fix the code and try again:\n%s\n\nGenerated code:\n%s", stderr, sdkCode)
	}

	return result, nil
}

// handleToolCalls reads tool call requests from the child's stderr,
// executes them via the executor, and writes responses to the child's stdin.
// Returns any error and accumulated non-JSON stderr output.
func handleToolCalls(ctx context.Context, scanner *bufio.Scanner, writer *bufio.Writer, executor ToolExecutor) (error, string) {
	var stderrLines []string
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req ToolCallRequest
		if err := json.Unmarshal(line, &req); err != nil {
			// Not a JSON tool request — accumulate as stderr (compiler errors, etc.)
			stderrLines = append(stderrLines, string(line))
			continue
		}

		result, execErr := executor(ctx, req.Tool, req.Args)

		var resp ToolCallResponse
		if execErr != nil {
			resp.Error = execErr.Error()
		} else {
			b, err := json.Marshal(result)
			if err != nil {
				resp.Error = fmt.Sprintf("marshal result: %v", err)
			} else {
				resp.Result = json.RawMessage(b)
			}
		}

		respBytes, _ := json.Marshal(resp)
		writeResponse(writer, respBytes)
	}
	return scanner.Err(), strings.Join(stderrLines, "\n")
}

func writeResponse(writer *bufio.Writer, data []byte) {
	writer.Write(data)
	writer.WriteByte('\n')
	writer.Flush()
}

func waitDone(cmd *exec.Cmd) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(ch)
	}()
	return ch
}
