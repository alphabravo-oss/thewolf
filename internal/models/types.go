package models

// Severity represents the severity level of a finding.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityInfo     Severity = "info"
)

// Category represents the type of analysis tool.
type Category string

const (
	CategorySAST      Category = "sast"
	CategorySCA       Category = "sca"
	CategorySecrets   Category = "secrets"
	CategoryQuality   Category = "quality"
	CategoryContainer Category = "container"
	CategoryDocs      Category = "docs"
	CategoryLicense   Category = "license"
	CategorySBOM      Category = "sbom"
	CategoryInfra     Category = "infra"
	CategoryDAST      Category = "dast"
)

// Status represents the status of a finding.
type Status string

const (
	StatusOpen          Status = "open"
	StatusFixed         Status = "fixed"
	StatusWontFix       Status = "wont_fix"
	StatusFalsePositive Status = "false_positive"
)

// ScanStatus represents the status of a scan.
type ScanStatus string

const (
	ScanStatusPending   ScanStatus = "pending"
	ScanStatusRunning   ScanStatus = "running"
	ScanStatusCompleted ScanStatus = "completed"
	ScanStatusFailed    ScanStatus = "failed"
	ScanStatusCancelled ScanStatus = "cancelled"
)

// FixStatus represents the status of a fix operation.
type FixStatus string

const (
	FixStatusPending   FixStatus = "pending"
	FixStatusRunning   FixStatus = "running"
	FixStatusCompleted FixStatus = "completed"
	FixStatusFailed    FixStatus = "failed"
	FixStatusCancelled FixStatus = "cancelled"
)

// FixItemStatus represents the status of an individual fix item.
type FixItemStatus string

const (
	FixItemStatusPending    FixItemStatus = "pending"
	FixItemStatusInProgress FixItemStatus = "in_progress"
	FixItemStatusFixed      FixItemStatus = "fixed"
	FixItemStatusFailed     FixItemStatus = "failed"
	FixItemStatusSkipped    FixItemStatus = "skipped"
)

// LoopStatus represents the status of a loop.
type LoopStatus string

const (
	LoopStatusRunning   LoopStatus = "running"
	LoopStatusPaused    LoopStatus = "paused"
	LoopStatusCompleted LoopStatus = "completed"
	LoopStatusStopped   LoopStatus = "stopped"
	LoopStatusFailed    LoopStatus = "failed"
)

// SourceType represents how a repo was added.
type SourceType string

const (
	SourceTypeLocal  SourceType = "local"
	SourceTypeGitHub SourceType = "github"
	SourceTypeGitLab SourceType = "gitlab"
	SourceTypeGit    SourceType = "git"
	SourceTypeSSH    SourceType = "ssh"
)

// Language represents a programming language.
type Language string

const (
	LangPython     Language = "python"
	LangJavaScript Language = "javascript"
	LangTypeScript Language = "typescript"
	LangGo         Language = "go"
	LangRust       Language = "rust"
	LangJava       Language = "java"
	LangRuby       Language = "ruby"
	LangPHP        Language = "php"
	LangC          Language = "c"
	LangCPP        Language = "cpp"
	LangShell      Language = "shell"
	LangSwift      Language = "swift"
	LangKotlin     Language = "kotlin"
	LangSQL        Language = "sql"
	LangObjC       Language = "objectivec"
)

// RescanStrategy defines the re-scan approach in a loop.
type RescanStrategy string

const (
	RescanFull     RescanStrategy = "full"
	RescanTargeted RescanStrategy = "targeted"
	RescanSmart    RescanStrategy = "smart"
)

// ValidationResult represents fix validation outcome.
type ValidationResult string

const (
	ValidationPass ValidationResult = "pass"
	ValidationFail ValidationResult = "fail"
)

// ArtifactType represents a scan artifact type.
type ArtifactType string

const (
	ArtifactSARIF    ArtifactType = "sarif"
	ArtifactJSON     ArtifactType = "json"
	ArtifactMarkdown ArtifactType = "markdown"
	ArtifactManifest ArtifactType = "manifest"
	ArtifactLog      ArtifactType = "log"
	ArtifactCoverage ArtifactType = "coverage"
)

// ScanConfig holds default scan settings for a collection.
type ScanConfig struct {
	DisabledTools   []string          `json:"disabled_tools,omitempty"`   // tools to exclude from auto-detected set
	BranchOverrides map[string]string `json:"branch_overrides,omitempty"` // repo_id → branch to scan
	AIEnabled       bool              `json:"ai_enabled,omitempty"`       // enable AI assessment post-scan
	AIEngine        string            `json:"ai_engine,omitempty"`        // e.g. "anthropic", "openai"
	AIModel         string            `json:"ai_model,omitempty"`         // e.g. "claude-sonnet-4-20250514"
	Concurrency     int               `json:"concurrency,omitempty"`
}

// KeyType represents a secret key type.
type KeyType string

const (
	KeyTypeGitHubToken  KeyType = "github_token"
	KeyTypeGitLabToken  KeyType = "gitlab_token"
	KeyTypeSSHPrivate   KeyType = "ssh_private_key"
	KeyTypeSSHPassword  KeyType = "ssh_password"
	KeyTypeAnthropicKey KeyType = "anthropic_key"
	KeyTypeOpenAIKey    KeyType = "openai_key"
	// KeyTypeDockerHubToken stores a DockerHub credential: the encrypted
	// value is the PAT, and KeyName holds the DockerHub username.
	KeyTypeDockerHubToken KeyType = "dockerhub_token"
	KeyTypeCustom         KeyType = "custom"
)
