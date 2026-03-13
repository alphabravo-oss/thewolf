package models

import "time"

type AIPromptTemplate struct {
	ID         string    `json:"id" db:"id"`
	Scope      string    `json:"scope" db:"scope"`
	ScopeID    string    `json:"scope_id" db:"scope_id"`
	PromptType string    `json:"prompt_type" db:"prompt_type"`
	Section    string    `json:"section" db:"section"`
	Content    string    `json:"content" db:"content"`
	UpdatedAt  time.Time `json:"updated_at" db:"updated_at"`
}
