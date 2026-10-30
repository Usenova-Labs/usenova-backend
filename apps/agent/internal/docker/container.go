package docker

import (
	"net/netip"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

// getRestartPolicy converts a string restart policy to a docker restart policy name.
func getRestartPolicy(policy string) container.RestartPolicyMode {
	switch policy {
	case "always", "always-restart":
		return container.RestartPolicyAlways
	case "on-failure", "on-failure-restart":
		return container.RestartPolicyOnFailure
	case "unless-stopped":
		return container.RestartPolicyUnlessStopped
	case "no", "":
		return container.RestartPolicyDisabled
	default:
		return container.RestartPolicyDisabled
	}
}

// buildContainerConfig creates docker container config from our ContainerConfig
func buildContainerConfig(cfg ContainerConfig) (*container.Config, *container.HostConfig, *network.PortMap) {
	// Build exposed ports
	exposedPorts := network.PortSet{}
	portBindings := network.PortMap{}
	hostIP := netip.MustParseAddr("0.0.0.0")

	for hostPort, containerPort := range cfg.Ports {
		if hostPort == "" || containerPort == "" {
			continue
		}
		port, err := network.ParsePort(containerPort + "/tcp")
		if err != nil {
			continue
		}
		exposedPorts[port] = struct{}{}
		portBindings[port] = []network.PortBinding{{HostIP: hostIP, HostPort: hostPort}}
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
