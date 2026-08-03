package php

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/testutil/parsercontract"
)

func TestAllPHPParsersHostileCorpus(t *testing.T) {
	parsercontract.AssertHostileCases(t, "phpstan", parsePHPStanOutput)
}
