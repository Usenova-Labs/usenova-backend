package agent

import (
	"testing"

	"github.com/google/uuid"
)

func TestRegisterAgent_GeneratesUUIDWhenMissing(t *testing.T) {
	s := NewAgentStore()

	a, err := s.RegisterAgent(AgentRegistration{
		Hostname:     "host-1",
		Version:      "1.0.0",
		Capabilities: AgentCapabilities{Docker: true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := uuid.Parse(a.AgentID); err != nil {
		t.Errorf("expected a valid UUID agent ID, got %q: %v", a.AgentID, err)
	}
}

func TestRegisterAgent_UsesProvidedID(t *testing.T) {
	s := NewAgentStore()

	a, err := s.RegisterAgent(AgentRegistration{
		AgentID:      "my-agent",
		Hostname:     "host-1",
		Version:      "1.0.0",
		Capabilities: AgentCapabilities{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.AgentID != "my-agent" {
		t.Errorf("expected provided ID to be preserved, got %q", a.AgentID)
	}
}

func TestRegisterAgent_SessionTokenIsRandomAndOpaque(t *testing.T) {
	s := NewAgentStore()

	first, err := s.RegisterAgent(AgentRegistration{
		AgentID:  "agent-a",
		Hostname: "host-1",
		Version:  "1.0.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := s.RegisterAgent(AgentRegistration{
		AgentID:  "agent-b",
		Hostname: "host-2",
		Version:  "1.0.0",
	})
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

func TestRegisterAgent_StoresSystemInfo(t *testing.T) {
	s := NewAgentStore()

	system := AgentSystemInfo{
		OS:            "linux",
		Arch:          "amd64",
		CPUCount:      8,
		MemoryBytes:   16 * 1024 * 1024 * 1024,
		DockerVersion: "24.0.7",
	}

	a, err := s.RegisterAgent(AgentRegistration{
		AgentID:  "agent-a",
		Hostname: "host-1",
		Version:  "1.0.0",
		System:   system,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a.System != system {
		t.Errorf("expected system info %+v, got %+v", system, a.System)
	}

	stored, err := s.GetAgent("agent-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stored.System != system {
		t.Errorf("expected stored system info %+v, got %+v", system, stored.System)
	}
}

func TestRegisterAgent_WithoutSystemInfoIsAccepted(t *testing.T) {
	s := NewAgentStore()

	a, err := s.RegisterAgent(AgentRegistration{
		AgentID:  "legacy-agent",
		Hostname: "host-1",
		Version:  "1.0.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.System != (AgentSystemInfo{}) {
		t.Errorf("expected an empty system info, got %+v", a.System)
	}
}

func TestUpdateSystemInfo_RefreshesInventory(t *testing.T) {
	s := NewAgentStore()

	_, err := s.RegisterAgent(AgentRegistration{
		AgentID:  "agent-a",
		Hostname: "host-1",
		Version:  "1.0.0",
		System: AgentSystemInfo{
			OS:            "linux",
			Arch:          "amd64",
			CPUCount:      4,
			MemoryBytes:   8 * 1024 * 1024 * 1024,
			DockerVersion: "24.0.7",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := AgentSystemInfo{
		OS:            "linux",
		Arch:          "arm64",
		CPUCount:      16,
		MemoryBytes:   32 * 1024 * 1024 * 1024,
		DockerVersion: "25.0.1",
	}
	if err := s.UpdateSystemInfo("agent-a", updated); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stored, err := s.GetAgent("agent-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stored.System != updated {
		t.Errorf("expected refreshed system info %+v, got %+v", updated, stored.System)
	}
}

func TestUpdateSystemInfo_UnknownAgent(t *testing.T) {
	s := NewAgentStore()

	if err := s.UpdateSystemInfo("missing", AgentSystemInfo{OS: "linux"}); err != ErrAgentNotFound {
		t.Errorf("expected ErrAgentNotFound, got %v", err)
	}
}
