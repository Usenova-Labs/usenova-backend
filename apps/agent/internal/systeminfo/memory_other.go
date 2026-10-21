//go:build !linux && !darwin

package systeminfo

// totalMemoryBytes returns 0 on platforms where the total memory cannot be
// determined without pulling in an additional dependency.
func totalMemoryBytes() int64 {
	return 0
}
