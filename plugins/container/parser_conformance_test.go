package container

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/testutil/parsercontract"
)

func TestAllContainerParsersHostileCorpus(t *testing.T) {
	for tool, parser := range map[string]parsercontract.ParseFunc{
		"checkov": parseCheckovOutput, "dockle": parseDockleOutput,
		"hadolint": parseHadolintOutput,
	} {
		t.Run(tool, func(t *testing.T) { parsercontract.AssertHostileCases(t, tool, parser) })
	}
}
