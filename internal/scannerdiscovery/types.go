// Package scannerdiscovery defines and executes persistence-neutral scanner
// update discovery. It deliberately does not know about API handlers, queues, or
// database rows; callers can persist Run and ItemResult using their own
// transaction and event model.
package scannerdiscovery

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

const SchemaVersion = "wolf.scanners.discovery/v1"

type ComponentKind string

const (
	ComponentTool          ComponentKind = "tool"
	ComponentUpstreamImage ComponentKind = "upstream_image"
	ComponentBaseImage     ComponentKind = "base_image"
	ComponentToolchain     ComponentKind = "toolchain"
)

type Status string

const (
	StatusCurrent     Status = "current"
	StatusUpdate      Status = "update_available"
	StatusUnreachable Status = "source_unreachable"
	StatusUnsupported Status = "unsupported"
	StatusHeld        Status = "held"
	StatusYanked      Status = "yanked"
	StatusUnknown     Status = "unknown"
)

func (s Status) Covered() bool {
	switch s {
	case StatusCurrent, StatusUpdate, StatusHeld, StatusYanked:
		return true
	default:
		return false
	}
}

type RunState string

const (
	RunCompleted RunState = "completed"
	RunPartial   RunState = "partial"
	RunFailed    RunState = "failed"
	RunCancelled RunState = "cancelled"
)

type ScopeMode string

const (
	ScopeComplete ScopeMode = "complete"
	ScopeSelected ScopeMode = "selected"
)

type ComponentID struct {
	Kind ComponentKind `json:"kind"`
	Name string        `json:"name"`
}

func (id ComponentID) String() string {
	return string(id.Kind) + ":" + id.Name
}

// Scope supports an entire definition or a selected set. Tool names include the
// tool update source and its upstream image dependency, when one exists.
// Components selects exact base-image/toolchain/image records.
type Scope struct {
	Mode       ScopeMode     `json:"mode"`
	Tools      []string      `json:"tools,omitempty"`
	Components []ComponentID `json:"components,omitempty"`
}

func CompleteScope() Scope {
	return Scope{Mode: ScopeComplete}
}

func SelectedTools(names ...string) Scope {
	return Scope{Mode: ScopeSelected, Tools: append([]string(nil), names...)}
}

type Source struct {
	Type      string `json:"type"`
	Host      string `json:"host,omitempty"`
	URL       string `json:"url,omitempty"`
	Reference string `json:"reference,omitempty"`
}

// Item is a stable discovery input derived from the manifest and lock. The
// manifest pointer is excluded from persistence and is only used by adapters.
type Item struct {
	ID             ComponentID       `json:"id"`
	CurrentValue   string            `json:"current_value,omitempty"`
	CurrentDigest  string            `json:"current_digest,omitempty"`
	Source         Source            `json:"source"`
	Platforms      []string          `json:"platforms,omitempty"`
	DefinitionRisk Risk              `json:"definition_risk,omitempty"`
	ToolDefinition *manifest.Tool    `json:"-"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type Evidence struct {
	SourceURL      string            `json:"source_url,omitempty"`
	Reference      string            `json:"reference,omitempty"`
	ResponseDigest string            `json:"response_digest,omitempty"`
	ETag           string            `json:"etag,omitempty"`
	LastModified   string            `json:"last_modified,omitempty"`
	Attributes     map[string]string `json:"attributes,omitempty"`
	Detail         string            `json:"detail,omitempty"`
}

type ChangeFacts struct {
	RebuildOnly        bool `json:"rebuild_only,omitempty"`
	ParserChanged      bool `json:"parser_changed,omitempty"`
	RulesChanged       bool `json:"rules_changed,omitempty"`
	LicenseChanged     bool `json:"license_changed,omitempty"`
	PlatformLost       bool `json:"platform_lost,omitempty"`
	SignatureChanged   bool `json:"signature_changed,omitempty"`
	SourceChanged      bool `json:"source_changed,omitempty"`
	PrivilegeIncreased bool `json:"privilege_increased,omitempty"`
	ActivelyExploited  bool `json:"actively_exploited,omitempty"`
	ArtifactRevoked    bool `json:"artifact_revoked,omitempty"`
}

// Observation is returned by a resolver. Status must be a semantic source
// result, not an HTTP transport status. Error paths are returned as errors.
type Observation struct {
	Status          Status      `json:"status"`
	AvailableValue  string      `json:"available_value,omitempty"`
	AvailableDigest string      `json:"available_digest,omitempty"`
	Evidence        Evidence    `json:"evidence,omitempty"`
	Facts           ChangeFacts `json:"facts,omitempty"`
}

// Resolver is intentionally small so managed, customer, cached, and offline
// sources can all participate without depending on persistence.
type Resolver interface {
	Name() string
	Supports(Item) bool
	Resolve(context.Context, Item) (Observation, error)
}

// ResultSink is an optional durability hook. The engine invokes it once for
// every persistence-safe item result, including cancellation and unsupported
// results. Implementations should be idempotent by run/item identity supplied
// by their surrounding service.
type ResultSink interface {
	StoreDiscoveryResult(context.Context, ItemResult) error
}

type HoldDecision struct {
	Held        bool      `json:"held"`
	Reason      string    `json:"reason,omitempty"`
	ReviewAfter time.Time `json:"review_after,omitempty"`
}

type HoldPolicy interface {
	Evaluate(context.Context, Item) HoldDecision
}

type ErrorClass string

const (
	ErrorTransientNetwork ErrorClass = "transient_network"
	ErrorRateLimited      ErrorClass = "rate_limited"
	ErrorUnavailable      ErrorClass = "unavailable"
	ErrorAuthentication   ErrorClass = "authentication"
	ErrorNotFound         ErrorClass = "not_found"
	ErrorInvalidResponse  ErrorClass = "invalid_response"
	ErrorUnsupported      ErrorClass = "unsupported"
	ErrorCancelled        ErrorClass = "cancelled"
	ErrorUnknown          ErrorClass = "unknown"
)

type RetryDecision struct {
	Class      ErrorClass
	Retry      bool
	RetryAfter time.Duration
}

type RetryClassifier interface {
	Classify(error) RetryDecision
}

type Backoff interface {
	Delay(attempt int, decision RetryDecision) time.Duration
}

type Sleeper interface {
	Sleep(context.Context, time.Duration) error
}

// ClassifiedError lets resolvers preserve transport semantics without exposing
// transport-specific error types to the engine.
type ClassifiedError struct {
	Class      ErrorClass
	RetryAfter time.Duration
	Err        error
	Evidence   Evidence
}

func (e *ClassifiedError) Error() string {
	if e == nil || e.Err == nil {
		return string(e.Class)
	}
	return e.Err.Error()
}

func (e *ClassifiedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type ItemResult struct {
	Item            Item       `json:"item"`
	Status          Status     `json:"status"`
	AvailableValue  string     `json:"available_value,omitempty"`
	AvailableDigest string     `json:"available_digest,omitempty"`
	Risk            RiskResult `json:"risk"`
	Evidence        Evidence   `json:"evidence,omitempty"`
	ErrorClass      ErrorClass `json:"error_class,omitempty"`
	Error           string     `json:"error,omitempty"`
	Resolver        string     `json:"resolver,omitempty"`
	Attempts        int        `json:"attempts"`
	RetryAt         *time.Time `json:"retry_at,omitempty"`
	CheckedAt       time.Time  `json:"checked_at"`
}

type Counts struct {
	Total           int `json:"total"`
	Covered         int `json:"covered"`
	Current         int `json:"current"`
	UpdateAvailable int `json:"update_available"`
	Unreachable     int `json:"source_unreachable"`
	Unsupported     int `json:"unsupported"`
	Held            int `json:"held"`
	Yanked          int `json:"yanked"`
	Unknown         int `json:"unknown"`
}

type Run struct {
	SchemaVersion    string       `json:"schema_version"`
	DefinitionDigest string       `json:"definition_digest"`
	LockDigest       string       `json:"lock_digest"`
	Scope            Scope        `json:"scope"`
	State            RunState     `json:"state"`
	Coverage         float64      `json:"coverage"`
	Counts           Counts       `json:"counts"`
	Items            []ItemResult `json:"items"`
	StartedAt        time.Time    `json:"started_at"`
	CompletedAt      time.Time    `json:"completed_at"`
}

func (r *Run) finalize(ctxErr error) {
	r.Counts = Counts{Total: len(r.Items)}
	for _, item := range r.Items {
		if item.Status.Covered() {
			r.Counts.Covered++
		}
		switch item.Status {
		case StatusCurrent:
			r.Counts.Current++
		case StatusUpdate:
			r.Counts.UpdateAvailable++
		case StatusUnreachable:
			r.Counts.Unreachable++
		case StatusUnsupported:
			r.Counts.Unsupported++
		case StatusHeld:
			r.Counts.Held++
		case StatusYanked:
			r.Counts.Yanked++
		default:
			r.Counts.Unknown++
		}
	}
	if r.Counts.Total > 0 {
		r.Coverage = float64(r.Counts.Covered) / float64(r.Counts.Total)
	}
	switch {
	case ctxErr != nil:
		r.State = RunCancelled
	case r.Counts.Total > 0 && r.Counts.Covered == 0:
		r.State = RunFailed
	case r.Counts.Covered < r.Counts.Total:
		r.State = RunPartial
	default:
		r.State = RunCompleted
	}
	sort.Slice(r.Items, func(i, j int) bool {
		return r.Items[i].Item.ID.String() < r.Items[j].Item.ID.String()
	})
}

func validateStatus(status Status) error {
	switch status {
	case StatusCurrent, StatusUpdate, StatusUnreachable, StatusUnsupported,
		StatusHeld, StatusYanked, StatusUnknown:
		return nil
	default:
		return fmt.Errorf("invalid discovery status %q", status)
	}
}
