package remediate

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the WOLF_REMEDIATE_* environment configuration.
type Config struct {
	Enabled         bool
	DefaultProvider string
	DefaultModel    string
	MaxTurns        int
	MaxTurnsCeiling int
	SessionTimeout  time.Duration
	AllowYolo       bool
}

// LoadConfig reads configuration from the environment. Defaults are
// fail-closed: remediation is off and yolo mode is unavailable until an
// admin opts in.
func LoadConfig() Config {
	return Config{
		Enabled:         envBool("WOLF_REMEDIATE_ENABLED", false),
		DefaultProvider: os.Getenv("WOLF_REMEDIATE_DEFAULT_PROVIDER"),
		DefaultModel:    os.Getenv("WOLF_REMEDIATE_DEFAULT_MODEL"),
		MaxTurns:        envInt("WOLF_REMEDIATE_MAX_TURNS", 20),
		MaxTurnsCeiling: envInt("WOLF_REMEDIATE_MAX_TURNS_CEILING", 100),
		SessionTimeout:  envDuration("WOLF_REMEDIATE_SESSION_TIMEOUT", 30*time.Minute),
		AllowYolo:       envBool("WOLF_REMEDIATE_ALLOW_YOLO", false),
	}
}

// ClampTurns bounds a requested budget to the admin ceiling.
func (c Config) ClampTurns(requested int) int {
	if requested <= 0 {
		return c.MaxTurns
	}
	if c.MaxTurnsCeiling > 0 && requested > c.MaxTurnsCeiling {
		return c.MaxTurnsCeiling
	}
	return requested
}

func envBool(name string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	return strings.EqualFold(v, "true") || v == "1"
}

func envInt(name string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && n > 0 {
		return n
	}
	return def
}

func envDuration(name string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv(name))); err == nil && d > 0 {
		return d
	}
	return def
}
