package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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

	// PlatformBootstrapToken selects the platform registration path when set.
	// PlatformOwnerID and PlatformLabels are fleet metadata reported with it.
	PlatformBootstrapToken string
	PlatformOwnerID        string
	PlatformLabels         map[string]string

	// CommandPollInterval is how often the agent asks the control plane for
	// pending commands.
	CommandPollInterval time.Duration

	// Local control API (optional, disabled unless API_ADDR is set).
	APIAddr  string
	APIToken string

	// RestartMaxRestarts is how many agent-initiated restarts a service may
	// get inside RestartWindow before the agent gives up on it. Values <= 0
	// disable the limit.
	RestartMaxRestarts int

	// RestartWindow is the sliding window over which restart attempts are
	// counted.
	RestartWindow time.Duration

	// RestartBackoff is the base delay between restart attempts. It doubles
	// with every consecutive attempt.
	RestartBackoff time.Duration

	// NetworkMode is the default Docker network policy for deployments that
	// do not set network_mode in their service spec. "bridge" keeps the
	// historical behavior; "dedicated" gives every service its own network.
	NetworkMode string

	// NetworkName attaches services to an existing shared Docker network.
	NetworkName string

	// NetworkInternal makes agent-created dedicated networks internal
	// (no external connectivity).
	NetworkInternal bool
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

	// Optional: platform registration. A dedicated bootstrap token selects
	// the platform path; without it the agent registers as a BYOI agent.
	config.PlatformBootstrapToken = os.Getenv("PLATFORM_BOOTSTRAP_TOKEN")
	config.PlatformOwnerID = os.Getenv("PLATFORM_OWNER_ID")
	labels, err := labelsEnv("PLATFORM_LABELS")
	if err != nil {
		return nil, err
	}
	config.PlatformLabels = labels
	if config.PlatformBootstrapToken == "" && (config.PlatformOwnerID != "" || len(config.PlatformLabels) > 0) {
		return nil, fmt.Errorf("PLATFORM_BOOTSTRAP_TOKEN is required when PLATFORM_OWNER_ID or PLATFORM_LABELS is set")
	}

	// Optional: Heartbeat interval (default: 30s)
	heartbeat, err := durationEnv("HEARTBEAT_INTERVAL", 30*time.Second)
	if err != nil {
		return nil, err
	}
	config.HeartbeatInterval = heartbeat

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

	// Optional: restart limits (defaults: 3 restarts per 10m window, 30s
	// backoff). They keep a crash-looping service from being restarted
	// forever.
	maxRestarts := os.Getenv("RESTART_MAX_RESTARTS")
	if maxRestarts == "" {
		config.RestartMaxRestarts = 3
	} else {
		parsed, err := strconv.Atoi(maxRestarts)
		if err != nil {
			return nil, fmt.Errorf("invalid RESTART_MAX_RESTARTS format: %s (use an integer)", maxRestarts)
		}
		config.RestartMaxRestarts = parsed
	}

	window, err := durationEnv("RESTART_WINDOW", 10*time.Minute)
	if err != nil {
		return nil, err
	}
	config.RestartWindow = window

	backoff, err := durationEnv("RESTART_BACKOFF", 30*time.Second)
	if err != nil {
		return nil, err
	}
	config.RestartBackoff = backoff

	if config.RestartWindow <= 0 {
		return nil, fmt.Errorf("RESTART_WINDOW must be positive")
	}
	if config.RestartBackoff < 0 {
		return nil, fmt.Errorf("RESTART_BACKOFF must not be negative")
	}

	// Optional: Docker network policy. The default preserves Docker's shared
	// bridge so existing deployments behave exactly as before.
	config.NetworkMode = strings.ToLower(envAny("AGENT_NETWORK_MODE", "DOCKER_NETWORK_MODE", "NETWORK_MODE"))
	if config.NetworkMode == "" {
		config.NetworkMode = "bridge"
	}
	switch config.NetworkMode {
	case "bridge", "default":
		config.NetworkMode = "bridge"
	case "none", "dedicated", "per-service", "per_service":
	case "host":
		return nil, fmt.Errorf("AGENT_NETWORK_MODE=host is not supported: host networking bypasses container isolation")
	default:
		return nil, fmt.Errorf("invalid AGENT_NETWORK_MODE %q (want bridge|default|none|dedicated|per-service)", config.NetworkMode)
	}
	config.NetworkName = envAny("AGENT_NETWORK_NAME", "DOCKER_NETWORK_NAME")
	internal, err := boolEnvAny(false, "AGENT_NETWORK_INTERNAL", "DOCKER_NETWORK_INTERNAL")
	if err != nil {
		return nil, err
	}
	config.NetworkInternal = internal

	return config, nil
}

// labelsEnv parses comma-separated key=value labels from the environment.
func labelsEnv(name string) (map[string]string, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return nil, nil
	}

	labels := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid %s format: %s (use comma-separated key=value pairs)", name, raw)
		}
		labels[key] = strings.TrimSpace(value)
	}
	return labels, nil
}

// envAny returns the first non-empty value among the given environment
// variables. Aliases keep older deployment manifests working while the
// AGENT_* names remain the documented form.
func envAny(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

// boolEnvAny reads a boolean from the first non-empty alias, defaulting when
// all are unset.
func boolEnvAny(def bool, names ...string) (bool, error) {
	for _, name := range names {
		raw := strings.TrimSpace(os.Getenv(name))
		if raw == "" {
			continue
		}
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return false, fmt.Errorf("invalid %s format: %s (use true or false)", name, raw)
		}
		return parsed, nil
	}
	return def, nil
}

// durationEnv reads a duration from the environment. It accepts a duration
// string ("30s", "1m") or a plain number of seconds.
func durationEnv(name string, def time.Duration) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return def, nil
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return d, nil
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		return time.Duration(seconds) * time.Second, nil
	}
	return 0, fmt.Errorf("invalid %s format: %s (use duration like '30s' or seconds as integer)", name, raw)
}

// String returns a string representation of the config (without sensitive data)
func (c *Config) String() string {
	api := "disabled"
	if c.APIAddr != "" {
		api = c.APIAddr
	}
	registration := "user"
	if c.PlatformBootstrapToken != "" {
		registration = "platform"
	}
	return fmt.Sprintf("Config{ControlPlaneURL=%s, AgentID=%s, HeartbeatInterval=%v, Version=%s, Registration=%s, API=%s, Restarts=%d/%v backoff=%v, Network=%s}",
		c.ControlPlaneURL, c.AgentID, c.HeartbeatInterval, c.Version, registration, api,
		c.RestartMaxRestarts, c.RestartWindow, c.RestartBackoff, c.NetworkMode)
}
