package scannerruntime

import (
	"context"
	"time"
)

type Mount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

// Invocation is runtime-neutral scanner intent. Docker and Kubernetes adapt
// this shape to their native primitives.
type Invocation struct {
	Image        string            `json:"image"`
	Command      []string          `json:"command,omitempty"`
	Args         []string          `json:"args,omitempty"`
	WorkingDir   string            `json:"working_dir"`
	Environment  map[string]string `json:"environment,omitempty"`
	Mounts       []Mount           `json:"mounts,omitempty"`
	Stdin        string            `json:"stdin,omitempty"`
	Memory       string            `json:"memory,omitempty"`
	CPUs         string            `json:"cpus,omitempty"`
	NetworkClass string            `json:"network_class,omitempty"`
	Timeout      time.Duration     `json:"timeout,omitempty"`
	ScanID       string            `json:"scan_id,omitempty"`
	UserID       string            `json:"user_id,omitempty"`
	LeaseToken   string            `json:"lease_token,omitempty"`
	Attempt      int               `json:"attempt,omitempty"`
	ToolName     string            `json:"tool_name"`
}

type identityKey struct{}

type Identity struct {
	ScanID       string
	ToolName     string
	UserID       string
	LeaseToken   string
	Attempt      int
	NetworkClass string
}

func WithIdentity(ctx context.Context, scanID, toolName string) context.Context {
	return context.WithValue(ctx, identityKey{}, Identity{ScanID: scanID, ToolName: toolName})
}

func WithExecutionIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, identity)
}

func IdentityFromContext(ctx context.Context) Identity {
	value, _ := ctx.Value(identityKey{}).(Identity)
	return value
}
