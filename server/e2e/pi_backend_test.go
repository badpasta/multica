// Package e2e holds integration tests for the Multica daemon's pi backend
// that exercise the full backend chain: factory creation → subprocess spawn
// → stdout parsing → ExecuteResult. These tests use a fake `pi` binary
// (server/e2e/testdata/fakepi) compiled once per `go test` invocation and
// located via PATH, so they exercise real subprocess lifecycle — process
// spawning, stdin/stdout pipes, exit codes, signal delivery — rather than
// the in-memory fakes used by the unit tests in backend/.
//
// When a real Pi CLI is installed on the test host (detected via
// `exec.LookPath("pi")`), an extra suite of live tests runs against it.
// When it isn't, those live tests skip (not fail) so CI stays green in
// environments that don't ship Pi.
package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/backend"
)

// fakePiOnce guards the one-time compilation of the fake pi binary.
// Compile once, share across all tests — go test caches testdata artifacts
// fine, and building once keeps the suite fast.
var (
	fakePiOnce sync.Once
	fakePiPath string
	fakePiErr  error
)

// buildFakePi compiles server/e2e/testdata/fakepi into a package-scoped
// temp directory and returns the absolute path to the resulting binary.
// The binary is cached across tests within a single `go test` run.
//
// We deliberately use os.MkdirTemp rather than t.TempDir() because the
// compiled binary must outlive the test that first triggered the build —
// t.TempDir() is scoped to a single test and would be cleaned up as
// soon as that test returns, leaving subsequent tests with a dangling
// path. The process-wide directory is cleaned up on process exit.
func buildFakePi(t *testing.T) string {
	t.Helper()
	fakePiOnce.Do(func() {
		src := filepath.Join("testdata", "fakepi", "main.go")
		var dir string
		dir, fakePiErr = os.MkdirTemp("", "multica-e2e-fakepi-")
		if fakePiErr != nil {
			return
		}
		out := filepath.Join(dir, "pi")
		cmd := exec.Command("go", "build", "-o", out, src)
		cmd.Stderr = os.Stderr
		fakePiErr = cmd.Run()
		if fakePiErr == nil {
			fakePiPath = out
		}
	})
	if fakePiErr != nil {
		t.Fatalf("build fake pi: %v", fakePiErr)
	}
	return fakePiPath
}

// installFakePiInPath inserts dir at the front of the test process's PATH
// so exec.LookPath("pi") inside PiBackend.Execute finds the fake binary.
//
// LookPath inspects the calling process's environment, not the child's,
// so merely passing an augmented Env in ExecuteRequest is not enough —
// we must modify the test process's own PATH. t.Setenv restores the
// original value when the test (and its subtests) complete.
func installFakePiInPath(t *testing.T, dir string) {
	t.Helper()
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+origPath)
}

// childEnv builds the Env to pass to ExecuteRequest for tests that need
// to inject extra variables (like MULTICA_FAKE_PI_MODE). It copies the
// current process environment and appends the extras.
//
// When no extras are needed, pass `Env: nil` to ExecuteRequest and let
// the child inherit the parent environment directly — that's simpler
// and matches what the production daemon does for most tasks.
func childEnv(t *testing.T, extras ...string) []string {
	t.Helper()
	out := make([]string, 0, len(os.Environ())+len(extras))
	out = append(out, os.Environ()...)
	out = append(out, extras...)
	return out
}

// skipIfNoPi skips the calling test when the real Pi CLI is not installed.
// Used only by the live-* tests that exercise an unmodified `pi` binary.
func skipIfNoPi(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("pi"); err != nil {
		t.Skipf("pi CLI not installed on PATH: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Factory → execute chain
// ---------------------------------------------------------------------------

// TestE2E_PiAgent_CreateAndExecute is the happy-path chain test:
//   1. Build the backend via backend.NewBackend (the factory the daemon uses).
//   2. Execute a request through it against a real subprocess.
//   3. Assert the captured stdout reaches the caller as ExecuteResult.Output.
//
// This is the thinnest possible "issue → agent → daemon → pi → result"
// slice we can run without spinning up a full server+daemon stack.
func TestE2E_PiAgent_CreateAndExecute(t *testing.T) {
	pi := buildFakePi(t)
	installFakePiInPath(t, filepath.Dir(pi))

	cfg := backend.RuntimeConfig{
		Backend: "pi",
		Pi: &backend.PiConfig{
			Mode:          "print",
			ThinkingLevel: "high",
		},
	}
	b, err := backend.NewBackend(cfg)
	if err != nil {
		t.Fatalf("NewBackend(pi): %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := b.Execute(ctx, backend.ExecuteRequest{
		Prompt:    "What is 2+2?",
		WorkDir:   t.TempDir(),
		AgentName: "test-pi-agent",
		Model:     "claude-sonnet-4-6",
		MaxTurns:  10,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result.Output, "Hello from pi!") {
		t.Errorf("Output = %q, want it to contain %q", result.Output, "Hello from pi!")
	}
	if result.DurationMs < 0 {
		t.Errorf("DurationMs = %d, want >= 0", result.DurationMs)
	}
}

// ---------------------------------------------------------------------------
// Print mode (Mode A)
// ---------------------------------------------------------------------------

// TestE2E_PiAgent_PrintMode verifies print mode end-to-end: the backend
// spawns the pi CLI with `-p ... <prompt>` and captures stdout verbatim
// as ExecuteResult.Output.
func TestE2E_PiAgent_PrintMode(t *testing.T) {
	pi := buildFakePi(t)
	installFakePiInPath(t, filepath.Dir(pi))

	pb := &backend.PiBackend{
		Config: backend.PiConfig{Mode: "print"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := pb.Execute(ctx, backend.ExecuteRequest{
		Prompt:    "summarise this file",
		WorkDir:   t.TempDir(),
		AgentName: "test-pi-agent",
		Model:     "claude-sonnet-4-6",
	})
	if err != nil {
		t.Fatalf("Execute (print): %v", err)
	}
	if !strings.Contains(result.Output, "Hello from pi!") {
		t.Errorf("Output = %q, want it to contain %q", result.Output, "Hello from pi!")
	}
}

// ---------------------------------------------------------------------------
// RPC mode (Mode B)
// ---------------------------------------------------------------------------

// TestE2E_PiAgent_RPCMode verifies RPC mode end-to-end: the backend spawns
// pi with `--mode rpc`, writes a JSON prompt to stdin, parses JSONL events
// from stdout, and returns the assembled text + collected tool calls.
func TestE2E_PiAgent_RPCMode(t *testing.T) {
	pi := buildFakePi(t)
	installFakePiInPath(t, filepath.Dir(pi))

	// Tell the fake pi to run in RPC mode and log to a file for diagnosis.
	logPath := filepath.Join(t.TempDir(), "fakepi.log")
	t.Setenv("MULTICA_FAKE_PI_MODE", "rpc")
	t.Setenv("MULTICA_FAKE_PI_LOG", logPath)

	pb := &backend.PiBackend{
		Config: backend.PiConfig{Mode: "rpc"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := pb.ExecuteRPC(ctx, backend.ExecuteRequest{
		Prompt:    "help me",
		WorkDir:   t.TempDir(),
		AgentName: "test-pi-agent",
		Model:     "claude-sonnet-4-6",
		Tools:     []string{"read", "write"},
		MaxTurns:  5,
	})

	// Dump the fake pi log only on failure so the happy path stays quiet.
	if t.Failed() || err != nil {
		if log, logErr := os.ReadFile(logPath); logErr == nil {
			t.Logf("fakepi log:\n%s", log)
		}
	}

	if err != nil {
		t.Fatalf("ExecuteRPC: %v", err)
	}

	// message_update deltas are concatenated to form Output.
	want := "I'll help you with that."
	if result.Output != want {
		t.Errorf("Output = %q, want %q", result.Output, want)
	}

	// tool_execution_start / tool_execution_end pairs yield one ToolCall.
	if len(result.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.Name != "read" {
		t.Errorf("ToolCall.Name = %q, want %q", tc.Name, "read")
	}
	if !strings.Contains(tc.Input, "test.go") {
		t.Errorf("ToolCall.Input = %q, want it to contain %q", tc.Input, "test.go")
	}
	if tc.Output != "file contents" {
		t.Errorf("ToolCall.Output = %q, want %q", tc.Output, "file contents")
	}
}

// ---------------------------------------------------------------------------
// Error handling
// ---------------------------------------------------------------------------

// TestE2E_PiAgent_ErrorHandling covers the four failure shapes the daemon
// has to survive: binary missing from PATH, child exiting non-zero, RPC
// protocol-level error events, and context cancellation.
func TestE2E_PiAgent_ErrorHandling(t *testing.T) {
	t.Run("binary_not_found", func(t *testing.T) {
		pb := &backend.PiBackend{}
		// Point PATH at an empty temp dir so LookPath("pi") fails.
		emptyDir := t.TempDir()
		t.Setenv("PATH", emptyDir)
		ctx := context.Background()
		_, err := pb.Execute(ctx, backend.ExecuteRequest{
			Prompt:    "test",
			AgentName: "test",
		})
		if err == nil {
			t.Fatal("expected error when pi binary not found")
		}
		if !strings.Contains(err.Error(), "pi executable not found") {
			t.Errorf("error should mention missing binary, got: %v", err)
		}
	})

	t.Run("non_zero_exit", func(t *testing.T) {
		pi := buildFakePi(t)
		installFakePiInPath(t, filepath.Dir(pi))

		pb := &backend.PiBackend{}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_, err := pb.Execute(ctx, backend.ExecuteRequest{
			Prompt:    "test",
			WorkDir:   t.TempDir(),
			Env:       childEnv(t, "MULTICA_FAKE_PI_MODE=fail"),
			AgentName: "test",
		})
		if err == nil {
			t.Fatal("expected error on non-zero exit")
		}
		if !strings.Contains(err.Error(), "simulated failure") {
			t.Errorf("error should contain stderr, got: %v", err)
		}
	})

	t.Run("rpc_error_event", func(t *testing.T) {
		pi := buildFakePi(t)
		installFakePiInPath(t, filepath.Dir(pi))

		pb := &backend.PiBackend{}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_, err := pb.ExecuteRPC(ctx, backend.ExecuteRequest{
			Prompt:    "test",
			WorkDir:   t.TempDir(),
			Env:       childEnv(t, "MULTICA_FAKE_PI_MODE=rpc_error"),
			AgentName: "test",
		})
		if err == nil {
			t.Fatal("expected error from RPC error event")
		}
		if !strings.Contains(err.Error(), "something went wrong in pi") {
			t.Errorf("error should contain RPC error message, got: %v", err)
		}
	})

	t.Run("context_cancelled", func(t *testing.T) {
		pi := buildFakePi(t)
		installFakePiInPath(t, filepath.Dir(pi))

		pb := &backend.PiBackend{}
		ctx, cancel := context.WithCancel(context.Background())

		done := make(chan struct{})
		var execErr error
		go func() {
			defer close(done)
			_, execErr = pb.Execute(ctx, backend.ExecuteRequest{
				Prompt:    "long running",
				WorkDir:   t.TempDir(),
				Env:       childEnv(t, "MULTICA_FAKE_PI_MODE=block"),
				AgentName: "test",
			})
		}()

		// Let the child start and settle into its sleep, then cancel.
		time.Sleep(200 * time.Millisecond)
		cancel()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Execute did not return within 5s of cancellation")
		}
		if execErr == nil {
			t.Fatal("expected error on cancellation")
		}
	})
}

// ---------------------------------------------------------------------------
// Timeout
// ---------------------------------------------------------------------------

// TestE2E_PiAgent_Timeout verifies that the daemon's per-run deadline
// actually terminates a hung pi process. The fake pi in "block" mode
// sleeps forever; the context deadline must abort it and surface a
// timeout-flavoured error.
func TestE2E_PiAgent_Timeout(t *testing.T) {
	pi := buildFakePi(t)
	installFakePiInPath(t, filepath.Dir(pi))

	pb := &backend.PiBackend{}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := pb.Execute(ctx, backend.ExecuteRequest{
		Prompt:    "test",
		WorkDir:   t.TempDir(),
		Env:       childEnv(t, "MULTICA_FAKE_PI_MODE=block"),
		AgentName: "test",
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	// The context deadline should fire around 200ms; the child gets killed
	// shortly after. If we're past 3s the timeout isn't being enforced.
	if elapsed > 3*time.Second {
		t.Errorf("timeout not enforced: elapsed=%s, want ~200ms", elapsed)
	}
	if !strings.Contains(err.Error(), "timed out") && !strings.Contains(err.Error(), "cancel") {
		t.Errorf("error should mention timeout or cancellation, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Live tests — run only when the real Pi CLI is installed.
// ---------------------------------------------------------------------------

// TestE2E_PiAgent_Live_Print exercises the real `pi` binary in print mode
// with a minimal prompt. Skipped unless pi is on PATH; see skipIfNoPi.
func TestE2E_PiAgent_Live_Print(t *testing.T) {
	skipIfNoPi(t)

	pb := &backend.PiBackend{
		Config: backend.PiConfig{Mode: "print"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := pb.Execute(ctx, backend.ExecuteRequest{
		Prompt:    "say the word 'pong' and nothing else",
		WorkDir:   t.TempDir(),
		AgentName: "live-pi-test",
		Model:     "claude-sonnet-4-6",
	})
	if err != nil {
		t.Fatalf("Execute (live print): %v", err)
	}
	if result.Output == "" {
		t.Error("live print mode produced empty output")
	}
	t.Logf("live print output: %s", result.Output)
}

// TestE2E_PiAgent_Live_RPC exercises the real `pi` binary in RPC mode.
// Skipped unless pi is on PATH; see skipIfNoPi.
func TestE2E_PiAgent_Live_RPC(t *testing.T) {
	skipIfNoPi(t)

	pb := &backend.PiBackend{
		Config: backend.PiConfig{Mode: "rpc"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := pb.ExecuteRPC(ctx, backend.ExecuteRequest{
		Prompt:    "say the word 'pong' and nothing else",
		WorkDir:   t.TempDir(),
		AgentName: "live-pi-test",
		Model:     "claude-sonnet-4-6",
		MaxTurns:  1,
	})
	if err != nil {
		t.Fatalf("ExecuteRPC (live): %v", err)
	}
	if result.Output == "" {
		t.Error("live RPC mode produced empty output")
	}
	t.Logf("live RPC output: %s", result.Output)
}
