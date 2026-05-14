package additional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// CodeQLPlugin runs CodeQL SAST analysis with SARIF output.
type CodeQLPlugin struct{}

func init() {
	plugin.Register(&CodeQLPlugin{})
}

func (p *CodeQLPlugin) Name() string               { return "codeql" }
func (p *CodeQLPlugin) Category() models.Category   { return models.CategorySAST }
func (p *CodeQLPlugin) Languages() []models.Language { return nil }

func (p *CodeQLPlugin) CheckAvailable() bool { return container.IsScannersReady() }

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
	// GitHub's CodeQL CLI ships a linux/amd64 binary only — there is no
	// published linux/arm64 release. On Apple Silicon / arm64 Linux
	// hosts the codeql image is amd64 and QEMU-translates with a hard
	// ld-linux-x86-64.so.2 failure. Skip cleanly rather than pretend
	// it'll work.
	if plugin.IsArm64Host() {
		plugin.Skipf(opts.OnOutput, "codeql",
			"GitHub's CodeQL CLI is linux/amd64-only; no native arm64 build exists. Skipping on arm64 host.")
		return nil, nil
	}
	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	// CodeQL is the heaviest tool we ship (~800MB CLI). It lives in a
	// dedicated bucket image that's NOT in the default wolf-scanners.
	// If the operator hasn't built the codeql bucket and pointed
	// WOLF_SCANNERS_IMAGE_CODEQL at it, skip cleanly with guidance —
	// previously this surfaced as a confusing "exit 127" failure.
	if !cfg.HasDedicatedImage("codeql") {
		plugin.Skipf(opts.OnOutput, "codeql",
			"not configured. CodeQL lives in its own bucket image; run `make scanners-build-codeql` then set WOLF_SCANNERS_IMAGE_CODEQL=wolf-scanners-codeql:dev. Skipping.")
		return nil, nil
	}
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

	// cfg already resolved above for the HasDedicatedImage check.
	// CodeQL's database create + analyze pipeline writes to disk (it builds an
	// AST database in a temp directory). We run all three steps inside the
	// scanner container, using /tmp (the 512 MB tmpfs) for the database.
	// /scan stays read-only — codeql honors --source-root for the input.
	dbPath := "/tmp/codeql-db"
	packName := fmt.Sprintf("codeql/%s-queries", lang)
	sarifFile := "/tmp/codeql.sarif"

	// Use sh -c so we can chain the three commands and read the SARIF result
	// in a single docker run (avoiding 3 container starts).
	script := fmt.Sprintf(
		"codeql database create %s --language=%s --source-root=/scan --overwrite && "+
			"codeql resolve qlpacks | grep -q %s || codeql pack download %s && "+
			"codeql database analyze %s %s --format=sarif-latest --output=%s && "+
			"cat %s",
		dbPath, lang, packName, packName, dbPath, packName, sarifFile, sarifFile)

	cmd := container.CommandContext(ctx, cfg,
		container.Options{RepoDir: opts.RepoPath},
		"sh", "-c", script)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("codeql failed: %w\n%s", err, truncateOutput(out))
	}

	findings, perr := parseCodeQLOutput(out)
	if perr != nil {
		return nil, perr
	}
	for i := range findings {
		findings[i].FilePath = container.NormalizePath(findings[i].FilePath)
	}
	return findings, nil
}

// _silence the unused imports warning if codeql plugin no longer needs them.
var _ = os.Open
var _ = filepath.Join
var _ = strings.Contains

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
