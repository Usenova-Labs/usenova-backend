package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/byoi-platform/agent/internal/docker"
)

// NetworkRuntime is implemented by container runtimes that can create and
// remove Docker networks. It is optional so existing runtime implementations
// (and test fakes) keep compiling; deployments still pass the resolved
// network name to RunContainer.
type NetworkRuntime interface {
	EnsureNetwork(ctx context.Context, cfg docker.NetworkConfig) error
	RemoveNetwork(ctx context.Context, name string) error
}

// Network modes accepted by a ServiceSpec or the agent-wide NetworkPolicy.
const (
	NetworkModeDefault   = "default"
	NetworkModeBridge    = "bridge"
	NetworkModeNone      = "none"
	NetworkModeDedicated = "dedicated"
)

const (
	// dedicatedNetworkPrefix namespaces agent-created networks so they can be
	// distinguished from user-managed networks.
	dedicatedNetworkPrefix = "usenova-"

	labelNetworkManaged = "usenova.network"
)

// NetworkPolicy is the agent-wide networking default for services that do not
// override it in their spec. The zero value is intentionally equivalent to
// Docker's default bridge, which keeps existing deployments unchanged.
type NetworkPolicy struct {
	// Mode is the default network mode: "bridge"/"default", "none" or
	// "dedicated"/"per-service". Empty means "bridge".
	Mode string
	// Name optionally attaches services to an existing shared network.
	Name string
	// Internal is applied to agent-created dedicated networks. It has no
	// effect on an existing user-managed network.
	Internal bool
}

// DefaultNetworkPolicy returns the backward-compatible default: containers use
// Docker's shared bridge unless a service or the agent opts into isolation.
func DefaultNetworkPolicy() NetworkPolicy {
	return NetworkPolicy{Mode: NetworkModeBridge}
}

// SetNetworkPolicy sets the agent-wide default network policy. A service spec
// may still override it with network_mode/network_name.
func (m *Manager) SetNetworkPolicy(policy NetworkPolicy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.networkPolicy = policy
}

// networkPlan is the resolved networking configuration for one deployment.
type networkPlan struct {
	// Name is the value to pass to Docker's HostConfig.NetworkMode: empty for
	// the default bridge, "none" for no networking, or a network name.
	Name string
	// Mode is the normalized policy mode.
	Mode string
	// Internal is used when the agent creates the network.
	Internal bool
	// Create asks the manager to ensure the network exists before creating
	// the container.
	Create bool
	// Managed marks networks the agent created and may clean up on removal.
	Managed bool
}

// resolveNetwork merges the service spec with the agent-wide policy and
// validates the result. Explicit network_name/network takes precedence over
// the mode for attaching to an existing network.
func (m *Manager) resolveNetwork(spec docker.ServiceSpec) (networkPlan, error) {
	m.mu.RLock()
	policy := m.networkPolicy
	m.mu.RUnlock()

	mode := spec.NetworkMode
	if strings.TrimSpace(mode) == "" {
		mode = policy.Mode
	}
	normalized, err := normalizeNetworkMode(mode)
	if err != nil {
		return networkPlan{}, err
	}

	name := strings.TrimSpace(spec.NetworkName)
	if name == "" {
		name = strings.TrimSpace(spec.Network)
	}
	if strings.EqualFold(name, "host") {
		return networkPlan{}, fmt.Errorf("network_name %q is not supported: host networking bypasses container isolation", name)
	}

	switch normalized {
	case NetworkModeNone:
		return networkPlan{Name: NetworkModeNone, Mode: normalized}, nil

	case NetworkModeDedicated:
		managed := name == ""
		if managed {
			name = dedicatedNetworkName(spec.Name)
		}
		internal := spec.NetworkInternal
		if managed && policy.Internal {
			internal = true
		}
		return networkPlan{
			Name:     name,
			Mode:     normalized,
			Internal: internal,
			Create:   managed, // only agent-owned networks are created; explicit names reference existing ones
			Managed:  managed,
		}, nil

	default: // bridge / default
		if name == "" {
			name = strings.TrimSpace(policy.Name)
		}
		if strings.EqualFold(name, "host") {
			return networkPlan{}, fmt.Errorf("network_name %q is not supported: host networking bypasses container isolation", name)
		}
		return networkPlan{Name: name, Mode: normalized}, nil
	}
}

// normalizeNetworkMode canonicalizes the accepted mode aliases.
func normalizeNetworkMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "default", "bridge":
		return NetworkModeBridge, nil
	case "none", "disabled", "off":
		return NetworkModeNone, nil
	case "dedicated", "per-service", "per_service", "service":
		return NetworkModeDedicated, nil
	case "host":
		return "", fmt.Errorf("network mode %q is not supported: host networking bypasses container isolation", mode)
	default:
		return "", fmt.Errorf("unsupported network mode %q (want default|bridge|none|dedicated)", mode)
	}
}

// dedicatedNetworkName is the deterministic name of the network the agent
// creates for a service when per-service isolation is enabled.
func dedicatedNetworkName(serviceName string) string {
	return dedicatedNetworkPrefix + serviceName
}

// managedNetworkLabels mark a network as owned by this agent. They are only
// applied to networks the agent creates.
func managedNetworkLabels(serviceName string) map[string]string {
	return map[string]string{
		labelManaged:        "true",
		labelService:        serviceName,
		labelNetworkManaged: "true",
	}
}

// ensureNetwork creates the resolved dedicated network before the container is
// started. If the runtime cannot manage networks we still pass the network name
// to Docker, which supports pre-existing user-managed networks.
func (m *Manager) ensureNetwork(ctx context.Context, spec docker.ServiceSpec, plan networkPlan) error {
	if !plan.Create || plan.Name == "" {
		return nil
	}
	nr, ok := m.docker.(NetworkRuntime)
	if !ok {
		m.log.WithField("network", plan.Name).Debug("Container runtime does not expose network management; assuming the network already exists")
		return nil
	}

	cfg := docker.NetworkConfig{Name: plan.Name, Internal: plan.Internal}
	if plan.Managed {
		cfg.Labels = managedNetworkLabels(spec.Name)
	}
	if err := nr.EnsureNetwork(ctx, cfg); err != nil {
		return fmt.Errorf("ensure network %s: %w", plan.Name, err)
	}
	return nil
}

// removeManagedNetwork deletes a dedicated network only when the agent created
// it. User-provided network names are always left untouched. Cleanup is
// best-effort: a network still in use by another container must not make a
// successful service removal fail.
func (m *Manager) removeManagedNetwork(ctx context.Context, spec docker.ServiceSpec) {
	plan, err := m.resolveNetwork(spec)
	if err != nil || !plan.Managed || plan.Name == "" {
		return
	}
	nr, ok := m.docker.(NetworkRuntime)
	if !ok {
		return
	}
	if err := nr.RemoveNetwork(ctx, plan.Name); err != nil {
		m.log.WithError(err).WithField("network", plan.Name).Warn("Could not remove managed Docker network")
	}
}
