package systeminfo

import (
	"runtime"
	"testing"
)

func TestDetect_ReportsPlatform(t *testing.T) {
	info := Detect()

	if info.OS != runtime.GOOS {
		t.Errorf("expected OS %q, got %q", runtime.GOOS, info.OS)
	}
	if info.Arch != runtime.GOARCH {
		t.Errorf("expected arch %q, got %q", runtime.GOARCH, info.Arch)
	}
	if info.CPUCount != runtime.NumCPU() {
		t.Errorf("expected %d CPUs, got %d", runtime.NumCPU(), info.CPUCount)
	}
}

func TestDetect_ReportsMemoryOnSupportedPlatforms(t *testing.T) {
	info := Detect()

	if info.MemoryBytes < 0 {
		t.Fatalf("expected a non-negative memory size, got %d", info.MemoryBytes)
	}

	// Memory detection is best-effort, but it is supported on the platforms
	// the agent runs on.
	switch runtime.GOOS {
	case "linux", "darwin":
		if info.MemoryBytes <= 0 {
			t.Errorf("expected memory to be detected on %s, got %d bytes", runtime.GOOS, info.MemoryBytes)
		}
	}
}
