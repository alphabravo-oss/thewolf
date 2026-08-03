package goplug

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/testutil/parsercontract"
)

func TestAllGoParsersHostileCorpus(t *testing.T) {
	for tool, parser := range map[string]parsercontract.ParseFunc{
		"gokart": parseGoKartOutput, "gosec": parseGosecOutput,
		"govulncheck": parseGovulncheckOutput, "staticcheck": parseStaticcheckOutput,
	} {
		t.Run(tool, func(t *testing.T) { parsercontract.AssertHostileCases(t, tool, parser) })
	}
}
