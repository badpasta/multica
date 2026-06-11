// Package backend defines the Backend interface and supporting types for
// AI agent execution backends (Claude Code, Codex, Pi, etc.).
package backend

import "context"

// Backend is the interface that all AI agent backends must implement.
type Backend interface {
	Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error)
}

// ExecuteRequest holds all parameters needed to execute an agent run.
type ExecuteRequest struct {
	Prompt    string
	WorkDir   string
	Env       []string
	AgentName string
	Model     string
	MaxTurns  int
	Tools     []string
}

// ExecuteResult holds the output of an agent run.
type ExecuteResult struct {
	Output     string
	ToolCalls  []ToolCall
	ExitCode   int
	DurationMs int64
}

// ToolCall represents a single tool invocation during an agent run.
type ToolCall struct {
	Name   string
	Input  string
	Output string
}
