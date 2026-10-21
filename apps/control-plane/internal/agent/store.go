package agent

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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
	// TODO: Add more status fields as needed
	// MemoryUsage     int64   `json:"memory_usage"`
	// CPUUsage        float64 `json:"cpu_usage"`
}

// AgentStore is a thread-safe store for agents. It keeps agents in memory
// for fast reads and, when a database handle is attached, writes every
// mutation through to SQLite so the registry survives control-plane restarts.
// Last reported status is intentionally ephemeral: it is refreshed by the
// next heartbeat after a restart.
type AgentStore struct {
	mu     sync.RWMutex
	agents map[string]*Agent
	db     *sql.DB
}

// NewAgentStore creates a new in-memory-only AgentStore.
func NewAgentStore() *AgentStore {
	return &AgentStore{
		agents: make(map[string]*Agent),
	}
}

// NewPersistentAgentStore creates an AgentStore backed by SQLite. It loads
// every previously registered agent from the database into memory, so the
// registry is intact immediately after a control-plane restart. The database
// schema must already be bootstrapped (see OpenSQLite/Migrate).
func NewPersistentAgentStore(db *sql.DB) (*AgentStore, error) {
	s := &AgentStore{
		agents: make(map[string]*Agent),
		db:     db,
	}

	if err := s.load(); err != nil {
		return nil, err
	}

	return s, nil
}

// load hydrates the in-memory registry from the persisted agents table.
func (s *AgentStore) load() error {
	rows, err := s.db.Query(`
		SELECT agent_id, hostname, version, capabilities, last_heartbeat, registration_time, session_token
		FROM agents`)
	if err != nil {
		return fmt.Errorf("load persisted agents: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			agentID      string
			hostname     string
			version      string
			capabilities string
			lastBeat     string
			registered   string
			sessionToken string
		)

		if err := rows.Scan(&agentID, &hostname, &version, &capabilities, &lastBeat, &registered, &sessionToken); err != nil {
			return fmt.Errorf("scan persisted agent: %w", err)
		}

		lastHeartbeat, err := time.Parse(time.RFC3339Nano, lastBeat)
		if err != nil {
			return fmt.Errorf("parse last heartbeat for agent %q: %w", agentID, err)
		}
		registrationTime, err := time.Parse(time.RFC3339Nano, registered)
		if err != nil {
			return fmt.Errorf("parse registration time for agent %q: %w", agentID, err)
		}

		var caps AgentCapabilities
		if err := json.Unmarshal([]byte(capabilities), &caps); err != nil {
			return fmt.Errorf("parse capabilities for agent %q: %w", agentID, err)
		}

		s.agents[agentID] = &Agent{
			AgentID:          agentID,
			Hostname:         hostname,
			Version:          version,
			Capabilities:     caps,
			LastHeartbeat:    lastHeartbeat,
			RegistrationTime: registrationTime,
			SessionToken:     sessionToken,
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate persisted agents: %w", err)
	}

	return nil
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

	// Persist before publishing to memory so a successful registration is
	// always durable.
	if s.db != nil {
		if err := s.persistRegistration(agent); err != nil {
			return nil, fmt.Errorf("persist agent registration: %w", err)
		}
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

// persistRegistration upserts an agent row. Re-registering an existing agent
// replaces its record (and session token), matching the in-memory semantics.
func (s *AgentStore) persistRegistration(a *Agent) error {
	capabilities, err := json.Marshal(a.Capabilities)
	if err != nil {
		return fmt.Errorf("marshal capabilities: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO agents (agent_id, hostname, version, capabilities, last_heartbeat, registration_time, session_token)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			hostname         = excluded.hostname,
			version          = excluded.version,
			capabilities     = excluded.capabilities,
			last_heartbeat   = excluded.last_heartbeat,
			registration_time = excluded.registration_time,
			session_token    = excluded.session_token`,
		a.AgentID,
		a.Hostname,
		a.Version,
		string(capabilities),
		a.LastHeartbeat.Format(time.RFC3339Nano),
		a.RegistrationTime.Format(time.RFC3339Nano),
		a.SessionToken,
	)
	if err != nil {
		return fmt.Errorf("upsert agent %q: %w", a.AgentID, err)
	}
	return nil
}

// persistHeartbeat updates the stored last-heartbeat timestamp for an agent.
func (s *AgentStore) persistHeartbeat(agentID string, lastHeartbeat time.Time) error {
	_, err := s.db.Exec(
		`UPDATE agents SET last_heartbeat = ? WHERE agent_id = ?`,
		lastHeartbeat.Format(time.RFC3339Nano),
		agentID,
	)
	if err != nil {
		return fmt.Errorf("update heartbeat for agent %q: %w", agentID, err)
	}
	return nil
}

// persistDeletion removes an agent row.
func (s *AgentStore) persistDeletion(agentID string) error {
	if _, err := s.db.Exec(`DELETE FROM agents WHERE agent_id = ?`, agentID); err != nil {
		return fmt.Errorf("delete agent %q: %w", agentID, err)
	}
	return nil
}

// UpdateHeartbeat updates an agent's last heartbeat time and status
func (s *AgentStore) UpdateHeartbeat(agentID string, status AgentStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, exists := s.agents[agentID]
	if !exists {
		return ErrAgentNotFound
	}

	now := time.Now()
	if s.db != nil {
		if err := s.persistHeartbeat(agentID, now); err != nil {
			return fmt.Errorf("persist heartbeat: %w", err)
		}
	}

	agent.LastHeartbeat = now
	agent.Status = status
	return nil
}

// ValidateSessionToken reports whether token is the current session token for
// the given agent. It returns ErrAgentNotFound when the agent is unknown and
// ErrInvalidToken when the token does not match, so callers can distinguish the
// two cases. Comparison is constant-time to avoid leaking token bytes through
// timing.
func (s *AgentStore) ValidateSessionToken(agentID, token string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agent, exists := s.agents[agentID]
	if !exists {
		return ErrAgentNotFound
	}
	if token == "" || subtle.ConstantTimeCompare([]byte(agent.SessionToken), []byte(token)) != 1 {
		return ErrInvalidToken
	}
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

	if s.db != nil {
		if err := s.persistDeletion(agentID); err != nil {
			return fmt.Errorf("persist deletion: %w", err)
		}
	}

	delete(s.agents, agentID)
	return nil
}

// Errors
var (
	ErrAgentNotFound = &AgentError{"agent not found"}
	// ErrInvalidToken is returned when a supplied session token does not match
	// the one issued at registration.
	ErrInvalidToken = &AgentError{"invalid session token"}
)

// AgentError represents agent-related errors
type AgentError struct {
	Message string
}

func (e *AgentError) Error() string {
	return e.Message
}
