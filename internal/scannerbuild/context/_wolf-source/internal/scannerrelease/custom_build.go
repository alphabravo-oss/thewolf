package scannerrelease

import "time"

type CustomBuildState string

const (
	CustomBuildQueued    CustomBuildState = "queued"
	CustomBuildClaimed   CustomBuildState = "claimed"
	CustomBuildRunning   CustomBuildState = "running"
	CustomBuildCompleted CustomBuildState = "completed"
	CustomBuildPartial   CustomBuildState = "partial"
	CustomBuildFailed    CustomBuildState = "failed"
	CustomBuildCancelled CustomBuildState = "cancelled"
)

const (
	CustomBuildMaxLogLines     = 4000
	CustomBuildMaxLogBytes     = 4 << 20
	CustomBuildMaxLogLineBytes = 8192
	CustomBuildTerminalEventID = CustomBuildMaxLogLines + 1
)

type CustomBuildVariantState string

const (
	CustomBuildVariantQueued    CustomBuildVariantState = "queued"
	CustomBuildVariantRunning   CustomBuildVariantState = "running"
	CustomBuildVariantCompleted CustomBuildVariantState = "completed"
	CustomBuildVariantFailed    CustomBuildVariantState = "failed"
	CustomBuildVariantCancelled CustomBuildVariantState = "cancelled"
)

type CustomBuild struct {
	ID                string           `db:"id" json:"id"`
	UserID            string           `db:"user_id" json:"user_id"`
	VariantsJSON      string           `db:"variants_json" json:"variants"`
	Push              bool             `db:"push" json:"push"`
	PlatformsJSON     string           `db:"platforms_json" json:"platforms"`
	Namespace         string           `db:"namespace" json:"namespace"`
	ReservedVersion   string           `db:"reserved_version" json:"reserved_version"`
	PublishVersion    *string          `db:"publish_version" json:"-"`
	SecretReference   string           `db:"secret_reference" json:"-"`
	State             CustomBuildState `db:"state" json:"state"`
	Actor             string           `db:"actor" json:"actor"`
	Reason            string           `db:"reason" json:"reason"`
	IdempotencyKey    string           `db:"idempotency_key" json:"idempotency_key"`
	RequestDigest     string           `db:"request_digest" json:"request_digest"`
	WorkerID          string           `db:"worker_id" json:"worker_id,omitempty"`
	LeaseToken        string           `db:"lease_token" json:"-"`
	LeaseExpiresAt    *time.Time       `db:"lease_expires_at" json:"lease_expires_at,omitempty"`
	HeartbeatAt       *time.Time       `db:"heartbeat_at" json:"heartbeat_at,omitempty"`
	Attempt           int              `db:"attempt" json:"attempt"`
	MaxAttempts       int              `db:"max_attempts" json:"max_attempts"`
	AvailableAt       time.Time        `db:"available_at" json:"available_at"`
	CancelRequestedAt *time.Time       `db:"cancel_requested_at" json:"cancel_requested_at,omitempty"`
	ErrorClass        string           `db:"error_class" json:"error_class,omitempty"`
	ErrorDetail       string           `db:"error_detail" json:"error_detail,omitempty"`
	SummaryJSON       string           `db:"summary_json" json:"summary"`
	Version           int64            `db:"version" json:"version"`
	StartedAt         *time.Time       `db:"started_at" json:"started_at,omitempty"`
	CompletedAt       *time.Time       `db:"completed_at" json:"completed_at,omitempty"`
	CreatedAt         time.Time        `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time        `db:"updated_at" json:"updated_at"`
}

type CustomBuildVariant struct {
	ID            string                  `db:"id" json:"id"`
	BuildID       string                  `db:"build_id" json:"build_id"`
	Variant       string                  `db:"variant" json:"variant"`
	Ordinal       int                     `db:"ordinal" json:"ordinal"`
	State         CustomBuildVariantState `db:"state" json:"state"`
	RefsJSON      string                  `db:"refs_json" json:"refs"`
	Digest        string                  `db:"digest" json:"digest,omitempty"`
	LoadedLocally bool                    `db:"loaded_locally" json:"loaded_locally"`
	Pushed        bool                    `db:"pushed" json:"pushed"`
	ErrorClass    string                  `db:"error_class" json:"error_class,omitempty"`
	ErrorDetail   string                  `db:"error_detail" json:"error_detail,omitempty"`
	Version       int64                   `db:"version" json:"version"`
	StartedAt     *time.Time              `db:"started_at" json:"started_at,omitempty"`
	CompletedAt   *time.Time              `db:"completed_at" json:"completed_at,omitempty"`
	CreatedAt     time.Time               `db:"created_at" json:"created_at"`
	UpdatedAt     time.Time               `db:"updated_at" json:"updated_at"`
}

type CustomBuildLog struct {
	BuildID   string    `db:"build_id" json:"build_id"`
	Sequence  int64     `db:"sequence" json:"sequence"`
	Variant   string    `db:"variant" json:"variant"`
	Line      string    `db:"line" json:"line"`
	Redacted  bool      `db:"redacted" json:"redacted"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

type CustomBuildInventory struct {
	Build    CustomBuild          `json:"build"`
	Variants []CustomBuildVariant `json:"variants"`
}

type CustomBuildCreateRequest struct {
	ID              string
	UserID          string
	Variants        []string
	Push            bool
	Platforms       []string
	Namespace       string
	SecretReference string
	Actor           string
	Reason          string
	IdempotencyKey  string
	MaxAttempts     int
}

type CustomBuildFilter struct {
	State  CustomBuildState
	UserID string
}

type CustomBuildPage struct {
	Items      []CustomBuild
	NextCursor string
}

type CustomBuildLeaseStatus struct {
	Current         bool             `json:"current"`
	CancelRequested bool             `json:"cancel_requested"`
	State           CustomBuildState `json:"state"`
	Version         int64            `json:"version"`
}

type CustomBuildVariantResult struct {
	Refs          []string
	Digest        string
	LoadedLocally bool
	Pushed        bool
	ErrorClass    string
	ErrorDetail   string
}
