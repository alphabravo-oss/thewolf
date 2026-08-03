package general

import "testing"

func TestTruffleHogReportsSkippedMalformedRecords(t *testing.T) {
	count := 0
	_, _ = parseTrufflehogOutputWithMetrics(
		[]byte("{malformed}\n"), func(error) { count++ },
	)
	if count != 1 {
		t.Fatalf("parse errors = %d, want 1", count)
	}
}
