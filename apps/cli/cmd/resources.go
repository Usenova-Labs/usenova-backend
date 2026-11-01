package cmd

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// parseMemoryBytes parses Docker-style memory sizes such as "512m", "1.5g" or
// a plain number of bytes. Binary suffixes (ki/mi/gi) and Docker's decimal
// aliases (k/m/g) both use 1024-based multipliers, matching common CLI usage.
func parseMemoryBytes(raw string) (int64, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return 0, nil
	}

	multiplier := int64(1)
	for _, suffix := range []struct {
		suffix string
		mult   int64
	}{
		{"kib", 1 << 10}, {"mib", 1 << 20}, {"gib", 1 << 30}, {"tib", 1 << 40},
		{"kb", 1000}, {"mb", 1000 * 1000}, {"gb", 1000 * 1000 * 1000}, {"tb", 1000 * 1000 * 1000 * 1000},
		{"k", 1 << 10}, {"m", 1 << 20}, {"g", 1 << 30}, {"t", 1 << 40},
		{"b", 1},
	} {
		if strings.HasSuffix(value, suffix.suffix) {
			multiplier = suffix.mult
			value = strings.TrimSpace(strings.TrimSuffix(value, suffix.suffix))
			break
		}
	}

	amount, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory size %q", raw)
	}
	if math.IsNaN(amount) || math.IsInf(amount, 0) {
		return 0, fmt.Errorf("invalid memory size %q", raw)
	}
	if amount < 0 {
		return 0, fmt.Errorf("memory size must not be negative: %q", raw)
	}
	// Guard against values that are positive but too small to represent even
	// one byte after scaling (e.g. "0.5b"), and against sizes that overflow
	// int64 (e.g. "9999999999g").
	scaled := amount * float64(multiplier)
	if scaled >= math.MaxInt64 {
		return 0, fmt.Errorf("memory size %q is too large", raw)
	}
	bytes := int64(scaled)
	if amount > 0 && bytes <= 0 {
		return 0, fmt.Errorf("memory size %q is too small (rounds to 0 bytes)", raw)
	}
	return bytes, nil
}
