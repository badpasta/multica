package agent

import (
	"encoding/json"
	"fmt"
)

// CreateRuntimeConfig is the runtime_config shape validated at agent
// create time. It mirrors the subset of the runtime_config JSON that
// the server needs to sanity-check before persisting — backend
// selection plus the pi-specific sub-fields that have a constrained
// vocabulary (mode, thinkingLevel) or are required (model).
//
// Fields not consumed by validation (e.g. extra provider keys) are
// silently ignored by json.Unmarshal, so the struct is safe to use
// on any runtime_config payload without losing unknown fields in the
// DB — the server still stores the original JSON verbatim.
type CreateRuntimeConfig struct {
	Backend string               `json:"backend"`
	Pi      *CreatePiRuntimeConfig `json:"pi,omitempty"`
}

// CreatePiRuntimeConfig holds the pi-specific fields validated at
// agent create time. Extensions, Skills, and Tools are pass-through
// arrays — validated as string arrays but with no value constraint.
type CreatePiRuntimeConfig struct {
	Mode          string   `json:"mode"`
	Model         string   `json:"model"`
	ThinkingLevel string   `json:"thinkingLevel"`
	MaxTurns      int      `json:"maxTurns"`
	Extensions    []string `json:"extensions"`
	Skills        []string `json:"skills"`
	Tools         []string `json:"tools"`
}

// knownBackends is the set of accepted backend values. Empty string
// is accepted as a synonym for "claude" (the default) so existing
// agents and CLI calls that omit backend continue to work.
var knownBackends = map[string]bool{
	"":        true,
	"claude":  true,
	"codex":   true,
	"pi":      true,
	"copilot": true,
	"gemini":  true,
	"kimi":    true,
	"kiro":    true,
	"cursor":  true,
	"opencode": true,
	"openclaw": true,
	"hermes":   true,
	"antigravity": true,
}

// piValidModes restricts the pi.mode vocabulary to the two modes the
// Pi CLI supports for non-interactive agent use.
var piValidModes = map[string]bool{
	"print": true,
	"rpc":   true,
}

// piValidThinkingLevels is the accept-list for pi.thinkingLevel.
// Empty is always valid (means "use the runtime default"). The set
// mirrors the common effort vocabulary shared across Pi provider
// backends; values outside this set are rejected at create time so
// a typo can't persist into the DB and surface as a cryptic
// daemon-side task error later.
var piValidThinkingLevels = map[string]bool{
	"":      true,
	"none":  true,
	"low":   true,
	"medium": true,
	"high":  true,
	"xhigh": true,
}

// ValidateRuntimeConfig parses and validates a runtime_config JSON
// payload for agent creation. On success it returns the parsed
// config with the Backend field normalised (empty → "claude"). On
// failure it returns a descriptive error suitable for surfacing to
// the caller.
//
// Validation rules:
//   - Empty or missing backend defaults to "claude" (backward compat).
//   - Unknown backend values are rejected.
//   - When backend is "pi":
//   - pi.mode must be "print" or "rpc".
//   - pi.model is required.
//   - pi.thinkingLevel, when non-empty, must be a recognised value.
//   - pi.extensions, pi.skills, pi.tools are pass-through string arrays.
func ValidateRuntimeConfig(raw []byte) (CreateRuntimeConfig, error) {
	var cfg CreateRuntimeConfig
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return CreateRuntimeConfig{}, fmt.Errorf("invalid runtime_config JSON: %w", err)
		}
	}

	// Default backend is claude.
	if cfg.Backend == "" {
		cfg.Backend = "claude"
	}

	if !knownBackends[cfg.Backend] {
		return CreateRuntimeConfig{}, fmt.Errorf("unknown backend %q", cfg.Backend)
	}

	if cfg.Backend == "pi" {
		if err := validatePiRuntimeConfig(cfg.Pi); err != nil {
			return CreateRuntimeConfig{}, err
		}
	}

	return cfg, nil
}

func validatePiRuntimeConfig(pi *CreatePiRuntimeConfig) error {
	if pi == nil {
		return fmt.Errorf("pi backend requires a pi config block")
	}
	if !piValidModes[pi.Mode] {
		return fmt.Errorf("pi mode must be \"print\" or \"rpc\", got %q", pi.Mode)
	}
	if pi.Model == "" {
		return fmt.Errorf("pi backend requires a model")
	}
	if pi.ThinkingLevel != "" && !piValidThinkingLevels[pi.ThinkingLevel] {
		return fmt.Errorf("pi thinkingLevel %q is not a recognised value", pi.ThinkingLevel)
	}
	return nil
}
