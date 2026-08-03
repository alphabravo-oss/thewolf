package python

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/testutil/parsercontract"
)

func TestAllPythonParsersHostileCorpus(t *testing.T) {
	for tool, parser := range map[string]parsercontract.ParseFunc{
		"bandit": parseBanditOutput, "mypy": parseMypyOutput,
		"pip-audit": parsePipAuditOutput, "radon": parseRadonOutput,
		"ruff": parseRuffOutput, "vulture": parseVultureOutput,
	} {
		t.Run(tool, func(t *testing.T) { parsercontract.AssertHostileCases(t, tool, parser) })
	}
}
