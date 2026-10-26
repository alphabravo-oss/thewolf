package models

// DASTTarget is repository configuration. Credentials are references only.
// Revision fences approval against concurrent edits to the reviewed target.
type DASTTarget struct {
	RepoID         string `json:"repo_id" db:"repo_id"`
	URL            string `json:"url" db:"url"`
	Mode           string `json:"mode" db:"mode"`
	APISpecURL     string `json:"api_spec_url" db:"api_spec_url"`
	APIFormat      string `json:"api_format" db:"api_format"`
	AuthSecretID   string `json:"auth_secret_id" db:"auth_secret_id"`
	Revision       int64  `json:"revision" db:"revision"`
	ApprovedBy     string `json:"approved_by" db:"approved_by"`
	AllowPrivate   bool   `json:"allow_private" db:"allow_private"`
	ActiveApproved bool   `json:"active_approved" db:"active_approved"`
}

func (t *DASTTarget) Approved() bool {
	return t != nil && t.URL != "" && t.ApprovedBy != "" && (t.Mode != "full" || t.ActiveApproved)
}

// DASTExecution is resolved immediately before execution, never serialized in
// scan requests, logs, or the database.
type DASTExecution struct {
	Target  DASTTarget
	Headers map[string]string
}
