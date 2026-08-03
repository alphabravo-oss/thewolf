package models

import "time"

const (
	// Durable tool output is a convenience for live/replay UI. Complete logs
	// remain in scan artifacts, so bounding this stream does not discard the
	// authoritative scanner output.
	MaxDurableToolOutputEventsPerScan = 2000
	MaxDurableScanEventDataBytes      = 4096

	ScanEventTypeToolOutput          = "tool_output"
	ScanEventTypeToolOutputTruncated = "tool_output_truncated"
	ScanEventTypeToolOutputDropped   = "tool_output_dropped"
)

// ScanEvent is an append-only, replayable scan progress event.
type ScanEvent struct {
	ID         string    `json:"id" db:"id"`
	ScanID     string    `json:"scan_id" db:"scan_id"`
	Sequence   int64     `json:"sequence" db:"sequence"`
	EventType  string    `json:"event_type" db:"event_type"`
	DataJSON   string    `json:"data" db:"data_json"`
	LeaseToken string    `json:"-" db:"-"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
}

// ScanWorker is the latest heartbeat/capability record for one worker process.
type ScanWorker struct {
	ID               string    `json:"id" db:"id"`
	Backend          string    `json:"backend" db:"backend"`
	Status           string    `json:"status" db:"status"`
	Capacity         int       `json:"capacity" db:"capacity"`
	ActiveScans      int       `json:"active_scans" db:"active_scans"`
	Version          string    `json:"version,omitempty" db:"version"`
	CapabilitiesJSON string    `json:"capabilities" db:"capabilities_json"`
	HeartbeatAt      time.Time `json:"heartbeat_at" db:"heartbeat_at"`
	StartedAt        time.Time `json:"started_at" db:"started_at"`
	UpdatedAt        time.Time `json:"updated_at" db:"updated_at"`
}
