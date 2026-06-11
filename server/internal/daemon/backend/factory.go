package backend

import (
	"context"
	"fmt"
)

// PiConfig holds Pi-specific backend configuration.
type PiConfig struct {
	Mode          string   `json:"mode"`
	ThinkingLevel string   `json:"thinkingLevel"`
	MaxTurns      int      `json:"maxTurns"`
	Extensions    []string `json:"extensions"`
}

// RuntimeConfig holds the runtime-level backend configuration.
type RuntimeConfig struct {
	Backend string    `json:"backend"`
	Pi      *PiConfig `json:"pi,omitempty"`
}

// NewBackend creates a Backend based on the provided RuntimeConfig.
// Empty or "claude" backend returns a ClaudeBackend.
func NewBackend(cfg RuntimeConfig) (Backend, error) {
	switch cfg.Backend {
	case "", "claude":
		return &ClaudeBackend{}, nil
	case "codex":
		return &CodexBackend{}, nil
	case "pi":
		piCfg := PiConfig{}
		if cfg.Pi != nil {
			piCfg = *cfg.Pi
		}
		return &PiBackend{Config: piCfg}, nil
	default:
		return nil, fmt.Errorf("unknown backend: %q", cfg.Backend)
	}
}

// ClaudeBackend implements Backend for Claude Code.
type ClaudeBackend struct{}

func (c *ClaudeBackend) Execute(_ context.Context, _ ExecuteRequest) (ExecuteResult, error) {
	return ExecuteResult{}, fmt.Errorf("not implemented")
}

// CodexBackend implements Backend for Codex.
type CodexBackend struct{}

func (c *CodexBackend) Execute(_ context.Context, _ ExecuteRequest) (ExecuteResult, error) {
	return ExecuteResult{}, fmt.Errorf("not implemented")
}
