package scannersigning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
)

const (
	maxAdapterInput  = 1 << 20
	maxAdapterOutput = 1 << 20
	maxAdapterLog    = 64 << 10
)

var (
	secretPattern = regexp.MustCompile(`(?i)(token|password|secret|api[_-]?key|authorization)\s*[:=]\s*[^\s,;]+`)
	bearerPattern = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`)
)

type Adapter interface {
	Sign(context.Context, Request) (Result, error)
}

// CommandAdapter invokes one administrator-installed binary directly. It
// never invokes a shell, inherits no environment, and places no request or
// credential reference in argv.
type CommandAdapter struct {
	Path           string
	Args           []string
	Environment    []string
	MaxOutputBytes int
}

func (a CommandAdapter) Sign(ctx context.Context, request Request) (Result, error) {
	if strings.TrimSpace(a.Path) == "" {
		return Result{}, errors.New("signer adapter path is required")
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return Result{}, err
	}
	if len(payload) > maxAdapterInput {
		return Result{}, errors.New("signer adapter request exceeds size limit")
	}
	command := exec.CommandContext(ctx, a.Path, a.Args...) // #nosec G204 -- deployment-owned executable and static args; no request data enters argv.
	command.Env = append([]string(nil), a.Environment...)
	command.Stdin = bytes.NewReader(payload)
	limit := a.MaxOutputBytes
	if limit <= 0 || limit > maxAdapterOutput {
		limit = maxAdapterOutput
	}
	var stdout, stderr boundedWriter
	stdout.maximum = limit
	stderr.maximum = maxAdapterLog
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return Result{}, fmt.Errorf(
			"signer adapter failed: %w: %s",
			err, redact(stderr.String(), 4096),
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	var result Result
	if err := decoder.Decode(&result); err != nil {
		return Result{}, fmt.Errorf("decode signer adapter result: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Result{}, errors.New("signer adapter returned trailing JSON")
	}
	return result, nil
}

type boundedWriter struct {
	value   bytes.Buffer
	maximum int
}

func (w *boundedWriter) Write(value []byte) (int, error) {
	original := len(value)
	if w.value.Len() >= w.maximum {
		return original, nil
	}
	remaining := w.maximum - w.value.Len()
	if len(value) > remaining {
		value = value[:remaining]
	}
	_, err := w.value.Write(value)
	return original, err
}

func (w *boundedWriter) Bytes() []byte  { return append([]byte(nil), w.value.Bytes()...) }
func (w *boundedWriter) String() string { return w.value.String() }

func redact(value string, maximum int) string {
	value = secretPattern.ReplaceAllString(value, "$1=[REDACTED]")
	value = bearerPattern.ReplaceAllString(value, "$1[REDACTED]")
	if len(value) > maximum {
		return value[:maximum] + "…"
	}
	return value
}
