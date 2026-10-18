package docker

import (
	"encoding/json"
	"time"

	"github.com/docker/go-connections/nat"
)

// ContainerConfig holds configuration for creating a container
type ContainerConfig struct {
	Image         string
	Name          string
	Command       []string
	Env           []string
	Ports         map[string]string // hostPort:containerPort (e.g., "8080":"8080")
	Volumes       []string
	Network       string
	RestartPolicy string // "no", "always", "on-failure", "unless-stopped"
	Labels        map[string]string
	WorkingDir    string
	User          string
	TTY           bool
	OpenStdin     bool
	ExtraHosts    []string
	// Resource constraints (0 = unlimited)
	NanoCPUs int64 // billionths of a CPU
	Memory   int64 // bytes
}

// ContainerInfo holds runtime information about a container
type ContainerInfo struct {
	ID              string
	Name            string
	Image           string
	Status          string
	State           string
	Running         bool
	StartedAt       time.Time
	FinishedAt      time.Time
	RestartCount    int
	ExitCode        int
	Ports           []PortBinding
	NetworkSettings NetworkSettings
	HealthStatus    string
}

// PortBinding represents a port binding
type PortBinding struct {
	HostIP        string
	HostPort      string
	ContainerPort string
}

// NetworkSettings represents network configuration
type NetworkSettings struct {
	IPAddress  string
	Gateway    string
	MacAddress string
}

// HealthConfig holds health check configuration.
// Type/Path/Port describe agent-side probes (http|tcp). Test is reserved for
// docker-native HEALTHCHECK CMD forms (TODO: wire into container.Config.Healthcheck).
type HealthConfig struct {
	Type        string        `json:"type,omitempty"` // "http" or "tcp" (agent-side probe); empty = infer from ports
	Path        string        `json:"path,omitempty"` // HTTP path, e.g. "/health" (used when Type == "http")
	Port        int           `json:"port,omitempty"` // TCP port (used when Type == "tcp")
	Test        []string      `json:"test,omitempty"`
	Interval    time.Duration `json:"interval,omitempty"`
	Timeout     time.Duration `json:"timeout,omitempty"`
	Retries     int           `json:"retries,omitempty"`
	StartPeriod time.Duration `json:"start_period,omitempty"`
}

// ServiceSpec defines a service to be deployed.
// JSON tags are part of the agent API contract: clients (CLI, control plane)
// send snake_case keys such as restart_policy and health_check.
type ServiceSpec struct {
	Name          string            `json:"name"`
	Image         string            `json:"image"`
	Version       string            `json:"version,omitempty"`
	Replicas      int               `json:"replicas,omitempty"`
	Ports         map[string]string `json:"ports,omitempty"`
	Env           []string          `json:"env,omitempty"`
	Volumes       []string          `json:"volumes,omitempty"`
	HealthCheck   *HealthConfig     `json:"health_check,omitempty"`
	RestartPolicy string            `json:"restart_policy,omitempty"`
	Resources     *ResourceLimits   `json:"resources,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	DependsOn     []string          `json:"depends_on,omitempty"`
}

// ResourceLimits defines resource constraints
type ResourceLimits struct {
	CPULimit      int64 `json:"cpu_limit,omitempty"`    // in CPU shares
	MemoryLimit   int64 `json:"memory_limit,omitempty"` // in bytes
	CPUReserve    int64 `json:"cpu_reserve,omitempty"`
	MemoryReserve int64 `json:"memory_reserve,omitempty"`
}

// UnmarshalJSON accepts both the current snake_case API keys and the legacy
// Go field-name keys emitted before JSON tags were introduced, so older
// clients do not silently lose fields during the API transition. encoding/json
// matches untagged-style keys case-insensitively only when the folded names
// are equal, so keys whose tags contain underscores (HealthCheck,
// RestartPolicy, DependsOn) need an explicit fallback. The current shape wins
// when both are present.
func (s *ServiceSpec) UnmarshalJSON(data []byte) error {
	type specTags ServiceSpec
	var cur specTags
	if err := json.Unmarshal(data, &cur); err != nil {
		return err
	}
	var legacy struct {
		HealthCheck   *HealthConfig
		RestartPolicy string
		DependsOn     []string
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	out := ServiceSpec(cur)
	if out.HealthCheck == nil {
		out.HealthCheck = legacy.HealthCheck
	}
	if out.RestartPolicy == "" {
		out.RestartPolicy = legacy.RestartPolicy
	}
	if len(out.DependsOn) == 0 {
		out.DependsOn = legacy.DependsOn
	}
	*s = out
	return nil
}

// UnmarshalJSON accepts legacy "StartPeriod" alongside "start_period".
func (h *HealthConfig) UnmarshalJSON(data []byte) error {
	type cfgTags HealthConfig
	var cur cfgTags
	if err := json.Unmarshal(data, &cur); err != nil {
		return err
	}
	var legacy struct {
		StartPeriod time.Duration
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	out := HealthConfig(cur)
	if out.StartPeriod == 0 {
		out.StartPeriod = legacy.StartPeriod
	}
	*h = out
	return nil
}

// UnmarshalJSON accepts legacy "CPULimit"/"MemoryLimit"/"CPUReserve"/
// "MemoryReserve" alongside their snake_case forms.
func (r *ResourceLimits) UnmarshalJSON(data []byte) error {
	type limTags ResourceLimits
	var cur limTags
	if err := json.Unmarshal(data, &cur); err != nil {
		return err
	}
	var legacy struct {
		CPULimit      int64
		MemoryLimit   int64
		CPUReserve    int64
		MemoryReserve int64
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	out := ResourceLimits(cur)
	if out.CPULimit == 0 {
		out.CPULimit = legacy.CPULimit
	}
	if out.MemoryLimit == 0 {
		out.MemoryLimit = legacy.MemoryLimit
	}
	if out.CPUReserve == 0 {
		out.CPUReserve = legacy.CPUReserve
	}
	if out.MemoryReserve == 0 {
		out.MemoryReserve = legacy.MemoryReserve
	}
	*r = out
	return nil
}

// LogOptions holds options for fetching logs
type LogOptions struct {
	Follow     bool
	Timestamps bool
	Since      time.Time
	Tail       string
}

func (c *ContainerConfig) ToPortMap() nat.PortMap {
	portMap := nat.PortMap{}
	for hostPort, containerPort := range c.Ports {
		if hostPort == "" || containerPort == "" {
			continue
		}
		port := nat.Port(containerPort + "/tcp")
		binding := []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: hostPort}}
		portMap[port] = binding
	}
	return portMap
}
