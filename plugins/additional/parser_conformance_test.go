package additional

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/testutil/parsercontract"
)

func TestAllAdditionalParsersHostileCorpus(t *testing.T) {
	for tool, parser := range map[string]parsercontract.ParseFunc{
		"codeql": parseCodeQLOutput, "detekt": parseDetektOutput,
		"pmd": parsePMDOutput, "shellcheck": parseShellcheckOutput,
		"yamllint": parsercontract.WithoutError(parseYamllintOutput),
	} {
		t.Run(tool, func(t *testing.T) { parsercontract.AssertHostileCases(t, tool, parser) })
	}
}
