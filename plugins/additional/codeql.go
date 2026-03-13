package additional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// CodeQLPlugin runs CodeQL SAST analysis with SARIF output.
type CodeQLPlugin struct{}

func init() {
	plugin.Register(&CodeQLPlugin{})
}

func (p *CodeQLPlugin) Name() string               { return "codeql" }
func (p *CodeQLPlugin) Category() models.Category   { return models.CategorySAST }
func (p *CodeQLPlugin) Languages() []models.Language { return nil }

func (p *CodeQLPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("codeql")
	return err == nil
}

// codeqlLanguages maps file extensions to CodeQL language identifiers.
var codeqlLanguages = map[string]string{
	".go":    "go",
	".py":    "python",
	".js":    "javascript",
	".ts":    "javascript",
	".jsx":   "javascript",
	".tsx":   "javascript",
	".java":  "java",
	".kt":    "java",
	".cs":    "csharp",
	".c":     "cpp",
	".cpp":   "cpp",
	".cc":    "cpp",
	".h":     "cpp",
	".hpp":   "cpp",
	".rb":    "ruby",
	".swift": "swift",
}

// detectCodeQLLanguage walks the repo to find the dominant language.
func detectCodeQLLanguage(repoPath string) string {
	counts := make(map[string]int)
	_ = filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			base := filepath.Base(path)
			if info != nil && info.IsDir() && (base == "vendor" || base == "node_modules" || base == ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(info.Name())
		if lang, ok := codeqlLanguages[ext]; ok {
			counts[lang]++
		}
		return nil
	})

	best := ""
	bestCount := 0
	for lang, count := range counts {
		if count > bestCount {
			best = lang
			bestCount = count
		}
	}
	return best
}

func (p *CodeQLPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	timeout := opts.Timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Detect the primary language in the repo
	lang := detectCodeQLLanguage(opts.RepoPath)
	if lang == "" {
		plugin.Skipf(opts.OnOutput, "codeql", "no supported language detected. CodeQL supports: go, python, javascript, java, csharp, cpp, ruby, swift.")
		return nil, nil
	}

	// Create a temporary database directory
	dbPath, err := os.MkdirTemp("", "codeql-db-*")
	if err != nil {
		return nil, fmt.Errorf("codeql: failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(dbPath)

	// Step 1: Create the CodeQL database
	createArgs := []string{
		"database", "create", dbPath,
		"--language=" + lang,
		"--source-root=" + opts.RepoPath,
		"--overwrite",
	}
	createCmd := plugin.CommandContext(ctx, "codeql", createArgs...)
	createCmd.Dir = opts.RepoPath
	if createOut, err := createCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("codeql database create failed: %w\n%s", err, truncateOutput(createOut))
	}

	// Step 2: Ensure query pack is available
	packName := fmt.Sprintf("codeql/%s-queries", lang)
	packCheck := plugin.CommandContext(ctx, "codeql", "resolve", "qlpacks")
	if packOut, err := packCheck.CombinedOutput(); err != nil || !strings.Contains(string(packOut), packName) {
		// Query pack not found — attempt auto-download.
		if opts.OnOutput != nil {
			opts.OnOutput(fmt.Sprintf("[INFO] codeql: downloading query pack %s...", packName))
		}
		downloadCmd := plugin.CommandContext(ctx, "codeql", "pack", "download", packName)
		if downloadOut, downloadErr := downloadCmd.CombinedOutput(); downloadErr != nil {
			plugin.Skipf(opts.OnOutput, "codeql",
				"query pack %s not installed and auto-download failed. Run: codeql pack download %s\n%s",
				packName, packName, truncateOutput(downloadOut))
			return nil, nil // Skip gracefully, don't fail
		}
	}

	// Step 3: Analyze the database with the explicit query pack
	sarifFile := filepath.Join(dbPath, "results.sarif")
	analyzeArgs := []string{
		"database", "analyze", dbPath,
		packName,
		"--format=sarif-latest",
		"--output=" + sarifFile,
	}
	analyzeCmd := plugin.CommandContext(ctx, "codeql", analyzeArgs...)
	if analyzeOut, err := analyzeCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("codeql database analyze failed: %w\n%s", err, truncateOutput(analyzeOut))
	}

	out, err := os.ReadFile(sarifFile)
	if err != nil {
		return nil, fmt.Errorf("codeql: failed to read SARIF output: %w", err)
	}

	return parseCodeQLOutput(out)
}

// truncateOutput limits error output to the last 500 bytes for readability.
func truncateOutput(out []byte) string {
	s := strings.TrimSpace(string(out))
	if len(s) > 500 {
		return "..." + s[len(s)-500:]
	}
	return s
}

// SARIF types for CodeQL output
type sarifOutput struct {
	Runs []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool struct {
		Driver struct {
			Rules []sarifRule `json:"rules"`
		} `json:"driver"`
	} `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifRule struct {
	ID               string `json:"id"`
	ShortDescription struct {
		Text string `json:"text"`
	} `json:"shortDescription"`
	DefaultConfiguration struct {
		Level string `json:"level"`
	} `json:"defaultConfiguration"`
	Properties struct {
		Tags []string `json:"tags"`
	} `json:"properties"`
}

type sarifResult struct {
	RuleID    string `json:"ruleId"`
	RuleIndex int    `json:"ruleIndex"`
	Message   struct {
		Text string `json:"text"`
	} `json:"message"`
	Level     string `json:"level"`
	Locations []struct {
		PhysicalLocation struct {
			ArtifactLocation struct {
				URI string `json:"uri"`
			} `json:"artifactLocation"`
			Region struct {
				StartLine   int `json:"startLine"`
				EndLine     int `json:"endLine"`
				StartColumn int `json:"startColumn"`
			} `json:"region"`
		} `json:"physicalLocation"`
	} `json:"locations"`
}

func parseCodeQLOutput(data []byte) ([]models.Finding, error) {
	var sarif sarifOutput
	if err := json.Unmarshal(plugin.ExtractJSON(data), &sarif); err != nil {
		return nil, fmt.Errorf("failed to parse codeql SARIF output: %w", err)
	}

	var findings []models.Finding
	for _, run := range sarif.Runs {
		ruleMap := make(map[string]sarifRule)
		for _, rule := range run.Tool.Driver.Rules {
			ruleMap[rule.ID] = rule
		}

		for _, result := range run.Results {
			filePath := ""
			lineStart, lineEnd := 0, 0
			if len(result.Locations) > 0 {
				loc := result.Locations[0].PhysicalLocation
				filePath = loc.ArtifactLocation.URI
				lineStart = loc.Region.StartLine
				lineEnd = loc.Region.EndLine
			}

			level := result.Level
			if level == "" {
				if r, ok := ruleMap[result.RuleID]; ok {
					level = r.DefaultConfiguration.Level
				}
			}

			findings = append(findings, models.Finding{
				ToolName:    "codeql",
				Category:    models.CategorySAST,
				Severity:    mapSARIFSeverity(level),
				Title:       result.RuleID,
				Description: result.Message.Text,
				FilePath:    filePath,
				LineStart:   lineStart,
				LineEnd:     lineEnd,
				RuleID:      result.RuleID,
				SARIFData:   string(data),
				Status:      models.StatusOpen,
			})
		}
	}
	return findings, nil
}

func mapSARIFSeverity(level string) models.Severity {
	switch level {
	case "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	case "note":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
