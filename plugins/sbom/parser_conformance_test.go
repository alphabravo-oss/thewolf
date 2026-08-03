package sbom

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/testutil/parsercontract"
)

func TestAllSBOMParsersHostileCorpus(t *testing.T) {
	parsercontract.AssertHostileCases(t, "syft", parseSyftOutput)
}
