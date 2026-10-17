package docker

import (
	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
)

// getRestartPolicy converts a string restart policy to a docker restart policy name.
// Docker v24 represents the policy name as a plain string.
func getRestartPolicy(policy string) string {
	switch policy {
	case "always", "always-restart":
		return "always"
	case "on-failure", "on-failure-restart":
		return "on-failure"
	case "unless-stopped":
		return "unless-stopped"
	case "no", "":
		return "no"
	default:
		return "no"
	}
}

// buildContainerConfig creates docker container config from our ContainerConfig
func buildContainerConfig(cfg ContainerConfig) (*container.Config, *container.HostConfig, *nat.PortMap) {
	// Build exposed ports
	exposedPorts := nat.PortSet{}
	portBindings := nat.PortMap{}

	for hostPort, containerPort := range cfg.Ports {
		if hostPort == "" || containerPort == "" {
			continue
		}
		port := nat.Port(containerPort + "/tcp")
		exposedPorts[port] = struct{}{}
		portBindings[port] = []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: hostPort}}
	}

	// Build labels
	labels := cfg.Labels
	if labels == nil {
		labels = make(map[string]string)
	}

	containerConfig := &container.Config{
		Image:        cfg.Image,
		Cmd:          cfg.Command,
		Env:          cfg.Env,
		ExposedPorts: exposedPorts,
		WorkingDir:   cfg.WorkingDir,
		User:         cfg.User,
		Tty:          cfg.TTY,
		OpenStdin:    cfg.OpenStdin,
		Labels:       labels,
	}

	hostConfig := &container.HostConfig{
		PortBindings:  portBindings,
		RestartPolicy: container.RestartPolicy{Name: getRestartPolicy(cfg.RestartPolicy)},
		ExtraHosts:    cfg.ExtraHosts,
		Binds:         cfg.Volumes, // "host:container[:mode]" entries
		// TODO: advanced networking (custom networks, aliases) beyond the
		// default bridge; Mounts API for tmpfs/named volumes.
	}

	// Resource limits (optional)
	if cfg.NanoCPUs > 0 {
		hostConfig.NanoCPUs = cfg.NanoCPUs
	}
	if cfg.Memory > 0 {
		hostConfig.Memory = cfg.Memory
	}

	return containerConfig, hostConfig, &portBindings
}
