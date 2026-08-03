package scannerreleaseworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

const defaultMaxExecutorOutput = 4 << 20

// CommandExecutor implements a shell-free JSON protocol for customer-managed
// BuildKit, Kubernetes Job, or local/offline backends. Request is written to
// stdin and exactly one StepResult JSON object is read from stdout.
type CommandExecutor struct {
	Path           string
	Args           []string
	Environment    []string
	MaxOutputBytes int64
}

func (e CommandExecutor) Execute(ctx context.Context, request StepRequest) (StepResult, error) {
	if e.Path == "" {
		return StepResult{}, errors.New("scanner release executor path is required")
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return StepResult{}, fmt.Errorf("encode executor request: %w", err)
	}
	limit := e.MaxOutputBytes
	if limit <= 0 {
		limit = defaultMaxExecutorOutput
	}
	command := exec.CommandContext(ctx, e.Path, e.Args...)
	command.Env = append([]string(nil), e.Environment...)
	command.Dir = request.Workspace
	command.Stdin = bytes.NewReader(payload)
	var stdout bytes.Buffer
	var stderr limitedBuffer
	stderr.limit = maxEvidenceText
	command.Stdout = &limitedWriter{writer: &stdout, remaining: limit + 1}
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return StepResult{}, fmt.Errorf("executor failed: %w: %s", err, redactText(stderr.String()))
	}
	if int64(stdout.Len()) > limit {
		return StepResult{}, fmt.Errorf("executor response exceeds %d bytes", limit)
	}
	var result StepResult
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return StepResult{}, fmt.Errorf("decode executor response: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return StepResult{}, err
	}
	return result, nil
}

type limitedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *limitedWriter) Write(payload []byte) (int, error) {
	original := len(payload)
	if w.remaining <= 0 {
		return original, nil
	}
	if int64(len(payload)) > w.remaining {
		payload = payload[:w.remaining]
	}
	_, err := w.writer.Write(payload)
	w.remaining -= int64(len(payload))
	return original, err
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(payload []byte) (int, error) {
	original := len(payload)
	if b.Len() >= b.limit {
		return original, nil
	}
	remaining := b.limit - b.Len()
	if len(payload) > remaining {
		payload = payload[:remaining]
	}
	_, err := b.Buffer.Write(payload)
	return original, err
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("executor returned more than one JSON value")
	}
	return fmt.Errorf("decode trailing executor output: %w", err)
}
