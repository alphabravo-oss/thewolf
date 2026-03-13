package models

import "time"

// RepoMap represents cached repository structural and semantic data.
type RepoMap struct {
	ID             string    `json:"id" db:"id"`
	RepoID         string    `json:"repo_id" db:"repo_id"`
	Branch         string    `json:"branch" db:"branch"`
	StructuralData string    `json:"structural_data" db:"structural_data"`
	SemanticData   string    `json:"semantic_data" db:"semantic_data"`
	FileHashes     string    `json:"file_hashes" db:"file_hashes"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}
