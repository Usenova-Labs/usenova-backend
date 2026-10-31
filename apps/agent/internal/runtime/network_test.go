package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"

	"github.com/byoi-platform/agent/internal/docker"
)

func TestDeployDefaultNetworkKeepsBridgeAndUnlimitedResources(t *testing.T) {
	fake := newFakeRuntime()
	m := NewManager(fake, testLogger())

	if _, err := m.DeployService(context.Background(), docker.ServiceSpec{Name: "web", Image: "nginx"}); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	cfg := fake.runConfigs[0]
	if cfg.Network != "" {
		t.Errorf("Network = %q, want empty for Docker default bridge", cfg.Network)
	}
	if cfg.NanoCPUs != 0 || cfg.Memory != 0 || cfg.CPUShares != 0 || cfg.MemoryReservation != 0 {
		t.Errorf("unexpected default limits: %+v", cfg)
	}
	if len(fake.ensuredNetworks) != 0 {
		t.Errorf("ensured networks = %v, want none", fake.ensuredNetworks)
	}
}

func TestDeployMapsResourceLimits(t *testing.T) {
	fake := newFakeRuntime()
	m := NewManager(fake, testLogger())

	spec := docker.ServiceSpec{
		Name:  "web",
		Image: "nginx",
		Resources: &docker.ResourceLimits{
			CPULimit:      2e9,
			MemoryLimit:   1 << 30,
			CPUReserve:    1024,
			MemoryReserve: 512 << 20,
		},
	}
	if _, err := m.DeployService(context.Background(), spec); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	cfg := fake.runConfigs[0]
	if cfg.NanoCPUs != 2e9 {
		t.Errorf("NanoCPUs = %d, want 2e9", cfg.NanoCPUs)
	}
	if cfg.Memory != 1<<30 {
		t.Errorf("Memory = %d, want 1GiB", cfg.Memory)
	}
	if cfg.CPUShares != 1024 {
		t.Errorf("CPUShares = %d, want 1024", cfg.CPUShares)
	}
	if cfg.MemoryReservation != 512<<20 {
		t.Errorf("MemoryReservation = %d, want 512MiB", cfg.MemoryReservation)
	}
}

func TestDeployDedicatedNetworkCreatesAndRemovesManagedNetwork(t *testing.T) {
	fake := newFakeRuntime()
	m := NewManager(fake, testLogger())

	spec := docker.ServiceSpec{Name: "web", Image: "nginx", NetworkMode: "dedicated"}
	if _, err := m.DeployService(context.Background(), spec); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	if len(fake.ensuredNetworks) != 1 {
		t.Fatalf("ensured networks = %v, want one", fake.ensuredNetworks)
	}
	netCfg := fake.ensuredNetworks[0]
	if netCfg.Name != "usenova-web" {
		t.Errorf("network name = %q, want usenova-web", netCfg.Name)
	}
	if netCfg.Labels[labelManaged] != "true" || netCfg.Labels[labelNetworkManaged] != "true" {
		t.Errorf("network labels = %v, want managed markers", netCfg.Labels)
	}
	if fake.runConfigs[0].Network != "usenova-web" {
		t.Errorf("container network = %q, want usenova-web", fake.runConfigs[0].Network)
	}

	if err := m.RemoveService(context.Background(), "web"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fake.removedNetworks) != 1 || fake.removedNetworks[0] != "usenova-web" {
		t.Errorf("removed networks = %v, want [usenova-web]", fake.removedNetworks)
	}
}

func TestDeployExplicitNetworkIsNeverRemoved(t *testing.T) {
	fake := newFakeRuntime()
	m := NewManager(fake, testLogger())

	spec := docker.ServiceSpec{
		Name:        "web",
		Image:       "nginx",
		NetworkMode: "dedicated",
		NetworkName: "shared-net",
	}
	if _, err := m.DeployService(context.Background(), spec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if len(fake.ensuredNetworks) != 1 || fake.ensuredNetworks[0].Name != "shared-net" {
		t.Fatalf("ensured networks = %v, want shared-net", fake.ensuredNetworks)
	}
	if fake.ensuredNetworks[0].Labels != nil {
		t.Errorf("explicit network labels = %v, want nil (not agent-owned)", fake.ensuredNetworks[0].Labels)
	}
	if fake.runConfigs[0].Network != "shared-net" {
		t.Errorf("container network = %q, want shared-net", fake.runConfigs[0].Network)
	}

	if err := m.RemoveService(context.Background(), "web"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(fake.removedNetworks) != 0 {
		t.Errorf("removed networks = %v, want none for user-managed network", fake.removedNetworks)
	}
}

func TestAgentNetworkPolicyDefaultsForServices(t *testing.T) {
	fake := newFakeRuntime()
	m := NewManager(fake, testLogger())
	m.SetNetworkPolicy(NetworkPolicy{Mode: "dedicated", Internal: true})

	if _, err := m.DeployService(context.Background(), docker.ServiceSpec{Name: "api", Image: "api"}); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if len(fake.ensuredNetworks) != 1 || fake.ensuredNetworks[0].Name != "usenova-api" {
		t.Fatalf("ensured networks = %v, want usenova-api", fake.ensuredNetworks)
	}
	if !fake.ensuredNetworks[0].Internal {
		t.Error("Internal = false, want agent policy internal network")
	}
}

func TestNetworkNoneDisablesNetworking(t *testing.T) {
	fake := newFakeRuntime()
	m := NewManager(fake, testLogger())

	if _, err := m.DeployService(context.Background(), docker.ServiceSpec{
		Name:        "worker",
		Image:       "worker",
		NetworkMode: "none",
	}); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if fake.runConfigs[0].Network != "none" {
		t.Errorf("Network = %q, want none", fake.runConfigs[0].Network)
	}
	if len(fake.ensuredNetworks) != 0 {
		t.Errorf("ensured networks = %v, want none", fake.ensuredNetworks)
	}
}

func TestDeployRejectsHostAndUnknownNetworkModes(t *testing.T) {
	for _, mode := range []string{"host", "bogus"} {
		fake := newFakeRuntime()
		m := NewManager(fake, testLogger())
		_, err := m.DeployService(context.Background(), docker.ServiceSpec{
			Name:        "web",
			Image:       "nginx",
			NetworkMode: mode,
		})
		if err == nil || !strings.Contains(err.Error(), "network") {
			t.Errorf("mode %q error = %v, want network mode error", mode, err)
		}
		if len(fake.runConfigs) != 0 {
			t.Errorf("mode %q started a container, want rejection before run", mode)
		}
	}
}

func TestDeployRejectsHostNetworkName(t *testing.T) {
	fake := newFakeRuntime()
	m := NewManager(fake, testLogger())
	_, err := m.DeployService(context.Background(), docker.ServiceSpec{
		Name:        "web",
		Image:       "nginx",
		NetworkName: "host",
	})
	if err == nil || !strings.Contains(err.Error(), "host networking") {
		t.Fatalf("error = %v, want host networking rejection", err)
	}
	if len(fake.runConfigs) != 0 {
		t.Error("container started, want rejection before run")
	}
}

func TestSpecFromInspectRestoresResourcesAndNetwork(t *testing.T) {
	info := &container.InspectResponse{
		Config: &container.Config{Image: "nginx"},
		HostConfig: &container.HostConfig{
			Resources: container.Resources{
				NanoCPUs:          1500000000,
				Memory:            512 << 20,
				CPUShares:         256,
				MemoryReservation: 128 << 20,
			},
			NetworkMode: "usenova-web",
		},
	}

	spec := specFromInspect("web", info, "")
	if spec.Resources == nil {
		t.Fatal("Resources = nil, want restored limits")
	}
	if spec.Resources.CPULimit != 1500000000 || spec.Resources.MemoryLimit != 512<<20 ||
		spec.Resources.CPUReserve != 256 || spec.Resources.MemoryReserve != 128<<20 {
		t.Errorf("Resources = %+v, want all four values restored", *spec.Resources)
	}
	if spec.NetworkMode != NetworkModeDedicated || spec.NetworkName != "" {
		t.Errorf("network = mode %q name %q, want dedicated with generated name", spec.NetworkMode, spec.NetworkName)
	}

	info.HostConfig.NetworkMode = "shared-net"
	spec = specFromInspect("web", info, "")
	if spec.NetworkName != "shared-net" {
		t.Errorf("NetworkName = %q, want shared-net", spec.NetworkName)
	}
}
