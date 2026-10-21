package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// Config holds all agent configuration
type Config struct {
	ControlPlaneURL   string
	AgentToken        string
	AgentID           string
	HeartbeatInterval time.Duration
	Version           string

	// CommandPollInterval is how often the agent asks the control plane for
	// pending commands.
	CommandPollInterval time.Duration

	// Local control API (optional, disabled unless API_ADDR is set).
	APIAddr  string
	APIToken string
}

// Load reads configuration from environment variables
func Load(version string) (*Config, error) {
	config := &Config{
		Version: version,
	}

	// Required: Control Plane URL
	config.ControlPlaneURL = os.Getenv("CONTROL_PLANE_URL")
	if config.ControlPlaneURL == "" {
		return nil, fmt.Errorf("CONTROL_PLANE_URL is required")
	}

	// Required: Agent Token for registration
	config.AgentToken = os.Getenv("AGENT_TOKEN")
	if config.AgentToken == "" {
		return nil, fmt.Errorf("AGENT_TOKEN is required")
	}

	// Optional: local control API. Disabled by default. When enabled it must
	// bind to loopback and require a token (enforced by the api package).
	config.APIAddr = os.Getenv("API_ADDR")
	if config.APIAddr != "" {
		config.APIToken = os.Getenv("API_TOKEN")
		if config.APIToken == "" {
			return nil, fmt.Errorf("API_TOKEN is required when API_ADDR is set")
		}
	}

	// Optional: Agent ID (generate if not provided)
	config.AgentID = os.Getenv("AGENT_ID")
	if config.AgentID == "" {
		// Generate a new UUID for this agent
		config.AgentID = uuid.New().String()
	}

	// Optional: Heartbeat interval (default: 30s)
	heartbeatStr := os.Getenv("HEARTBEAT_INTERVAL")
	if heartbeatStr == "" {
		config.HeartbeatInterval = 30 * time.Second
	} else {
		// Try parsing as duration first (e.g., "30s", "1m")
		if interval, err := time.ParseDuration(heartbeatStr); err == nil {
			config.HeartbeatInterval = interval
		} else {
			// Try parsing as seconds
			if seconds, err := strconv.Atoi(heartbeatStr); err == nil {
				config.HeartbeatInterval = time.Duration(seconds) * time.Second
			} else {
				return nil, fmt.Errorf("invalid HEARTBEAT_INTERVAL format: %s (use duration like '30s' or seconds as integer)", heartbeatStr)
			}
		}
	}

	// Validate heartbeat interval bounds
	if config.HeartbeatInterval < 5*time.Second {
		return nil, fmt.Errorf("HEARTBEAT_INTERVAL must be at least 5 seconds")
	}
	if config.HeartbeatInterval > 10*time.Minute {
		return nil, fmt.Errorf("HEARTBEAT_INTERVAL must not exceed 10 minutes")
	}

	// Optional: command poll interval (default: 10s)
	commandStr := os.Getenv("COMMAND_POLL_INTERVAL")
	if commandStr == "" {
		config.CommandPollInterval = 10 * time.Second
	} else {
		if interval, err := time.ParseDuration(commandStr); err == nil {
			config.CommandPollInterval = interval
		} else {
			seconds, err := strconv.Atoi(commandStr)
			if err != nil {
				return nil, fmt.Errorf("invalid COMMAND_POLL_INTERVAL format: %s (use duration like '10s' or seconds as integer)", commandStr)
			}
			config.CommandPollInterval = time.Duration(seconds) * time.Second
		}
	}

	if config.CommandPollInterval < time.Second {
		return nil, fmt.Errorf("COMMAND_POLL_INTERVAL must be at least 1 second")
	}

	return config, nil
}

// String returns a string representation of the config (without sensitive data)
func (c *Config) String() string {
	api := "disabled"
	if c.APIAddr != "" {
		api = c.APIAddr
	}
	return fmt.Sprintf("Config{ControlPlaneURL=%s, AgentID=%s, HeartbeatInterval=%v, Version=%s, API=%s}",
		c.ControlPlaneURL, c.AgentID, c.HeartbeatInterval, c.Version, api)
}
