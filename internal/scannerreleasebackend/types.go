// Package scannerreleasebackend provides policy-bound execution adapters for
// durable scanner release steps. It deliberately sits behind
// scannerreleaseworker.Executor so the existing shell-free command protocol
// remains a supported sovereign deployment boundary.
package scannerreleasebackend

import (
	"context"
	"errors"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
)

var (
	ErrUnsupportedStep = errors.New("scanner release backend does not support step")
	ErrResourcePolicy  = errors.New("scanner release backend resource policy violation")
	ErrBinding         = errors.New("scanner release backend immutable binding mismatch")
	ErrAmbiguousResult = errors.New("scanner release backend external result is ambiguous")
)

type Resources struct {
	CPUMilli       int64         `json:"cpu_milli"`
	MemoryBytes    int64         `json:"memory_bytes"`
	DiskBytes      int64         `json:"ephemeral_disk_bytes"`
	Timeout        time.Duration `json:"timeout"`
	MaxConcurrency int           `json:"max_concurrency"`
}

type Binding struct {
	DefinitionCommit string `json:"definition_commit"`
	LockDigest       string `json:"lock_digest"`
	PolicyID         string `json:"policy_id"`
	PolicyRevision   int64  `json:"policy_revision"`
}

type Action struct {
	Name     string                   `json:"name"`
	Kind     scannerpipeline.StepKind `json:"kind"`
	Image    string                   `json:"image,omitempty"`
	Platform string                   `json:"platform,omitempty"`
}

type Invocation struct {
	OperationID string                           `json:"operation_id"`
	Request     scannerreleaseworker.StepRequest `json:"request"`
	Action      Action                           `json:"action"`
	Resources   Resources                        `json:"resources"`
	Binding     Binding                          `json:"binding"`
}

type BackendResult struct {
	Result              scannerreleaseworker.StepResult `json:"result"`
	Binding             Binding                         `json:"binding"`
	ExternalOperationID string                          `json:"external_operation_id,omitempty"`
	Log                 string                          `json:"log,omitempty"`
}

type Capabilities struct {
	Name                 string                     `json:"name"`
	Actions              []string                   `json:"actions"`
	Kinds                []scannerpipeline.StepKind `json:"kinds"`
	Platforms            []string                   `json:"platforms,omitempty"`
	MaxCPU               int64                      `json:"max_cpu_milli"`
	MaxMemory            int64                      `json:"max_memory_bytes"`
	MaxDisk              int64                      `json:"max_ephemeral_disk_bytes"`
	MaxTimeout           time.Duration              `json:"max_timeout"`
	MaxConcurrency       int                        `json:"max_concurrency"`
	EnforcesCPU          bool                       `json:"enforces_cpu"`
	EnforcesMemory       bool                       `json:"enforces_memory"`
	EnforcesDisk         bool                       `json:"enforces_ephemeral_disk"`
	EnforcesTimeout      bool                       `json:"enforces_timeout"`
	EnforcesCancellation bool                       `json:"enforces_cancellation"`
	Idempotent           bool                       `json:"idempotent"`
	ExternalIdempotency  bool                       `json:"external_idempotency"`
}

type Backend interface {
	Name() string
	Capabilities(context.Context) (Capabilities, error)
	Execute(context.Context, Invocation) (BackendResult, error)
}

func scannerreleaseworkerResult(
	uri, digest string,
	summary map[string]any,
) scannerreleaseworker.StepResult {
	return scannerreleaseworker.StepResult{
		OutputURI: uri, OutputDigest: digest, Summary: summary,
	}
}
