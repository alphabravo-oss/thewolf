package docs

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/testutil/parsercontract"
)

func TestAllDocumentationParsersHostileCorpus(t *testing.T) {
	for tool, parser := range map[string]parsercontract.ParseFunc{
		"markdownlint": parsercontract.WithoutError(parseMarkdownlintOutput),
		"spectral":     parseSpectralOutput, "vale": parseValeOutput,
	} {
		t.Run(tool, func(t *testing.T) { parsercontract.AssertHostileCases(t, tool, parser) })
	}
}
