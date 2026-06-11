package agent

import (
	"strings"
	"testing"
)

// TestAgentCreate_PiBackend verifies that a runtime-config with
// `backend: "pi"` is correctly parsed and validated. All required pi
// fields (mode, model) must be present and the parsed struct must
// round-trip the original values.
func TestAgentCreate_PiBackend(t *testing.T) {
	raw := `{
		"backend": "pi",
		"pi": {
			"mode": "print",
			"model": "gpt-4o",
			"thinkingLevel": "high",
			"maxTurns": 20,
			"extensions": ["code-interpreter"],
			"skills": ["planning"],
			"tools": ["web-search"]
		}
	}`
	cfg, err := ValidateRuntimeConfig([]byte(raw))
	if err != nil {
		t.Fatalf("ValidateRuntimeConfig: unexpected error: %v", err)
	}
	if cfg.Backend != "pi" {
		t.Fatalf("Backend = %q, want %q", cfg.Backend, "pi")
	}
	if cfg.Pi == nil {
		t.Fatal("Pi config is nil, want non-nil")
	}
	if cfg.Pi.Mode != "print" {
		t.Fatalf("Pi.Mode = %q, want %q", cfg.Pi.Mode, "print")
	}
	if cfg.Pi.Model != "gpt-4o" {
		t.Fatalf("Pi.Model = %q, want %q", cfg.Pi.Model, "gpt-4o")
	}
	if cfg.Pi.ThinkingLevel != "high" {
		t.Fatalf("Pi.ThinkingLevel = %q, want %q", cfg.Pi.ThinkingLevel, "high")
	}
	if cfg.Pi.MaxTurns != 20 {
		t.Fatalf("Pi.MaxTurns = %d, want 20", cfg.Pi.MaxTurns)
	}
}

// TestAgentCreate_PiBackend_InvalidMode verifies that a pi.mode value
// other than "print" or "rpc" returns a clear validation error.
func TestAgentCreate_PiBackend_InvalidMode(t *testing.T) {
	raw := `{
		"backend": "pi",
		"pi": {
			"mode": "agent",
			"model": "gpt-4o"
		}
	}`
	_, err := ValidateRuntimeConfig([]byte(raw))
	if err == nil {
		t.Fatal("expected validation error for invalid pi.mode, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "mode") {
		t.Fatalf("error should mention 'mode', got: %v", err)
	}
}

// TestAgentCreate_PiBackend_MissingModel verifies that pi backend
// without a model specified returns a validation error.
func TestAgentCreate_PiBackend_MissingModel(t *testing.T) {
	raw := `{
		"backend": "pi",
		"pi": {
			"mode": "print"
		}
	}`
	_, err := ValidateRuntimeConfig([]byte(raw))
	if err == nil {
		t.Fatal("expected validation error for missing pi.model, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "model") {
		t.Fatalf("error should mention 'model', got: %v", err)
	}
}

// TestAgentCreate_PiBackend_Extensions verifies that pi.extensions
// and pi.skills arrays are correctly parsed and round-tripped.
func TestAgentCreate_PiBackend_Extensions(t *testing.T) {
	raw := `{
		"backend": "pi",
		"pi": {
			"mode": "rpc",
			"model": "gpt-4o",
			"extensions": ["code-interpreter", "web-search"],
			"skills": ["planning", "coding"],
			"tools": ["file-editor"]
		}
	}`
	cfg, err := ValidateRuntimeConfig([]byte(raw))
	if err != nil {
		t.Fatalf("ValidateRuntimeConfig: unexpected error: %v", err)
	}
	if cfg.Pi == nil {
		t.Fatal("Pi config is nil, want non-nil")
	}
	if len(cfg.Pi.Extensions) != 2 || cfg.Pi.Extensions[0] != "code-interpreter" || cfg.Pi.Extensions[1] != "web-search" {
		t.Fatalf("Pi.Extensions = %v, want [code-interpreter web-search]", cfg.Pi.Extensions)
	}
	if len(cfg.Pi.Skills) != 2 || cfg.Pi.Skills[0] != "planning" || cfg.Pi.Skills[1] != "coding" {
		t.Fatalf("Pi.Skills = %v, want [planning coding]", cfg.Pi.Skills)
	}
	if len(cfg.Pi.Tools) != 1 || cfg.Pi.Tools[0] != "file-editor" {
		t.Fatalf("Pi.Tools = %v, want [file-editor]", cfg.Pi.Tools)
	}
}

// TestAgentCreate_DefaultBackend verifies backward compatibility:
// when no backend is specified (empty or missing), the default is
// "claude" and validation succeeds without pi-specific checks.
func TestAgentCreate_DefaultBackend(t *testing.T) {
	// Missing backend field entirely.
	raw := `{"some_other_key": "value"}`
	cfg, err := ValidateRuntimeConfig([]byte(raw))
	if err != nil {
		t.Fatalf("ValidateRuntimeConfig: unexpected error: %v", err)
	}
	if cfg.Backend != "claude" {
		t.Fatalf("Backend = %q, want %q (default)", cfg.Backend, "claude")
	}

	// Explicit empty string also defaults to claude.
	raw = `{"backend": ""}`
	cfg, err = ValidateRuntimeConfig([]byte(raw))
	if err != nil {
		t.Fatalf("ValidateRuntimeConfig: unexpected error: %v", err)
	}
	if cfg.Backend != "claude" {
		t.Fatalf("Backend = %q, want %q (default)", cfg.Backend, "claude")
	}

	// Explicit "claude" backend.
	raw = `{"backend": "claude"}`
	cfg, err = ValidateRuntimeConfig([]byte(raw))
	if err != nil {
		t.Fatalf("ValidateRuntimeConfig: unexpected error: %v", err)
	}
	if cfg.Backend != "claude" {
		t.Fatalf("Backend = %q, want %q", cfg.Backend, "claude")
	}
}
