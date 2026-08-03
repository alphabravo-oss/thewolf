package general

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/testutil/parsercontract"
)

func TestAllGeneralParsersHostileCorpus(t *testing.T) {
	for tool, parser := range map[string]parsercontract.ParseFunc{
		"gitleaks": parseGitleaksOutput, "grype": parseGrypeOutput,
		"semgrep": parseSemgrepOutput, "trivy": parseTrivyOutput,
		"trufflehog": parseTrufflehogOutput,
	} {
		t.Run(tool, func(t *testing.T) { parsercontract.AssertHostileCases(t, tool, parser) })
	}
}
