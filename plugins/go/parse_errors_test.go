package goplug

import "testing"

func TestStreamingParsersReportSkippedMalformedRecords(t *testing.T) {
	for name, parse := range map[string]func([]byte, func(error)){
		"staticcheck": func(value []byte, callback func(error)) {
			_, _ = parseStaticcheckOutputWithMetrics(value, callback)
		},
		"govulncheck": func(value []byte, callback func(error)) {
			_, _ = parseGovulncheckOutputWithMetrics(value, callback)
		},
	} {
		t.Run(name, func(t *testing.T) {
			count := 0
			parse([]byte("{malformed}\n"), func(error) { count++ })
			if count != 1 {
				t.Fatalf("parse errors = %d, want 1", count)
			}
		})
	}
}
