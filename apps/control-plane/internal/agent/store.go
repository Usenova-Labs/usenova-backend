package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// AgentCapabilities represents what this agent can do
type AgentCapabilities struct {
	Docker bool `json:"docker"`
	// TODO: Add more capabilities as needed
	// Kubernetes bool `json:"kubernetes"`
	// Systemd    bool `json:"systemd"`
}

// Agent represents a registered agent in the system
type Agent struct {
	AgentID          string            `json:"agent_id"`
	Hostname         string            `json:"hostname"`
	Version          string            `json:"version"`
	Capabilities     AgentCapabilities `json:"capabilities"`
	LastHeartbeat    time.Time         `json:"last_heartbeat"`
	RegistrationTime time.Time         `json:"registration_time"`
	Status           AgentStatus       `json:"status,omitempty"`
	SessionToken     string            `json:"session_token,omitempty"`
}

// AgentStatus represents current agent status information
type AgentStatus struct {
	Uptime          time.Duration `json:"uptime"`
	RunningServices int           `json:"running_services"`
	// FailedServices counts services the agent gave up on (they exhausted
	// their restart budget or their deploy failed).
	FailedServices int `json:"failed_services,omitempty"`
	// TODO: Add more status fields as needed
	// MemoryUsage     int64   `json:"memory_usage"`
	// CPUUsage        float64 `json:"cpu_usage"`
}

// AgentStore is a thread-safe in-memory store for agents
type AgentStore struct {
	mu     sync.RWMutex
	agents map[string]*Agent
}

// NewAgentStore creates a new AgentStore
func NewAgentStore() *AgentStore {
	return &AgentStore{
		agents: make(map[string]*Agent),
	}
}

// RegisterAgent registers or updates an agent
func (s *AgentStore) RegisterAgent(agentID, hostname, version string, capabilities AgentCapabilities) (*Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	// Generate agent ID if not provided.
	if agentID == "" {
		agentID = uuid.NewString()
	}

	// Session token used to authenticate this agent on subsequent requests.
	// TODO: replace with a signed, expiring JWT (and persist only a hash) once
	// the control plane has auth middleware.
	sessionToken, err := generateSessionToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate session token: %w", err)
	}

	agent := &Agent{
		AgentID:          agentID,
		Hostname:         hostname,
		Version:          version,
		Capabilities:     capabilities,
		RegistrationTime: now,
		LastHeartbeat:    now,
		SessionToken:     sessionToken,
	}

	s.agents[agentID] = agent
	return agent, nil
}

// generateSessionToken returns a cryptographically random 256-bit token.
func generateSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// UpdateHeartbeat updates an agent's last heartbeat time and status
func (s *AgentStore) UpdateHeartbeat(agentID string, status AgentStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, exists := s.agents[agentID]
	if !exists {
		return ErrAgentNotFound
	}

	agent.LastHeartbeat = time.Now()
	agent.Status = status
	return nil
}

// GetAgent retrieves an agent by ID
func (s *AgentStore) GetAgent(agentID string) (*Agent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agent, exists := s.agents[agentID]
	if !exists {
		return nil, ErrAgentNotFound
	}

	return agent, nil
}

// ListAgents returns all registered agents
func (s *AgentStore) ListAgents() []*Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agents := make([]*Agent, 0, len(s.agents))
	for _, agent := range s.agents {
		agents = append(agents, agent)
	}

	return agents
}

// DeleteAgent removes an agent from the store
func (s *AgentStore) DeleteAgent(agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.agents[agentID]; !exists {
		return ErrAgentNotFound
	}

	delete(s.agents, agentID)
	return nil
}

// Errors
var (
	ErrAgentNotFound = &AgentError{"agent not found"}
)

// AgentError represents agent-related errors
type AgentError struct {
	Message string
}

func (e *AgentError) Error() string {
	return e.Message
}
