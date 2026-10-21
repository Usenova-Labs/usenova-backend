// Package systeminfo reports a best-effort inventory of the machine the
// agent runs on.
//
// Detection never fails: an agent must still be able to register on hosts
// where the information cannot be determined, so unknown values are reported
// as zero values instead of aborting the detection.
package systeminfo

import "runtime"

// Info describes the host machine the agent runs on.
type Info struct {
	// OS is the operating system the agent runs on (e.g. "linux").
	OS string `json:"os,omitempty"`
	// Arch is the CPU architecture the agent runs on (e.g. "amd64").
	Arch string `json:"arch,omitempty"`
	// CPUCount is the number of logical CPUs usable by the agent.
	CPUCount int `json:"cpu_count,omitempty"`
	// MemoryBytes is the total physical memory of the host, or 0 when it
	// cannot be determined.
	MemoryBytes int64 `json:"memory_bytes,omitempty"`
}

// Detect collects the machine inventory of the current host.
func Detect() Info {
	return Info{
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		CPUCount:    runtime.NumCPU(),
		MemoryBytes: totalMemoryBytes(),
	}
}
