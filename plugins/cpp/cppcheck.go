package cpp

import (
	"context"
	"encoding/xml"
	"fmt"
	"os/exec"
	"strconv"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// CppcheckPlugin runs cppcheck static analysis for C/C++ code.
type CppcheckPlugin struct{}

func init() {
	plugin.Register(&CppcheckPlugin{})
}

func (p *CppcheckPlugin) Name() string             { return "cppcheck" }
func (p *CppcheckPlugin) Category() models.Category { return models.CategorySAST }
func (p *CppcheckPlugin) Languages() []models.Language {
	return []models.Language{models.LangC, models.LangCPP}
}

func (p *CppcheckPlugin) CheckAvailable() bool {
	_, err := exec.LookPath("cppcheck")
	return err == nil
}

func (p *CppcheckPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if !plugin.HasFilesWithExtension(opts.RepoPath, "c", "cpp", "cc", "h", "hpp") {
		plugin.Skipf(opts.OnOutput, "cppcheck", "no C/C++ source files found. Add .c, .cpp, or .h files to enable static analysis.")
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := plugin.CommandContext(ctx, "cppcheck", "--xml", "--enable=all", opts.RepoPath)
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return nil, plugin.WrapExecError("cppcheck", err)
		}
	}

	return parseCppcheckOutput(out)
}

type cppcheckResults struct {
	XMLName xml.Name       `xml:"results"`
	Errors  []cppcheckError `xml:"errors>error"`
}

type cppcheckError struct {
	ID       string            `xml:"id,attr"`
	Severity string            `xml:"severity,attr"`
	Msg      string            `xml:"msg,attr"`
	Verbose  string            `xml:"verbose,attr"`
	CWE      string            `xml:"cwe,attr"`
	Location []cppcheckLocation `xml:"location"`
}

type cppcheckLocation struct {
	File string `xml:"file,attr"`
	Line string `xml:"line,attr"`
}

func parseCppcheckOutput(data []byte) ([]models.Finding, error) {
	var results cppcheckResults
	if err := xml.Unmarshal(plugin.ExtractXML(data), &results); err != nil {
		return nil, fmt.Errorf("failed to parse cppcheck output: %w", err)
	}

	findings := make([]models.Finding, 0, len(results.Errors))
	for _, e := range results.Errors {
		file := ""
		line := 0
		if len(e.Location) > 0 {
			file = e.Location[0].File
			line, _ = strconv.Atoi(e.Location[0].Line)
		}

		findings = append(findings, models.Finding{
			ToolName:    "cppcheck",
			Category:    models.CategorySAST,
			Severity:    mapCppcheckSeverity(e.Severity),
			Title:       e.ID,
			Description: e.Msg,
			FilePath:    file,
			LineStart:   line,
			CWEID:       e.CWE,
			RuleID:      e.ID,
			Status:      models.StatusOpen,
		})
	}
	return findings, nil
}

func mapCppcheckSeverity(s string) models.Severity {
	switch s {
	case "error":
		return models.SeverityHigh
	case "warning":
		return models.SeverityMedium
	case "style", "performance", "portability":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
