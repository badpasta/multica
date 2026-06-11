//go:build unix

package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTestExecutable writes content to path with exec perms.
func writeTestExecutable(tb testing.TB, path string, content []byte) {
	tb.Helper()
	if err := os.WriteFile(path, content, 0o755); err != nil {
		tb.Fatalf("write test executable %s: %v", path, err)
	}
}

// TestClaudeBackend_Execute_Success verifies that stdout from the claude
// subprocess is captured as ExecuteResult.Output and exit code is zero.
func TestClaudeBackend_Execute_Success(t *testing.T) {
	dir := t.TempDir()
	fakePath := filepath.Join(dir, "claude")
	// Fake claude: drain stdin, print output, exit 0.
	script := "#!/bin/sh\n" +
		"cat >/dev/null\n" +
		"echo 'hello from claude'\n" +
		"exit 0\n"
	writeTestExecutable(t, fakePath, []byte(script))

	b := &ClaudeBackend{ExecutablePath: fakePath}
	ctx := context.Background()
	req := ExecuteRequest{
		Prompt:  "test prompt",
		WorkDir: dir,
	}

	result, err := b.Execute(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "hello from claude") {
		t.Errorf("expected output to contain 'hello from claude', got %q", result.Output)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if result.DurationMs <= 0 {
		t.Errorf("expected positive duration, got %d", result.DurationMs)
	}
}

// TestClaudeBackend_Execute_Args verifies the command-line arguments passed
// to the claude CLI: -p, --model, --thinking, --max-turns.
func TestClaudeBackend_Execute_Args(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")

	fakePath := filepath.Join(dir, "claude")
	// Fake claude: record argv to a file, drain stdin, exit 0.
	// CLAUDE_ARGS_FILE is passed via the process env.
	script := `#!/bin/sh
printf '%s\n' "$@" > "$CLAUDE_ARGS_FILE"
cat >/dev/null
echo "ok"
exit 0
`
	writeTestExecutable(t, fakePath, []byte(script))

	b := &ClaudeBackend{ExecutablePath: fakePath}
	ctx := context.Background()
	req := ExecuteRequest{
		Prompt:        "test prompt",
		WorkDir:       dir,
		Model:         "claude-sonnet-4-6",
		MaxTurns:      10,
		ThinkingLevel: "high",
		Env:           []string{fmt.Sprintf("CLAUDE_ARGS_FILE=%s", argsFile)},
	}

	_, err := b.Execute(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")

	// Verify -p flag is present.
	foundPrint := false
	for _, a := range args {
		if a == "-p" {
			foundPrint = true
			break
		}
	}
	if !foundPrint {
		t.Errorf("expected -p flag in args, got %v", args)
	}

	// Verify --model flag with correct value.
	foundModel := false
	for i, a := range args {
		if a == "--model" && i+1 < len(args) && args[i+1] == "claude-sonnet-4-6" {
			foundModel = true
			break
		}
	}
	if !foundModel {
		t.Errorf("expected --model claude-sonnet-4-6 in args, got %v", args)
	}

	// Verify --thinking flag with correct value.
	foundThinking := false
	for i, a := range args {
		if a == "--thinking" && i+1 < len(args) && args[i+1] == "high" {
			foundThinking = true
			break
		}
	}
	if !foundThinking {
		t.Errorf("expected --thinking high in args, got %v", args)
	}

	// Verify --max-turns flag with correct value.
	foundMaxTurns := false
	for i, a := range args {
		if a == "--max-turns" && i+1 < len(args) && args[i+1] == "10" {
			foundMaxTurns = true
			break
		}
	}
	if !foundMaxTurns {
		t.Errorf("expected --max-turns 10 in args, got %v", args)
	}
}

// TestClaudeBackend_Execute_Error verifies that a non-zero exit from the
// claude process returns an error and stderr content is included in the
// error message.
func TestClaudeBackend_Execute_Error(t *testing.T) {
	dir := t.TempDir()
	fakePath := filepath.Join(dir, "claude")
	// Fake claude: write to stderr and exit non-zero.
	script := "#!/bin/sh\n" +
		"cat >/dev/null\n" +
		"echo 'FATAL ERROR: something went wrong' >&2\n" +
		"exit 1\n"
	writeTestExecutable(t, fakePath, []byte(script))

	b := &ClaudeBackend{ExecutablePath: fakePath}
	ctx := context.Background()
	req := ExecuteRequest{
		Prompt:  "test prompt",
		WorkDir: dir,
	}

	_, err := b.Execute(ctx, req)
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil")
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("expected error to contain stderr text, got: %v", err)
	}
}

// TestClaudeBackend_Execute_Timeout verifies that context cancellation
// kills the subprocess and returns a timeout error.
func TestClaudeBackend_Execute_Timeout(t *testing.T) {
	dir := t.TempDir()
	fakePath := filepath.Join(dir, "claude")
	// Fake claude: sleep forever (will be killed by timeout).
	// exec replaces the shell with sleep so the killed PID IS the sleep
	// process — no orphan children hold the pipes open.
	script := "#!/bin/sh\n" +
		"cat >/dev/null\n" +
		"exec sleep 60\n"
	writeTestExecutable(t, fakePath, []byte(script))

	b := &ClaudeBackend{ExecutablePath: fakePath}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	req := ExecuteRequest{
		Prompt:  "test prompt",
		WorkDir: dir,
	}

	start := time.Now()
	_, err := b.Execute(ctx, req)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "context") {
		t.Errorf("expected error to mention context, got: %v", err)
	}
	// Verify the process was killed promptly (within 5 seconds, not 60).
	if elapsed > 5*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}
