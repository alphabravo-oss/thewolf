package security

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/testutil/parsercontract"
)

func TestAllSecurityParsersHostileCorpus(t *testing.T) {
	for tool, parser := range map[string]parsercontract.ParseFunc{
		"bearer": parseBearerOutput, "detect-secrets": parseDetectSecretsOutput,
		"nuclei": parseNucleiOutput, "osv-scanner": parseOSVScannerOutput,
		"renovate": func(data []byte) ([]models.Finding, error) {
			return parseRenovateOutput(data, func(string) {})
		},
		"scorecard": parseScorecardOutput,
	} {
		t.Run(tool, func(t *testing.T) { parsercontract.AssertHostileCases(t, tool, parser) })
	}
}
