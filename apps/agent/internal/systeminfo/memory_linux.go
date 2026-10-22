//go:build linux

package systeminfo

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// meminfoPath is the procfs file exposing the host memory statistics.
const meminfoPath = "/proc/meminfo"

// totalMemoryBytes returns the total physical memory of the host in bytes.
//
// TODO: report the cgroup memory limit instead when the agent runs inside a
// container, since that is the capacity actually available to it.
func totalMemoryBytes() int64 {
	file, err := os.Open(meminfoPath)
	if err != nil {
		return 0
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		// Only MemTotal describes the installed memory.
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}

		// procfs reports the value in kilobytes.
		kilobytes, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0
		}

		return kilobytes * 1024
	}

	return 0
}
