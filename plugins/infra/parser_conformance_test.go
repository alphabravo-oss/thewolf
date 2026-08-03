package infra

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/testutil/parsercontract"
)

func TestAllInfrastructureParsersHostileCorpus(t *testing.T) {
	for tool, parser := range map[string]parsercontract.ParseFunc{
		"conftest": parseConftestOutput, "kics": parseKICSOutput,
		"kube-linter": parseKubeLinterOutput, "kubescape": parseKubescapeOutput,
		"pluto": parsePlutoOutput, "tflint": parseTFLintOutput,
	} {
		t.Run(tool, func(t *testing.T) { parsercontract.AssertHostileCases(t, tool, parser) })
	}
}
