// Package parsercontract provides the shared hostile-output contract executed
// by every registered scanner parser test. It intentionally permits a parser
// to reject malformed input; the contract is bounded completion, no panic, and
// no cross-tool finding attribution for any partial or adversarial result.
package parsercontract

import (
	"bytes"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

type ParseFunc func([]byte) ([]models.Finding, error)

func WithoutError(parser func([]byte) []models.Finding) ParseFunc {
	return func(data []byte) ([]models.Finding, error) { return parser(data), nil }
}

func AssertHostileCases(t *testing.T, tool string, parser ParseFunc) {
	t.Helper()
	large := append([]byte(`{"padding":"`), bytes.Repeat([]byte("a"), 1<<20)...)
	large = append(large, []byte(`"}`)...)
	cases := map[string][]byte{
		"malformed":   []byte("{not-json}\x00"),
		"partial":     []byte(`{"results":[{"rule":"WOLF-FIXTURE"}`),
		"empty":       {},
		"large":       large,
		"encoded":     []byte(`{"message":"café 安全","path":"src/space name.go"}`),
		"non-utf8":    {0xff, 0x00, 0x01},
		"adversarial": []byte("{\"path\":\"../../etc/passwd\",\"line\":-1,\"message\":\"${WOLF_FAKE_SECRET}\\u001b[31m\"}"),
	}
	for name, input := range cases {
		name, input := name, input
		t.Run(name, func(t *testing.T) {
			var findings []models.Finding
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						t.Fatalf("parser panicked: %v", recovered)
					}
				}()
				findings, _ = parser(input)
			}()
			if len(findings) > 100_000 {
				t.Fatalf("parser returned %d findings for bounded hostile input", len(findings))
			}
			for index, finding := range findings {
				if finding.ToolName != tool {
					t.Fatalf("finding %d attributed to %q, want %q", index, finding.ToolName, tool)
				}
			}
		})
	}
}
