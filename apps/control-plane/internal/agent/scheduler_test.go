package agent

import (
	"testing"
	"time"
)

func registerScheduledAgent(t *testing.T, store *AgentStore, id string, owner OwnerType, system *AgentSystemInfo) *Agent {
	t.Helper()

	reg := AgentRegistration{
		AgentID:      id,
		Hostname:     "host-" + id,
		Version:      "1.0.0",
		Capabilities: AgentCapabilities{Docker: true},
		OwnerType:    owner,
	}
	if system != nil {
		reg.System = *system
	}
	agent, err := store.RegisterAgent(reg)
	if err != nil {
		t.Fatalf("RegisterAgent(%q): %v", id, err)
	}
	return agent
}

func TestScheduler_SelectsPlatformAgent(t *testing.T) {
	store := NewAgentStore()
	registerScheduledAgent(t, store, "user-1", OwnerTypeUser, &AgentSystemInfo{CPUCount: 16, MemoryBytes: 32 << 30})
	registerScheduledAgent(t, store, "platform-1", OwnerTypePlatform, &AgentSystemInfo{CPUCount: 4, MemoryBytes: 8 << 30})

	scheduler := NewSchedulerWithOptions(store, time.Minute, time.Now)
	agent, err := scheduler.Select(ResourceHints{})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if agent.AgentID != "platform-1" {
		t.Errorf("selected %q, want platform-1", agent.AgentID)
	}
}

func TestScheduler_RejectsStaleHeartbeat(t *testing.T) {
	store := NewAgentStore()
	registerScheduledAgent(t, store, "platform-1", OwnerTypePlatform, &AgentSystemInfo{CPUCount: 4, MemoryBytes: 8 << 30})

	// Registration sets last_heartbeat to the real current time. Move the
	// scheduler clock far enough ahead that the heartbeat is stale.
	scheduler := NewSchedulerWithOptions(store, time.Minute, func() time.Time {
		return time.Now().Add(10 * time.Minute)
	})
	if _, err := scheduler.Select(ResourceHints{}); err != ErrNoCapacity {
		t.Errorf("Select error = %v, want ErrNoCapacity", err)
	}
}

func TestScheduler_RequiresInventory(t *testing.T) {
	store := NewAgentStore()
	registerScheduledAgent(t, store, "platform-1", OwnerTypePlatform, nil)

	scheduler := NewSchedulerWithOptions(store, time.Minute, time.Now)
	if _, err := scheduler.Select(ResourceHints{}); err != ErrNoCapacity {
		t.Errorf("Select error = %v, want ErrNoCapacity", err)
	}
}

func TestScheduler_RespectsResourceHints(t *testing.T) {
	store := NewAgentStore()
	registerScheduledAgent(t, store, "small", OwnerTypePlatform, &AgentSystemInfo{CPUCount: 2, MemoryBytes: 2 << 30})
	registerScheduledAgent(t, store, "large", OwnerTypePlatform, &AgentSystemInfo{CPUCount: 8, MemoryBytes: 16 << 30})

	scheduler := NewSchedulerWithOptions(store, time.Minute, time.Now)
	agent, err := scheduler.Select(ResourceHints{CPUCount: 4, MemoryBytes: 8 << 30})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if agent.AgentID != "large" {
		t.Errorf("selected %q, want large", agent.AgentID)
	}

	if _, err := scheduler.Select(ResourceHints{CPUCount: 32}); err != ErrNoCapacity {
		t.Errorf("Select too-large error = %v, want ErrNoCapacity", err)
	}
	if _, err := scheduler.Select(ResourceHints{CPUCount: -1}); err != ErrInvalidResourceHints {
		t.Errorf("Select negative error = %v, want ErrInvalidResourceHints", err)
	}
}

func TestScheduler_PrefersLessLoadedAgent(t *testing.T) {
	store := NewAgentStore()
	registerScheduledAgent(t, store, "busy", OwnerTypePlatform, &AgentSystemInfo{CPUCount: 8, MemoryBytes: 16 << 30})
	registerScheduledAgent(t, store, "idle", OwnerTypePlatform, &AgentSystemInfo{CPUCount: 8, MemoryBytes: 16 << 30})

	if err := store.UpdateHeartbeat("busy", AgentStatus{RunningServices: 10}); err != nil {
		t.Fatalf("UpdateHeartbeat: %v", err)
	}

	scheduler := NewSchedulerWithOptions(store, time.Minute, time.Now)
	agent, err := scheduler.Select(ResourceHints{})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if agent.AgentID != "idle" {
		t.Errorf("selected %q, want idle", agent.AgentID)
	}
}

func TestScheduler_NoPlatformAgents(t *testing.T) {
	store := NewAgentStore()
	registerScheduledAgent(t, store, "user-1", OwnerTypeUser, &AgentSystemInfo{CPUCount: 8, MemoryBytes: 16 << 30})

	scheduler := NewSchedulerWithOptions(store, time.Minute, time.Now)
	if _, err := scheduler.Select(ResourceHints{}); err != ErrNoCapacity {
		t.Errorf("Select error = %v, want ErrNoCapacity", err)
	}
}
