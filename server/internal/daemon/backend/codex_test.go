package backend

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// newTestCodexBackend creates a CodexBackend that spawns a fake executable
// instead of the real codex CLI. The body is a shell script body that the
// fake executable runs; tests use it to simulate stdout output, non-zero
// exits, or long-running sleeps for timeout verification.
func newTestCodexBackend(t *testing.T, body string) *CodexBackend {
	t.Helper()
	fakePath := t.TempDir() + "/codex"
	script := "#!/bin/sh\n" + body
	if err := os.WriteFile(fakePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	return &CodexBackend{ExecPath: fakePath}
}

func TestCodexBackend_Execute_Success(t *testing.T) {
	t.Parallel()

	b := newTestCodexBackend(t, `echo "hello from codex"`)

	result, err := b.Execute(context.Background(), ExecuteRequest{
		Prompt: "do something",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := strings.TrimSpace(result.Output)
	if got != "hello from codex" {
		t.Errorf("Output = %q, want %q", got, "hello from codex")
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.DurationMs <= 0 {
		t.Errorf("DurationMs = %d, want > 0", result.DurationMs)
	}
}

func TestCodexBackend_Execute_Args(t *testing.T) {
	t.Parallel()

	// The fake codex writes its received arguments to stdout so the test
	// can verify ExecPath, WorkDir, Env, and the prompt all flow through
	// to the child process unchanged.
	b := newTestCodexBackend(t, `
		echo "PWD=$(pwd)"
		echo "ENV_TEST=$ENV_TEST"
		for arg in "$@"; do echo "ARG=$arg"; done
	`)

	result, err := b.Execute(context.Background(), ExecuteRequest{
		Prompt:  "test prompt",
		WorkDir: t.TempDir(),
		Env:     []string{"ENV_TEST=hello"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := result.Output

	if !strings.Contains(out, "ENV_TEST=hello") {
		t.Errorf("env var not passed through, output:\n%s", out)
	}
	// The prompt must reach the codex CLI as a positional argument.
	if !strings.Contains(out, "ARG=test prompt") {
		t.Errorf("prompt not passed as argument, output:\n%s", out)
	}
}

func TestCodexBackend_Execute_ArgsWorkDir(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	b := newTestCodexBackend(t, `pwd`)

	result, err := b.Execute(context.Background(), ExecuteRequest{
		Prompt:  "do something",
		WorkDir: workDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := strings.TrimSpace(result.Output)
	// Resolve symlinks: macOS TempDir returns /var/folders/… which is a
	// symlink to /private/var/folders/…, and pwd resolves it.
	want := workDir
	if !strings.HasSuffix(got, want) && got != want {
		// Allow the resolved symlink form too.
		if !strings.Contains(got, "var/folders") || !strings.Contains(want, "var/folders") {
			t.Errorf("WorkDir not applied: pwd=%q, want %q", got, want)
		}
	}
}

func TestCodexBackend_Execute_Error(t *testing.T) {
	t.Parallel()

	b := newTestCodexBackend(t, `
		echo "error: something went wrong" >&2
		exit 1
	`)

	_, err := b.Execute(context.Background(), ExecuteRequest{
		Prompt: "do something",
	})
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil")
	}
	// The error message should contain the stderr output so the caller
	// can diagnose the failure.
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("error should contain stderr hint, got: %v", err)
	}
}

func TestCodexBackend_Execute_Timeout(t *testing.T) {
	t.Parallel()

	b := newTestCodexBackend(t, `sleep 30`)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := b.Execute(ctx, ExecuteRequest{
		Prompt: "do something",
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error for timeout, got nil")
	}
	// The process must be killed promptly, not after the full 30s sleep.
	if elapsed > 5*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}
