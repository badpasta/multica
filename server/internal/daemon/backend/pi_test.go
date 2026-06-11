package backend

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fakeRunner implements commandRunner for tests. It records invocation
// details and returns predetermined stdout/stderr/err without spawning a
// real subprocess. When Delay > 0 it blocks until the delay elapses or
// the context is cancelled, simulating a long-running process.
type fakeRunner struct {
	Stdout   string
	Stderr   string
	Err      error
	Delay    time.Duration
	Args     []string
	Dir      string
	Env      []string
	StartErr error
}

func (f *fakeRunner) runCommand(ctx context.Context, path string, args []string, dir string, env []string) (stdout, stderr string, err error) {
	if f.StartErr != nil {
		return "", "", f.StartErr
	}
	f.Args = args
	f.Dir = dir
	f.Env = env

	if f.Delay > 0 {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(f.Delay):
		}
	} else {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		default:
		}
	}

	return f.Stdout, f.Stderr, f.Err
}

func TestPiBackend_Execute_PrintMode(t *testing.T) {
	runner := &fakeRunner{Stdout: "hello from pi\n"}
	b := &PiBackend{binaryPath: "echo", runner: runner}

	ctx := context.Background()
	req := ExecuteRequest{
		Prompt:    "do something",
		WorkDir:   "/tmp",
		AgentName: "test-agent",
	}

	result, err := b.Execute(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "hello from pi\n" {
		t.Errorf("Output = %q, want %q", result.Output, "hello from pi\n")
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.DurationMs < 0 {
		t.Errorf("DurationMs = %d, should be non-negative", result.DurationMs)
	}
}

func TestPiBackend_Args(t *testing.T) {
	runner := &fakeRunner{Stdout: "ok"}
	b := &PiBackend{
		binaryPath: "echo",
		runner:     runner,
		Config: PiConfig{
			Mode:          "print",
			ThinkingLevel: "high",
		},
	}

	ctx := context.Background()
	req := ExecuteRequest{
		Prompt:    "my task description",
		WorkDir:   "/home/user/project",
		AgentName: "test-agent",
		Model:     "claude-sonnet-4-6",
		Tools:     []string{"Bash", "Read", "Write"},
	}

	result, err := b.Execute(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "ok" {
		t.Errorf("Output = %q, want %q", result.Output, "ok")
	}

	args := runner.Args
	argsStr := strings.Join(args, " ")

	// -p flag with prompt as the last positional argument
	if !containsArg(args, "-p") {
		t.Error("args should contain -p flag")
	}
	if args[len(args)-1] != "my task description" {
		t.Errorf("last arg should be the prompt, got %q (args: %v)", args[len(args)-1], args)
	}

	// --model from ExecuteRequest.Model
	if !containsArgWithValue(args, "--model", "claude-sonnet-4-6") {
		t.Errorf("args should contain --model claude-sonnet-4-6, got: %s", argsStr)
	}

	// --thinking from PiConfig.ThinkingLevel
	if !containsArgWithValue(args, "--thinking", "high") {
		t.Errorf("args should contain --thinking high, got: %s", argsStr)
	}

	// --no-session always present
	if !containsArg(args, "--no-session") {
		t.Errorf("args should contain --no-session, got: %s", argsStr)
	}

	// --approve always present
	if !containsArg(args, "--approve") {
		t.Errorf("args should contain --approve, got: %s", argsStr)
	}

	// --tools from ExecuteRequest.Tools (comma-separated)
	if !containsArgWithValue(args, "--tools", "Bash,Read,Write") {
		t.Errorf("args should contain --tools Bash,Read,Write, got: %s", argsStr)
	}

	// Working directory passed through
	if runner.Dir != "/home/user/project" {
		t.Errorf("Dir = %q, want %q", runner.Dir, "/home/user/project")
	}
}

func TestPiBackend_Execute_PiNotFound(t *testing.T) {
	b := &PiBackend{
		binaryPath: "definitely-not-a-real-binary-xyz123",
		// runner intentionally nil — Execute should LookPath before running
	}

	ctx := context.Background()
	req := ExecuteRequest{
		Prompt:    "test",
		AgentName: "test-agent",
	}

	_, err := b.Execute(ctx, req)
	if err == nil {
		t.Fatal("expected error when pi binary not found, got nil")
	}
	if !strings.Contains(err.Error(), "pi executable not found") {
		t.Errorf("error should mention 'pi executable not found', got: %v", err)
	}
	// Error should include installation guidance.
	if !strings.Contains(err.Error(), "install") && !strings.Contains(err.Error(), "Install") {
		t.Errorf("error should include installation guidance, got: %v", err)
	}
}

func TestPiBackend_Execute_Timeout(t *testing.T) {
	runner := &fakeRunner{
		Stdout: "",
		Delay:  5 * time.Second,
	}
	b := &PiBackend{binaryPath: "echo", runner: runner}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	req := ExecuteRequest{
		Prompt:    "test",
		AgentName: "test-agent",
	}

	_, err := b.Execute(ctx, req)
	if err == nil {
		t.Fatal("expected error on timeout, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error should mention timeout, got: %v", err)
	}
}

func TestPiBackend_Execute_NonZeroExit(t *testing.T) {
	runner := &fakeRunner{
		Stderr: "something went wrong",
		Err:    errFakeExit,
	}
	b := &PiBackend{binaryPath: "echo", runner: runner}

	ctx := context.Background()
	req := ExecuteRequest{
		Prompt:    "test",
		AgentName: "test-agent",
	}

	_, err := b.Execute(ctx, req)
	if err == nil {
		t.Fatal("expected error on non-zero exit, got nil")
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("error should contain stderr output, got: %v", err)
	}
}

func TestPiBackend_Execute_EnvPassthrough(t *testing.T) {
	runner := &fakeRunner{Stdout: "ok"}
	b := &PiBackend{binaryPath: "echo", runner: runner}

	ctx := context.Background()
	req := ExecuteRequest{
		Prompt:    "test",
		WorkDir:   "/tmp",
		Env:       []string{"FOO=bar", "BAZ=qux"},
		AgentName: "test-agent",
	}

	_, err := b.Execute(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(runner.Env) != 2 {
		t.Fatalf("expected 2 env vars, got %d: %v", len(runner.Env), runner.Env)
	}
	found := 0
	for _, e := range runner.Env {
		if e == "FOO=bar" || e == "BAZ=qux" {
			found++
		}
	}
	if found != 2 {
		t.Errorf("expected FOO=bar and BAZ=qux in env, got: %v", runner.Env)
	}
}

func TestPiBackend_Execute_NoTools(t *testing.T) {
	runner := &fakeRunner{Stdout: "ok"}
	b := &PiBackend{binaryPath: "echo", runner: runner}

	ctx := context.Background()
	req := ExecuteRequest{
		Prompt:    "test",
		AgentName: "test-agent",
	}

	_, err := b.Execute(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if containsArg(runner.Args, "--tools") {
		t.Errorf("args should not contain --tools when no tools specified, got: %v", runner.Args)
	}
}

func TestPiBackend_Execute_NoThinking(t *testing.T) {
	runner := &fakeRunner{Stdout: "ok"}
	b := &PiBackend{
		binaryPath: "echo",
		runner:     runner,
		Config: PiConfig{
			Mode: "print",
			// ThinkingLevel intentionally empty
		},
	}

	ctx := context.Background()
	req := ExecuteRequest{
		Prompt:    "test",
		AgentName: "test-agent",
	}

	_, err := b.Execute(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if containsArg(runner.Args, "--thinking") {
		t.Errorf("args should not contain --thinking when ThinkingLevel is empty, got: %v", runner.Args)
	}
}

func TestPiBackend_DefaultBinaryPath(t *testing.T) {
	// When binaryPath is empty, PiBackend defaults to "pi".
	// With no runner override and pi not installed, LookPath fails.
	b := &PiBackend{}

	ctx := context.Background()
	req := ExecuteRequest{
		Prompt:    "test",
		AgentName: "test-agent",
	}

	_, err := b.Execute(ctx, req)
	// Either "not found" (pi not installed) or an error from the default runner.
	if err == nil {
		t.Fatal("expected error from zero-value PiBackend, got nil")
	}
}

func TestPiBackend_Execute_Duration(t *testing.T) {
	runner := &fakeRunner{
		Stdout: "ok",
		Delay:  50 * time.Millisecond,
	}
	b := &PiBackend{binaryPath: "echo", runner: runner}

	ctx := context.Background()
	req := ExecuteRequest{
		Prompt:    "test",
		AgentName: "test-agent",
	}

	result, err := b.Execute(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DurationMs < 40 {
		t.Errorf("DurationMs = %d, expected at least ~50ms", result.DurationMs)
	}
}

// containsArg checks if args contains the exact flag.
func containsArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// containsArgWithValue checks if args contains --flag value as consecutive elements.
func containsArgWithValue(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}
