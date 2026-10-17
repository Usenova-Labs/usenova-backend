package agent

import (
	"testing"

	"github.com/google/uuid"
)

func TestRegisterAgent_GeneratesUUIDWhenMissing(t *testing.T) {
	s := NewAgentStore()

	a, err := s.RegisterAgent("", "host-1", "1.0.0", AgentCapabilities{Docker: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := uuid.Parse(a.AgentID); err != nil {
		t.Errorf("expected a valid UUID agent ID, got %q: %v", a.AgentID, err)
	}
}

func TestRegisterAgent_UsesProvidedID(t *testing.T) {
	s := NewAgentStore()

	a, err := s.RegisterAgent("my-agent", "host-1", "1.0.0", AgentCapabilities{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.AgentID != "my-agent" {
		t.Errorf("expected provided ID to be preserved, got %q", a.AgentID)
	}
}

func TestRegisterAgent_SessionTokenIsRandomAndOpaque(t *testing.T) {
	s := NewAgentStore()

	first, err := s.RegisterAgent("agent-a", "host-1", "1.0.0", AgentCapabilities{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := s.RegisterAgent("agent-b", "host-2", "1.0.0", AgentCapabilities{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if first.SessionToken == "" {
		t.Fatal("expected a session token to be set")
	}
	if first.SessionToken == "token-"+first.AgentID {
		t.Error("session token must not be a predictable derivation of the agent ID")
	}
	if first.SessionToken == second.SessionToken {
		t.Error("session tokens must be unique per registration")
	}
	if len(first.SessionToken) != 64 { // 32 random bytes hex-encoded
		t.Errorf("expected 64-char hex token, got %d chars", len(first.SessionToken))
	}
}
