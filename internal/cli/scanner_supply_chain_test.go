package cli

import (
	"strings"
	"testing"
	"time"
)

func TestScannerCandidateExceptionCLIValidatesCompleteContractLocally(t *testing.T) {
	if _, err := run(t,
		"scanner", "candidate", "exception", "candidate-id",
		"--gate", "vulnerability",
	); err == nil || !strings.Contains(err.Error(), "--owner") {
		t.Fatalf("incomplete exception validation error = %v", err)
	}
	if _, err := run(t,
		"scanner", "candidate", "exception", "candidate-id",
		"--gate", "vulnerability", "--owner", "security-owner",
		"--reason", "temporary advisory", "--compensating-control", "quarantine",
		"--evidence-digest", "sha256:"+strings.Repeat("a", 64),
		"--expires-at", time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
	); err == nil || !strings.Contains(err.Error(), "future RFC3339") {
		t.Fatalf("expired exception validation error = %v", err)
	}
}
