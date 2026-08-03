package security

import "testing"

func TestStreamingParsersReportSkippedMalformedRecords(t *testing.T) {
	for name, parse := range map[string]func([]byte, func(error)){
		"nuclei": func(value []byte, callback func(error)) {
			_, _ = parseNucleiOutputWithMetrics(value, callback)
		},
		"renovate": func(value []byte, callback func(error)) {
			_, _ = parseRenovateOutputWithMetrics(value, nil, callback)
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
