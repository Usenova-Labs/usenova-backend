package agent

import (
	"time"
)

// DefaultHeartbeatFreshness is how old an agent's last heartbeat may be before
// the scheduler stops considering it eligible for new work. Agents heartbeat
// every 30 seconds by default, so this tolerates a few missed beats.
const DefaultHeartbeatFreshness = 2 * time.Minute

// ResourceHints describe the minimum resources a deployment needs. Zero values
// mean "no requirement".
type ResourceHints struct {
	CPUCount    int   `json:"cpu_count,omitempty"`
	MemoryBytes int64 `json:"memory_bytes,omitempty"`
}

// Scheduler selects a healthy platform agent for a remote deployment.
//
// The policy is deliberately naive: only platform-owned agents with a fresh
// heartbeat and a reported inventory are eligible. Among eligible agents,
// fresher heartbeats, more capacity and fewer running services score higher.
type Scheduler struct {
	store     *AgentStore
	freshness time.Duration
	now       func() time.Time
}

// NewScheduler creates a scheduler with the default freshness window.
func NewScheduler(store *AgentStore) *Scheduler {
	return NewSchedulerWithOptions(store, DefaultHeartbeatFreshness, time.Now)
}

// NewSchedulerWithOptions creates a scheduler with an explicit freshness
// window and clock. It is used by tests to make selection deterministic.
func NewSchedulerWithOptions(store *AgentStore, freshness time.Duration, now func() time.Time) *Scheduler {
	if freshness <= 0 {
		freshness = DefaultHeartbeatFreshness
	}
	if now == nil {
		now = time.Now
	}
	return &Scheduler{store: store, freshness: freshness, now: now}
}

// Select returns the best eligible platform agent for the given hints.
//
// It returns ErrNoCapacity when no agent qualifies. The agent is a snapshot;
// callers must not mutate it.
func (s *Scheduler) Select(hints ResourceHints) (*Agent, error) {
	if hints.CPUCount < 0 || hints.MemoryBytes < 0 {
		return nil, ErrInvalidResourceHints
	}

	now := s.now().UTC()
	candidates := s.store.SnapshotAgentsFiltered(AgentFilter{OwnerType: OwnerTypePlatform})

	var (
		best      *Agent
		bestScore int64
	)
	for i := range candidates {
		candidate := candidates[i]
		if !s.eligible(&candidate, hints, now) {
			continue
		}

		score := s.score(&candidate, now)
		if best == nil || score > bestScore || (score == bestScore && candidate.AgentID < best.AgentID) {
			best = &candidate
			bestScore = score
		}
	}

	if best == nil {
		return nil, ErrNoCapacity
	}
	return best, nil
}

func (s *Scheduler) eligible(agent *Agent, hints ResourceHints, now time.Time) bool {
	if agent.OwnerType != OwnerTypePlatform {
		return false
	}
	if !agent.Capabilities.Docker {
		return false
	}
	if agent.LastHeartbeat.IsZero() {
		return false
	}
	age := now.Sub(agent.LastHeartbeat)
	if age > s.freshness {
		return false
	}

	// An agent that reports no inventory at all cannot be placed reliably.
	if agent.System.CPUCount <= 0 && agent.System.MemoryBytes <= 0 {
		return false
	}
	if hints.CPUCount > 0 && agent.System.CPUCount < hints.CPUCount {
		return false
	}
	if hints.MemoryBytes > 0 && agent.System.MemoryBytes < hints.MemoryBytes {
		return false
	}
	return true
}

func (s *Scheduler) score(agent *Agent, now time.Time) int64 {
	age := now.Sub(agent.LastHeartbeat)
	if age < 0 {
		age = 0
	}

	// Freshness dominates so an agent that stopped reporting is deprioritized
	// even if it has a lot of nominal capacity.
	score := int64(s.freshness-age) / int64(time.Second)
	score += int64(agent.System.CPUCount) * 10
	score += agent.System.MemoryBytes / (1 << 30) * 5
	score -= int64(agent.Status.RunningServices) * 20
	score -= int64(agent.Status.FailedServices) * 10
	return score
}

// SchedulerSelection is the safe, token-free view of a selected agent.
type SchedulerSelection struct {
	AgentID         string            `json:"agent_id"`
	Hostname        string            `json:"hostname"`
	OwnerID         string            `json:"owner_id,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	System          AgentSystemInfo   `json:"system"`
	RunningServices int               `json:"running_services"`
}

// Selection returns the selected agent in a form safe to return over the API.
func (a *Agent) Selection() SchedulerSelection {
	return SchedulerSelection{
		AgentID:         a.AgentID,
		Hostname:        a.Hostname,
		OwnerID:         a.OwnerID,
		Labels:          a.Labels,
		System:          a.System,
		RunningServices: a.Status.RunningServices,
	}
}

// ErrNoCapacity means no eligible platform agent is available.
var ErrNoCapacity = &AgentError{"no platform capacity available"}

// ErrInvalidResourceHints means the requested resource hints are negative.
var ErrInvalidResourceHints = &AgentError{"resource hints must not be negative"}
