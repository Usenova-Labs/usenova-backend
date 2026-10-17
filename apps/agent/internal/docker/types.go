package docker

import (
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
	Type        string // "http" or "tcp" (agent-side probe); empty = infer from ports
	Path        string // HTTP path, e.g. "/health" (used when Type == "http")
	Port        int    // TCP port (used when Type == "tcp")
	Test        []string
	Interval    time.Duration
	Timeout     time.Duration
	Retries     int
	StartPeriod time.Duration
}

// ServiceSpec defines a service to be deployed
type ServiceSpec struct {
	Name          string
	Image         string
	Version       string
	Replicas      int
	Ports         map[string]string
	Env           []string
	Volumes       []string
	HealthCheck   *HealthConfig
	RestartPolicy string
	Resources     *ResourceLimits
	Labels        map[string]string
	DependsOn     []string
}

// ResourceLimits defines resource constraints
type ResourceLimits struct {
	CPULimit      int64 // in CPU shares
	MemoryLimit   int64 // in bytes
	CPUReserve    int64
	MemoryReserve int64
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
