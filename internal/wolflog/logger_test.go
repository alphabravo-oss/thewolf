package wolflog

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestInit_JSONMode(t *testing.T) {
	var buf bytes.Buffer
	Init(&buf, zerolog.InfoLevel, true)

	Info().Msg("test message")

	output := buf.String()
	if !strings.Contains(output, `"message":"test message"`) {
		t.Errorf("expected JSON output with message field, got %q", output)
	}
}

func TestInit_ConsoleMode(t *testing.T) {
	var buf bytes.Buffer
	Init(&buf, zerolog.InfoLevel, false)

	Info().Msg("console test")

	output := buf.String()
	if !strings.Contains(output, "console test") {
		t.Errorf("expected console output containing 'console test', got %q", output)
	}
}

func TestSetLevel(t *testing.T) {
	var buf bytes.Buffer
	Init(&buf, zerolog.WarnLevel, true)

	Info().Msg("should not appear")
	if buf.Len() > 0 {
		t.Error("info message should not appear at warn level")
	}

	SetLevel(zerolog.InfoLevel)
	Info().Msg("should appear")
	if buf.Len() == 0 {
		t.Error("info message should appear after SetLevel to info")
	}
}

func TestL_ReturnsLogger(t *testing.T) {
	l := L()
	if l == nil {
		t.Fatal("L() returned nil")
	}
}

func TestComponent(t *testing.T) {
	var buf bytes.Buffer
	Init(&buf, zerolog.InfoLevel, true)

	comp := Component("scanner")
	comp.Info().Msg("component test")

	output := buf.String()
	if !strings.Contains(output, `"component":"scanner"`) {
		t.Errorf("expected component field in output, got %q", output)
	}
}

func TestLogLevelFunctions(t *testing.T) {
	var buf bytes.Buffer
	Init(&buf, zerolog.DebugLevel, true)

	Debug().Msg("debug msg")
	if !strings.Contains(buf.String(), "debug msg") {
		t.Error("Debug() message not found")
	}
	buf.Reset()

	Info().Msg("info msg")
	if !strings.Contains(buf.String(), "info msg") {
		t.Error("Info() message not found")
	}
	buf.Reset()

	Warn().Msg("warn msg")
	if !strings.Contains(buf.String(), "warn msg") {
		t.Error("Warn() message not found")
	}
	buf.Reset()

	Error().Msg("error msg")
	if !strings.Contains(buf.String(), "error msg") {
		t.Error("Error() message not found")
	}
}

func TestFromContext_FallsBackToGlobal(t *testing.T) {
	l := FromContext(context.Background())
	if l == nil {
		t.Fatal("FromContext returned nil for empty context")
	}
}

func TestWithContext_RoundTrip(t *testing.T) {
	ctx := WithContext(context.Background())
	l := zerolog.Ctx(ctx)
	if l == nil {
		t.Fatal("WithContext should store a logger in context")
	}
}
