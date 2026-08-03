// Package wolflog provides structured logging for the Wolf application using
// zerolog. It configures a shared logger that all packages should use instead
// of fmt.Printf or the default log package.
package wolflog

import (
	"context"
	"io"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

var (
	mu     sync.RWMutex
	logger zerolog.Logger
)

func init() {
	// Default: pretty console output at Info level for development.
	// Production callers should call Init() with JSON output.
	zerolog.TimeFieldFormat = time.RFC3339
	logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.Kitchen}).
		With().
		Timestamp().
		Logger().
		Level(zerolog.InfoLevel)
}

// Init re-initialises the global logger. Call this early in main() if you
// want non-default settings (e.g., JSON output for production).
func Init(w io.Writer, level zerolog.Level, json bool) {
	mu.Lock()
	defer mu.Unlock()

	var output io.Writer
	if json {
		output = w
	} else {
		output = zerolog.ConsoleWriter{Out: w, TimeFormat: time.Kitchen}
	}

	logger = zerolog.New(output).
		With().
		Timestamp().
		Logger().
		Level(level)
}

// SetLevel changes the log level dynamically (useful for --verbose flags).
func SetLevel(level zerolog.Level) {
	mu.Lock()
	defer mu.Unlock()
	logger = logger.Level(level)
}

// L returns the global logger. Packages should call wolflog.L() to obtain a
// logger and then create sub-loggers with .With() for component-specific
// context.
func L() *zerolog.Logger {
	mu.RLock()
	defer mu.RUnlock()
	return &logger
}

// With returns a sub-logger context with the given component name attached.
func Component(name string) zerolog.Logger {
	return L().With().Str("component", name).Logger()
}

// Debug logs at Debug level.
func Debug() *zerolog.Event { return L().Debug() }

// Info logs at Info level.
func Info() *zerolog.Event { return L().Info() }

// Warn logs at Warn level.
func Warn() *zerolog.Event { return L().Warn() }

// Error logs at Error level.
func Error() *zerolog.Event { return L().Error() }

// Fatal logs at Fatal level and exits.
func Fatal() *zerolog.Event { return L().Fatal() }

// FromContext extracts a logger from the context. Falls back to the global
// logger if none is found.
func FromContext(ctx context.Context) *zerolog.Logger {
	l := zerolog.Ctx(ctx)
	if l.GetLevel() == zerolog.Disabled {
		return L()
	}
	return l
}

// WithContext stores a logger in the context.
func WithContext(ctx context.Context) context.Context {
	return L().WithContext(ctx)
}
