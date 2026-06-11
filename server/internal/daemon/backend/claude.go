package backend

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Execute runs the Claude CLI as a subprocess and captures its output.
// The prompt is piped to the process's stdin as plain text. Stdout is
// captured as ExecuteResult.Output; stderr is attached to any error
// returned so callers get diagnostic context on failure.
func (b *ClaudeBackend) Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	execPath := b.ExecutablePath
	if execPath == "" {
		execPath = "claude"
	}

	logger := b.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Verify the executable is reachable before spawning.
	if _, err := exec.LookPath(execPath); err != nil {
		return ExecuteResult{}, fmt.Errorf("claude executable not found at %q: %w", execPath, err)
	}

	args := buildClaudeArgs(req)

	cmd := exec.CommandContext(ctx, execPath, args...)
	// WaitDelay bounds how long cmd.Wait() blocks after the process exits,
	// preventing orphaned child processes from holding stdout/stderr pipes
	// open indefinitely.
	cmd.WaitDelay = 5 * time.Second
	// Cancel kills the entire process group (on Unix) or just the process
	// (on other platforms) when the context is cancelled.
	cmd.Cancel = func() error {
		killProcGroup(cmd)
		return nil
	}
	// Run the process in its own process group (Unix only; no-op elsewhere)
	// so Cancel can kill all descendants.
	setProcGroup(cmd)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	if len(req.Env) > 0 {
		cmd.Env = append(os.Environ(), req.Env...)
	}

	// Pipe the prompt to stdin.
	cmd.Stdin = strings.NewReader(req.Prompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	logger.Info("claude: starting", "exec", execPath, "args", args, "cwd", req.WorkDir)

	startTime := time.Now()
	err := cmd.Run()
	duration := time.Since(startTime)

	if ctx.Err() != nil {
		return ExecuteResult{
			Output:     stdout.String(),
			DurationMs: duration.Milliseconds(),
		}, fmt.Errorf("%w: %v", ctx.Err(), stderrTail(stderr.String()))
	}

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return ExecuteResult{
				Output:     stdout.String(),
				DurationMs: duration.Milliseconds(),
			}, fmt.Errorf("claude process error: %w", err)
		}
		return ExecuteResult{
			Output:     stdout.String(),
			ExitCode:   exitCode,
			DurationMs: duration.Milliseconds(),
		}, fmt.Errorf("claude exited with code %d: %s", exitCode, stderrTail(stderr.String()))
	}

	logger.Info("claude: finished", "duration", duration.Round(time.Millisecond).String())

	return ExecuteResult{
		Output:     stdout.String(),
		ExitCode:   0,
		DurationMs: duration.Milliseconds(),
	}, nil
}

// buildClaudeArgs constructs the CLI argument list for the claude subprocess.
func buildClaudeArgs(req ExecuteRequest) []string {
	args := []string{"-p"}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.ThinkingLevel != "" {
		args = append(args, "--thinking", req.ThinkingLevel)
	}
	if req.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(req.MaxTurns))
	}
	return args
}

// stderrTail returns a trimmed, bounded view of stderr output for error messages.
func stderrTail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 4096 {
		s = s[len(s)-4096:]
	}
	return s
}
