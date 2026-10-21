//go:build darwin

package systeminfo

import (
	"strconv"
	"syscall"
)

// totalMemoryBytes returns the total physical memory of the host in bytes, or
// 0 when it cannot be determined.
func totalMemoryBytes() int64 {
	value, err := syscall.Sysctl("hw.memsize")
	if err != nil {
		return 0
	}

	size, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0
	}

	return int64(size)
}
