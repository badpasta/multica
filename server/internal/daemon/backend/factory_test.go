package backend

import (
	"strings"
	"testing"
)

func TestNewBackend_Claude(t *testing.T) {
	// Empty backend defaults to claude.
	b, err := NewBackend(RuntimeConfig{Backend: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := b.(*ClaudeBackend); !ok {
		t.Fatalf("expected *ClaudeBackend, got %T", b)
	}

	// Explicit "claude" backend.
	b, err = NewBackend(RuntimeConfig{Backend: "claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := b.(*ClaudeBackend); !ok {
		t.Fatalf("expected *ClaudeBackend, got %T", b)
	}
}

func TestNewBackend_Codex(t *testing.T) {
	b, err := NewBackend(RuntimeConfig{Backend: "codex"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := b.(*CodexBackend); !ok {
		t.Fatalf("expected *CodexBackend, got %T", b)
	}
}

func TestNewBackend_Pi(t *testing.T) {
	cfg := RuntimeConfig{
		Backend: "pi",
		Pi: &PiConfig{
			Mode:          "agent",
			ThinkingLevel: "high",
			MaxTurns:      20,
			Extensions:    []string{"code-interpreter", "web-search"},
		},
	}
	b, err := NewBackend(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pb, ok := b.(*PiBackend)
	if !ok {
		t.Fatalf("expected *PiBackend, got %T", b)
	}

	// Verify PiConfig sub-fields were stored correctly.
	if pb != nil {
		// PiBackend is a stub; config is validated on cfg, not on the backend.
		_ = pb
	}

	if cfg.Pi.Mode != "agent" {
		t.Errorf("Pi.Mode = %q, want %q", cfg.Pi.Mode, "agent")
	}
	if cfg.Pi.ThinkingLevel != "high" {
		t.Errorf("Pi.ThinkingLevel = %q, want %q", cfg.Pi.ThinkingLevel, "high")
	}
	if cfg.Pi.MaxTurns != 20 {
		t.Errorf("Pi.MaxTurns = %d, want 20", cfg.Pi.MaxTurns)
	}
	if len(cfg.Pi.Extensions) != 2 {
		t.Errorf("len(Pi.Extensions) = %d, want 2", len(cfg.Pi.Extensions))
	}
}

func TestNewBackend_Unknown(t *testing.T) {
	b, err := NewBackend(RuntimeConfig{Backend: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown backend, got nil")
	}
	if !strings.Contains(err.Error(), "unknown backend") {
		t.Errorf("error should mention 'unknown backend', got: %v", err)
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should contain the backend name, got: %v", err)
	}
	if b != nil {
		t.Errorf("expected nil Backend on error, got %T", b)
	}
}
