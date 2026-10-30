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

// AgentSystemInfo describes the machine an agent runs on.
//
// The inventory is best-effort: agents that do not report it (older agents,
// or hosts where the details cannot be determined) leave the fields empty.
type AgentSystemInfo struct {
	OS          string `json:"os,omitempty"`
	Arch        string `json:"arch,omitempty"`
	CPUCount    int    `json:"cpu_count,omitempty"`
	MemoryBytes int64  `json:"memory_bytes,omitempty"`
	// DockerVersion is the version of the Docker engine available on the
	// agent host. It is empty when Docker is unavailable or its version is
	// unknown.
	DockerVersion string `json:"docker_version,omitempty"`
}

// Agent represents a registered agent in the system
type Agent struct {
	AgentID          string            `json:"agent_id"`
	Hostname         string            `json:"hostname"`
	Version          string            `json:"version"`
	Capabilities     AgentCapabilities `json:"capabilities"`
	System           AgentSystemInfo   `json:"system"`
	OwnerType        OwnerType         `json:"owner_type"`
	OwnerID          string            `json:"owner_id,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	LastHeartbeat    time.Time         `json:"last_heartbeat"`
	RegistrationTime time.Time         `json:"registration_time"`
	Status           AgentStatus       `json:"status,omitempty"`
	SessionToken     string            `json:"session_token,omitempty"`
}

// AgentFilter narrows the agents returned by ListAgentsFiltered. Zero values
// match every agent. Labels are matched exactly and all supplied labels must
// be present.
type AgentFilter struct {
	OwnerType OwnerType
	OwnerID   string
	Labels    map[string]string
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

// AgentStore is a thread-safe store for agents. It keeps agents in memory
// for fast reads and, when a database handle is attached, writes every
// mutation through to SQLite so the registry survives control-plane restarts.
// Last reported status is intentionally ephemeral: it is refreshed by the
// next heartbeat after a restart.
type AgentStore struct {
	mu     sync.RWMutex
	agents map[string]*Agent
	tokens map[string]string
	db     *sql.DB
}

// NewAgentStore creates a new in-memory-only AgentStore.
func NewAgentStore() *AgentStore {
	return &AgentStore{
		agents: make(map[string]*Agent),
		tokens: make(map[string]string),
	}
}

// AgentRegistration carries the data an agent reports when it registers.
type AgentRegistration struct {
	// AgentID is optional: the control plane generates one when it is empty.
	AgentID  string
	Hostname string
	Version  string
	// Capabilities describes what the agent is able to do.
	Capabilities AgentCapabilities
	// System describes the machine the agent runs on. It is empty for
	// agents that do not report an inventory.
	System AgentSystemInfo
	// OwnerType defaults to user when empty.
	OwnerType OwnerType
	// OwnerID and Labels are fleet metadata for platform-owned agents.
	OwnerID string
	Labels  map[string]string
}

// NewPersistentAgentStore creates an AgentStore backed by SQLite. It loads
// every previously registered agent from the database into memory, so the
// registry is intact immediately after a control-plane restart. The database
// schema must already be bootstrapped (see OpenSQLite/Migrate).
func NewPersistentAgentStore(db *sql.DB) (*AgentStore, error) {
	s := &AgentStore{
		agents: make(map[string]*Agent),
		tokens: make(map[string]string),
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
		SELECT agent_id, hostname, version, capabilities, owner_type, owner_id, labels,
		       last_heartbeat, registration_time, session_token
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
			ownerType    string
			ownerID      string
			labels       string
			lastBeat     string
			registered   string
			sessionToken string
		)

		if err := rows.Scan(&agentID, &hostname, &version, &capabilities, &ownerType, &ownerID, &labels, &lastBeat, &registered, &sessionToken); err != nil {
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

		var agentLabels map[string]string
		if labels != "" {
			if err := json.Unmarshal([]byte(labels), &agentLabels); err != nil {
				return fmt.Errorf("parse labels for agent %q: %w", agentID, err)
			}
		}

		agent := &Agent{
			AgentID:          agentID,
			Hostname:         hostname,
			Version:          version,
			Capabilities:     caps,
			OwnerType:        OwnerType(ownerType),
			OwnerID:          ownerID,
			Labels:           agentLabels,
			LastHeartbeat:    lastHeartbeat,
			RegistrationTime: registrationTime,
			SessionToken:     sessionToken,
		}
		if agent.OwnerType == "" {
			agent.OwnerType = OwnerTypeUser
		}
		s.agents[agentID] = agent
		if sessionToken != "" {
			s.tokens[sessionToken] = agentID
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate persisted agents: %w", err)
	}
	return nil
}

// RegisterAgent registers or updates an agent
func (s *AgentStore) RegisterAgent(reg AgentRegistration) (*Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	ownerType := reg.OwnerType
	if ownerType == "" {
		ownerType = OwnerTypeUser
	}
	if !ownerType.Valid() {
		return nil, ErrInvalidOwnerType
	}

	// Generate agent ID if not provided.
	agentID := reg.AgentID
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
		Hostname:         reg.Hostname,
		Version:          reg.Version,
		Capabilities:     reg.Capabilities,
		System:           reg.System,
		OwnerType:        ownerType,
		OwnerID:          reg.OwnerID,
		Labels:           reg.Labels,
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

	if prev, ok := s.agents[agentID]; ok && prev.SessionToken != "" {
		delete(s.tokens, prev.SessionToken)
	}
	s.agents[agentID] = agent
	if agent.SessionToken != "" {
		s.tokens[agent.SessionToken] = agentID
	}
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
	agentLabels := a.Labels
	if agentLabels == nil {
		agentLabels = map[string]string{}
	}
	labels, err := json.Marshal(agentLabels)
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO agents (agent_id, hostname, version, capabilities, owner_type, owner_id, labels, last_heartbeat, registration_time, session_token)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			hostname         = excluded.hostname,
			version          = excluded.version,
			capabilities     = excluded.capabilities,
			owner_type       = excluded.owner_type,
			owner_id         = excluded.owner_id,
			labels           = excluded.labels,
			last_heartbeat   = excluded.last_heartbeat,
			registration_time = excluded.registration_time,
			session_token    = excluded.session_token`,
		a.AgentID,
		a.Hostname,
		a.Version,
		string(capabilities),
		string(a.OwnerType),
		a.OwnerID,
		string(labels),
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

// UpdateSystemInfo refreshes the machine inventory recorded for an agent.
//
// Agents report their inventory on every heartbeat, so dynamic values (CPU
// count, Docker version, ...) stay current without re-registering.
func (s *AgentStore) UpdateSystemInfo(agentID string, system AgentSystemInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, exists := s.agents[agentID]
	if !exists {
		return ErrAgentNotFound
	}

	agent.System = system
	return nil
}

// ValidateSessionToken reports whether token is the current session token for
// the given agent. It returns ErrAgentNotFound when the agent is unknown,
// ErrTokenForOtherAgent when the token is valid but was issued to a different
// agent, and ErrInvalidToken when the token does not belong to any agent, so
// callers can distinguish the cases. Comparisons are constant-time to avoid
// leaking token bytes through timing.
func (s *AgentStore) ValidateSessionToken(agentID, token string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agent, exists := s.agents[agentID]
	if !exists {
		return ErrAgentNotFound
	}
	if token != "" && subtle.ConstantTimeCompare([]byte(agent.SessionToken), []byte(token)) == 1 {
		return nil
	}

	if ownerID, ok := s.tokens[token]; ok && ownerID != agentID {
		return ErrTokenForOtherAgent
	}
	return ErrInvalidToken
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

// ListAgents returns all registered agents.
func (s *AgentStore) ListAgents() []*Agent {
	return s.ListAgentsFiltered(AgentFilter{})
}

// ListAgentsFiltered returns the registered agents matching filter.
func (s *AgentStore) ListAgentsFiltered(filter AgentFilter) []*Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agents := make([]*Agent, 0, len(s.agents))
	for _, agent := range s.agents {
		if !agentMatchesFilter(agent, filter) {
			continue
		}
		agents = append(agents, agent)
	}

	return agents
}

// SnapshotAgentsFiltered returns copies of the registered agents matching
// filter. Callers that need to read mutable fields (heartbeat, status) after
// releasing the store lock should use this instead of ListAgentsFiltered.
func (s *AgentStore) SnapshotAgentsFiltered(filter AgentFilter) []Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agents := make([]Agent, 0, len(s.agents))
	for _, agent := range s.agents {
		if !agentMatchesFilter(agent, filter) {
			continue
		}
		agents = append(agents, *agent)
	}

	return agents
}

func agentMatchesFilter(agent *Agent, filter AgentFilter) bool {
	if filter.OwnerType != "" && agent.OwnerType != filter.OwnerType {
		return false
	}
	if filter.OwnerID != "" && agent.OwnerID != filter.OwnerID {
		return false
	}
	for key, want := range filter.Labels {
		if got, ok := agent.Labels[key]; !ok || got != want {
			return false
		}
	}
	return true
}

// DeleteAgent removes an agent from the store
func (s *AgentStore) DeleteAgent(agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, exists := s.agents[agentID]
	if !exists {
		return ErrAgentNotFound
	}

	if s.db != nil {
		if err := s.persistDeletion(agentID); err != nil {
			return fmt.Errorf("persist deletion: %w", err)
		}
	}

	if agent.SessionToken != "" {
		delete(s.tokens, agent.SessionToken)
	}
	delete(s.agents, agentID)
	return nil
}

// Errors
var (
	ErrAgentNotFound = &AgentError{"agent not found"}
	// ErrInvalidToken is returned when a supplied session token does not belong
	// to any registered agent.
	ErrInvalidToken = &AgentError{"invalid session token"}
	// ErrTokenForOtherAgent is returned when a valid session token is presented
	// for an agent other than the one it was issued to.
	ErrTokenForOtherAgent = &AgentError{"session token does not match agent"}
	// ErrInvalidOwnerType is returned when a registration carries an unknown
	// owner type.
	ErrInvalidOwnerType = &AgentError{"invalid owner type"}
)

// AgentError represents agent-related errors
type AgentError struct {
	Message string
}

func (e *AgentError) Error() string {
	return e.Message
}
