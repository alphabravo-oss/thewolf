package models

import "time"

type ScannerVersionStatus string

const (
	ScannerVersionCurrent         ScannerVersionStatus = "current"
	ScannerVersionUpdateAvailable ScannerVersionStatus = "update_available"
	ScannerVersionUnknown         ScannerVersionStatus = "unknown"
	ScannerVersionCheckFailed     ScannerVersionStatus = "check_failed"
	ScannerVersionManifestDrift   ScannerVersionStatus = "manifest_drift"
	ScannerVersionOverridden      ScannerVersionStatus = "overridden"
)

type ScannerVersionCheck struct {
	ToolName        string               `json:"tool_name" db:"tool_name"`
	PinnedVersion   string               `json:"pinned_version" db:"pinned_version"`
	LatestVersion   string               `json:"latest_version,omitempty" db:"latest_version"`
	LatestReference string               `json:"latest_reference,omitempty" db:"latest_reference"`
	Status          ScannerVersionStatus `json:"status" db:"status"`
	CheckedAt       time.Time            `json:"checked_at" db:"checked_at"`
	Error           string               `json:"error,omitempty" db:"error"`
	SourceType      string               `json:"source_type" db:"source_type"`
	SourceURL       string               `json:"source_url,omitempty" db:"source_url"`
}
