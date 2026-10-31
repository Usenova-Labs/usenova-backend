package docker

import (
	"testing"
)

func TestBuildContainerConfigMapsResourceLimits(t *testing.T) {
	cfg := ContainerConfig{
		Image:             "nginx",
		NanoCPUs:          1500000000,
		Memory:            512 << 20,
		CPUShares:         512,
		MemoryReservation: 256 << 20,
	}

	_, hostConfig, _ := buildContainerConfig(cfg)

	if hostConfig.NanoCPUs != 1500000000 {
		t.Errorf("NanoCPUs = %d, want 1500000000", hostConfig.NanoCPUs)
	}
	if hostConfig.Memory != 512<<20 {
		t.Errorf("Memory = %d, want %d", hostConfig.Memory, int64(512<<20))
	}
	if hostConfig.CPUShares != 512 {
		t.Errorf("CPUShares = %d, want 512", hostConfig.CPUShares)
	}
	if hostConfig.MemoryReservation != 256<<20 {
		t.Errorf("MemoryReservation = %d, want %d", hostConfig.MemoryReservation, int64(256<<20))
	}
}

func TestBuildContainerConfigLeavesDefaultsUnlimited(t *testing.T) {
	_, hostConfig, _ := buildContainerConfig(ContainerConfig{Image: "nginx"})

	if hostConfig.NanoCPUs != 0 || hostConfig.Memory != 0 ||
		hostConfig.CPUShares != 0 || hostConfig.MemoryReservation != 0 {
		t.Errorf("unexpected resource limits: %+v", hostConfig)
	}
	if hostConfig.NetworkMode != "" {
		t.Errorf("NetworkMode = %q, want empty for backward-compatible default", hostConfig.NetworkMode)
	}
}

func TestBuildContainerConfigSetsNetworkMode(t *testing.T) {
	_, hostConfig, _ := buildContainerConfig(ContainerConfig{Image: "nginx", Network: "usenova-web"})

	if string(hostConfig.NetworkMode) != "usenova-web" {
		t.Errorf("NetworkMode = %q, want usenova-web", hostConfig.NetworkMode)
	}
}
