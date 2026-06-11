package backend

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

const defaultCodexExecPath = "codex"

// Execute spawns the Codex CLI as a subprocess, passing the prompt as a
// positional argument. It captures stdout into ExecuteResult.Output and
// surfaces stderr in the returned error when the process exits non-zero.
// Context cancellation terminates the child process promptly, which makes
// the method safe to use with deadline-scoped contexts.
func (c *CodexBackend) Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	execPath := c.ExecPath
	if execPath == "" {
		execPath = defaultCodexExecPath
	}

	args := []string{req.Prompt}

	cmd := exec.CommandContext(ctx, execPath, args...)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	if len(req.Env) > 0 {
		cmd.Env = req.Env
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	result := ExecuteResult{
		Output:     stdout.String(),
		DurationMs: duration.Milliseconds(),
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			return result, fmt.Errorf("codex exited with status %d: %s",
				exitErr.ExitCode(), stderr.String())
		}
		// Context cancellation, executable not found, or other spawn failure.
		return result, fmt.Errorf("codex execution failed: %w", err)
	}

	return result, nil
}
