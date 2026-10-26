package container

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type HostResources struct {
	ID       string
	NCPU     int
	MemTotal int64
}

// DetectHostResources queries the selected daemon, not the worker container.
func DetectHostResources(ctx context.Context, cfg *Config) (HostResources, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var host HostResources
	if alternateCommandFactory() != nil {
		return host, fmt.Errorf("host detection is only supported by the Docker runtime")
	}
	out, err := dockerCommandContext(ctx, cfg, "info", "--format", "{{json .}}").Output()
	if err != nil {
		return host, fmt.Errorf("cannot detect scanner host capacity: %w", err)
	}
	if err := json.Unmarshal(out, &host); err != nil || host.ID == "" || host.NCPU < 1 || host.MemTotal < 1 {
		return host, fmt.Errorf("Docker returned invalid host capacity")
	}
	return host, nil
}

// MemoryLimitFor is shared by container creation and admission scheduling.
// These tools need more than the default cap even on modest repositories.
func MemoryLimitFor(cfg *Config, tool string) string {
	switch tool {
	case "bearer", "gosec":
		return "4g"
	case "grype", "codeql":
		return "6g"
	default:
		if cfg != nil {
			return cfg.Memory
		}
		return "2g"
	}
}

func MemoryBytes(limit string) (int64, error) {
	// Docker's k/m/g suffixes mean powers of 1024, unlike SI quantities.
	value := strings.ToLower(strings.TrimSpace(limit))
	value = strings.TrimSuffix(value, "b")
	multiplier := float64(1)
	for _, unit := range []struct {
		docker string
		bytes  float64
	}{{"k", 1 << 10}, {"m", 1 << 20}, {"g", 1 << 30}, {"t", 1 << 40}} {
		if strings.HasSuffix(value, unit.docker) {
			value = strings.TrimSuffix(value, unit.docker)
			multiplier = unit.bytes
			break
		}
	}
	q, err := strconv.ParseFloat(value, 64)
	bytes := q * multiplier
	if err != nil || math.IsNaN(bytes) || bytes < 1 || bytes >= math.MaxInt64 {
		return 0, fmt.Errorf("invalid scanner memory limit %q", limit)
	}
	return int64(math.Ceil(bytes)), nil
}

func CPUCount(cfg *Config) float64 {
	if cfg != nil {
		if cpus, err := strconv.ParseFloat(cfg.CPUs, 64); err == nil && cpus > 0 && !math.IsInf(cpus, 0) {
			return cpus
		}
	}
	return 1.5
}

// goMemoryLimit is the Go runtime soft limit for a scanner container: 85% of
// its memory limit. Most scanners are Go programs; under a CPU cap their
// collector falls behind and the heap overshoots the hard limit (GoKart on a
// 3k-file service: OOM at 8 GiB, 6.7 GiB peak with this). Others ignore it.
func goMemoryLimit(limit string) string {
	bytes, err := MemoryBytes(limit)
	if err != nil || bytes <= 0 {
		return ""
	}
	return fmt.Sprintf("%dMiB", bytes*85/100/(1<<20))
}
