package swift

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/testutil/parsercontract"
)

func TestAllSwiftParsersHostileCorpus(t *testing.T) {
	parsercontract.AssertHostileCases(t, "swiftlint", parseSwiftLintOutput)
}
