package backend

import (
	"fmt"
	"log/slog"
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

// ClaudeBackend implements Backend for Claude Code by spawning the claude
// CLI as a subprocess. ExecutablePath overrides the default "claude" lookup
// (useful for testing with a fake binary). Logger captures diagnostic output.
type ClaudeBackend struct {
	ExecutablePath string
	Logger         *slog.Logger
}

// CodexBackend implements Backend by spawning the Codex CLI as a
// subprocess. ExecPath overrides the default "codex" binary name for
// tests and non-standard installations. See codex.go for the Execute
// implementation.
type CodexBackend struct {
	ExecPath string
}
