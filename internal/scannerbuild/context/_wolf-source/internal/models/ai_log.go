package models

import "time"

// AILog represents a single AI provider call made during a scan assessment.
type AILog struct {
	ID             string    `json:"id" db:"id"`
	ScanID         string    `json:"scan_id" db:"scan_id"`
	Provider       string    `json:"provider" db:"provider"`
	Model          string    `json:"model" db:"model"`
	Phase          string    `json:"phase" db:"phase"`
	ToolName       string    `json:"tool_name" db:"tool_name"`
	Prompt         string    `json:"prompt" db:"prompt"`
	Response       string    `json:"response" db:"response"`
	Error          string    `json:"error" db:"error"`
	PromptTokens   int       `json:"prompt_tokens" db:"prompt_tokens"`
	ResponseTokens int       `json:"response_tokens" db:"response_tokens"`
	DurationMs     int64     `json:"duration_ms" db:"duration_ms"`
	CostUSD        float64   `json:"cost_usd" db:"cost_usd"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}
