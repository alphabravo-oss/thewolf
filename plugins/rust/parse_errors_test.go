package rust

import "testing"

func TestClippyReportsSkippedMalformedRecords(t *testing.T) {
	count := 0
	_, _ = parseClippyOutputWithMetrics(
		[]byte("{malformed}\n"), func(error) { count++ },
	)
	if count != 1 {
		t.Fatalf("parse errors = %d, want 1", count)
	}
}
