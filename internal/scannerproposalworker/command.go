package scannerproposalworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"

	"github.com/alphabravocompany/thewolf/internal/scannerdiscovery"
)

const defaultMaximumOutput = int64(4 << 20)

// CommandProposer invokes a shell-free JSON protocol. Candidate selection is
// passed on stdin and never interpolated into an argument or environment
// variable. The child process must write exactly one Result JSON object.
type CommandProposer struct {
	Path           string
	Args           []string
	Environment    []string
	MaxOutputBytes int64
}

func (p CommandProposer) Propose(ctx context.Context, request Request) (Result, error) {
	if p.Path == "" {
		return Result{}, errors.New("scanner proposal executor path is required")
	}
	input, err := json.Marshal(request)
	if err != nil {
		return Result{}, fmt.Errorf("encode scanner proposal request: %w", err)
	}
	limit := p.MaxOutputBytes
	if limit <= 0 {
		limit = defaultMaximumOutput
	}
	command := exec.CommandContext(ctx, p.Path, p.Args...) // #nosec G204 -- trusted administrator configuration; no shell.
	command.Env = append([]string(nil), p.Environment...)
	command.Stdin = bytes.NewReader(input)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &limitedWriter{Writer: &stdout, Remaining: limit + 1}
	command.Stderr = &limitedWriter{Writer: &stderr, Remaining: 64 << 10}
	if err := command.Run(); err != nil {
		return Result{}, fmt.Errorf(
			"scanner proposal executor failed: %w: %s",
			err, scannerdiscovery.RedactText(stderr.String()),
		)
	}
	if int64(stdout.Len()) > limit {
		return Result{}, fmt.Errorf("scanner proposal response exceeds %d bytes", limit)
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	var result Result
	if err := decoder.Decode(&result); err != nil {
		return Result{}, fmt.Errorf("decode scanner proposal response: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return Result{}, err
	}
	return result, nil
}

type limitedWriter struct {
	Writer    io.Writer
	Remaining int64
}

func (w *limitedWriter) Write(value []byte) (int, error) {
	if w.Remaining <= 0 {
		return len(value), nil
	}
	write := value
	if int64(len(write)) > w.Remaining {
		write = write[:w.Remaining]
	}
	_, err := w.Writer.Write(write)
	w.Remaining -= int64(len(write))
	return len(value), err
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("scanner proposal executor returned multiple JSON values")
		}
		return fmt.Errorf("decode scanner proposal trailing data: %w", err)
	}
	return nil
}
