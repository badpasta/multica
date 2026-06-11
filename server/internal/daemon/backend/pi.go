package backend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// errFakeExit is a test-only sentinel error returned by fakeRunner to
// simulate a non-zero process exit. It is never produced by real code.
var errFakeExit = errors.New("process exited with non-zero status")

// commandRunner abstracts subprocess execution so that PiBackend.Execute
// can be tested without spawning a real "pi" process.
type commandRunner interface {
	runCommand(ctx context.Context, path string, args []string, dir string, env []string) (stdout, stderr string, err error)
}

// piRunner is the production commandRunner that shells out via
// exec.CommandContext.
type piRunner struct{}

func (piRunner) runCommand(ctx context.Context, path string, args []string, dir string, env []string) (string, string, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = env
	}
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	return stdoutBuf.String(), stderrBuf.String(), err
}

// PiBackend implements Backend for the Pi CLI in print mode (Mode A).
//
// In print mode the Pi CLI is invoked as:
//
//	pi -p --model <m> [--thinking <level>] --no-session --approve [--tools <csv>] <prompt>
//
// Stdout is captured verbatim as the result text; stderr is used for
// error reporting when the process exits non-zero.
type PiBackend struct {
	// Config holds Pi-specific options (thinking level, mode, etc.).
	Config PiConfig

	// binaryPath overrides the pi binary name. Empty defaults to "pi".
	binaryPath string

	// runner abstracts subprocess execution; nil defaults to piRunner.
	// Used by the print-mode Execute path.
	runner commandRunner

	// rpcProcessFactory, when non-nil, produces the piRPCProcess used by
	// ExecuteRPC instead of the default realRPCProcess. Test-only.
	rpcProcessFactory func(ctx context.Context, args []string, dir string, env []string) piRPCProcess
}

// Execute runs the Pi CLI in print mode and returns the captured output.
func (p *PiBackend) Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	binaryName := p.binaryPath
	if binaryName == "" {
		binaryName = "pi"
	}

	// Verify the binary exists before attempting to run it.
	lookedUp, err := exec.LookPath(binaryName)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf(
			"pi executable not found at %q: %w — install Pi from https://github.com/canopyprotocol/pi and ensure it is on your PATH",
			binaryName, err,
		)
	}

	args := buildPiPrintArgs(req, p.Config)

	runner := p.runner
	if runner == nil {
		runner = piRunner{}
	}

	start := time.Now()
	stdout, stderr, runErr := runner.runCommand(ctx, lookedUp, args, req.WorkDir, req.Env)
	duration := time.Since(start)

	result := ExecuteResult{
		Output:     stdout,
		DurationMs: duration.Milliseconds(),
	}

	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return result, fmt.Errorf("pi execution timed out after %s", duration)
		}
		if ctx.Err() == context.Canceled {
			return result, fmt.Errorf("pi execution cancelled: %w", ctx.Err())
		}
		return result, fmt.Errorf("pi exited with error (stderr: %s): %w", strings.TrimSpace(stderr), runErr)
	}

	return result, nil
}

// buildPiPrintArgs constructs the argument list for a print-mode Pi invocation.
//
// Layout:
//
//	-p [--model <m>] [--thinking <level>] --no-session --approve [--tools <csv>] <prompt>
//
// The prompt is always the last positional argument.
func buildPiPrintArgs(req ExecuteRequest, cfg PiConfig) []string {
	args := []string{"-p"}

	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	if cfg.ThinkingLevel != "" {
		args = append(args, "--thinking", cfg.ThinkingLevel)
	}

	args = append(args, "--no-session", "--approve")

	if len(req.Tools) > 0 {
		args = append(args, "--tools", strings.Join(req.Tools, ","))
	}

	args = append(args, req.Prompt)
	return args
}
