package sql

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/testutil/parsercontract"
)

func TestAllSQLParsersHostileCorpus(t *testing.T) {
	parsercontract.AssertHostileCases(t, "sqlfluff", parseSQLFluffOutput)
}
