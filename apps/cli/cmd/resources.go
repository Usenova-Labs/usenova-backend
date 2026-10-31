package cmd

import (
	"fmt"
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
	if amount < 0 {
		return 0, fmt.Errorf("memory size must not be negative: %q", raw)
	}
	bytes := int64(amount * float64(multiplier))
	return bytes, nil
}
