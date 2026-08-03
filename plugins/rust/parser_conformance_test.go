package rust

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/testutil/parsercontract"
)

func TestAllRustParsersHostileCorpus(t *testing.T) {
	parsercontract.AssertHostileCases(t, "clippy", parseClippyOutput)
}
