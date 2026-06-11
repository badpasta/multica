package backend

import (
	"context"
	"testing"
)

func TestBackendInterface(t *testing.T) {
	// Verify that all backends satisfy the Backend interface.
	var _ Backend = &ClaudeBackend{}
	var _ Backend = &CodexBackend{}
	var _ Backend = &PiBackend{}

	// All three backends (Claude, Codex, Pi) have real implementations
	// tested in their respective test files (claude_test.go, codex_test.go,
	// pi_test.go). No stubs remain.
	_ = context.Background()
}

func TestExecuteRequest(t *testing.T) {
	req := ExecuteRequest{
		Prompt:    "hello world",
		WorkDir:   "/home/user/project",
		Env:       []string{"PATH=/usr/bin", "HOME=/home/user"},
		AgentName: "claude",
		Model:     "claude-opus-4-7",
		MaxTurns:  25,
	}

	if req.Prompt != "hello world" {
		t.Errorf("Prompt = %q, want %q", req.Prompt, "hello world")
	}
	if req.WorkDir != "/home/user/project" {
		t.Errorf("WorkDir = %q, want %q", req.WorkDir, "/home/user/project")
	}
	if len(req.Env) != 2 {
		t.Errorf("len(Env) = %d, want 2", len(req.Env))
	}
	if req.AgentName != "claude" {
		t.Errorf("AgentName = %q, want %q", req.AgentName, "claude")
	}
	if req.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q, want %q", req.Model, "claude-opus-4-7")
	}
	if req.MaxTurns != 25 {
		t.Errorf("MaxTurns = %d, want 25", req.MaxTurns)
	}
}

func TestToolCall(t *testing.T) {
	tc := ToolCall{
		Name:   "Bash",
		Input:  `{"command": "ls -la"}`,
		Output: "total 42\n-rw-r--r-- 1 user user 1234 main.go",
	}

	if tc.Name != "Bash" {
		t.Errorf("Name = %q, want %q", tc.Name, "Bash")
	}
	if tc.Input != `{"command": "ls -la"}` {
		t.Errorf("Input = %q", tc.Input)
	}
	if tc.Output != "total 42\n-rw-r--r-- 1 user user 1234 main.go" {
		t.Errorf("Output = %q", tc.Output)
	}
}
