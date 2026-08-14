package models

import "time"

const (
	FixerConsoleQueued    = "queued"
	FixerConsoleClaimed   = "claimed"
	FixerConsoleRunning   = "running"
	FixerConsoleExited    = "exited"
	FixerConsoleCancelled = "cancelled"

	FixerConsoleLogin   = "login"
	FixerConsoleShell   = "shell"
	FixerConsoleInstall = "install"
)

// FixerConsole is a short-lived session on the fixer worker: OAuth login
// or an operator shell. Output is a log artifact; stdin is queued in DB.
type FixerConsole struct {
	ID          string     `json:"id" db:"id"`
	UserID      string     `json:"user_id" db:"user_id"`
	Kind        string     `json:"kind" db:"kind"`
	Engine      string     `json:"engine" db:"engine"`
	Status      string     `json:"status" db:"status"`
	ClaimedBy   string     `json:"claimed_by,omitempty" db:"claimed_by"`
	LastURL     string     `json:"last_url,omitempty" db:"last_url"`
	Error       string     `json:"error,omitempty" db:"error"`
	ClaimedAt   *time.Time `json:"claimed_at,omitempty" db:"claimed_at"`
	StartedAt   *time.Time `json:"started_at,omitempty" db:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty" db:"finished_at"`
	HeartbeatAt *time.Time `json:"heartbeat_at,omitempty" db:"heartbeat_at"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at" db:"updated_at"`
}

// FixerConsoleActive reports whether a worker should still hold the PTY.
func FixerConsoleActive(status string) bool {
	switch status {
	case FixerConsoleQueued, FixerConsoleClaimed, FixerConsoleRunning:
		return true
	default:
		return false
	}
}
