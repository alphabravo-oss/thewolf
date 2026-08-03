package routes

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerrollout"
)

func TestRolloutHealthFromSummarySeparatesBoundedEvidence(t *testing.T) {
	observedAt := time.Date(2026, time.July, 30, 18, 0, 0, 0, time.UTC)
	raw, err := json.Marshal(map[string]any{
		"health": scannerrollout.CanaryHealth{
			Samples: 12, StableSamples: 20,
			ParserFailures:       1,
			CandidateP95Duration: 1500 * time.Millisecond,
			StableP95Duration:    time.Second,
		},
		"synthetic_health": scannerrollout.SyntheticHealthEvidence{
			CorpusID:     "wolf-core-synthetic-2026.1",
			CorpusDigest: "sha256:" + strings.Repeat("a", 64),
			Current:      true, State: "passed",
			FixtureTotal: 3, FixturePassed: 3, ObservedAt: observedAt,
		},
		"real_scan_health": scannerrollout.RealScanHealthEvidence{
			State: "degraded", CandidateSamples: 12, StableSamples: 20,
			ParserFailures: 1, CandidateP95DurationMS: 1500,
			StableP95DurationMS: 1000, WorkersTotal: 2, WorkersReady: 1,
			WorkersFailed: 1, ObservedAt: observedAt,
		},
		// These keys model sensitive/raw fields that must never be copied into
		// the public projection.
		"worker_id":  "worker-secret-id",
		"raw_output": "Authorization: Bearer secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	combined, synthetic, realScans, ok := rolloutHealthFromSummary(string(raw))
	if !ok {
		t.Fatal("valid durable health summary was rejected")
	}
	if combined["samples"] != 12 ||
		combined["candidate_p95_duration_ms"] != int64(1500) ||
		combined["stable_p95_duration_ms"] != int64(1000) {
		t.Fatalf("combined health = %#v", combined)
	}
	if _, leaked := combined["worker_id"]; leaked {
		t.Fatalf("combined health leaked an unapproved field: %#v", combined)
	}
	if synthetic == nil || synthetic.CorpusID != "wolf-core-synthetic-2026.1" ||
		!synthetic.Current || synthetic.FixturePassed != 3 {
		t.Fatalf("synthetic health = %#v", synthetic)
	}
	if realScans == nil || realScans.CandidateSamples != 12 ||
		realScans.ParserFailures != 1 || realScans.WorkersFailed != 1 {
		t.Fatalf("real-scan health = %#v", realScans)
	}
}

func TestRolloutHealthFromSummaryPreservesLegacyCombinedCountersOnly(t *testing.T) {
	raw, err := json.Marshal(scannerrollout.CanaryHealth{
		Samples: 4, InfrastructureFailures: 1,
		CandidateP95Duration: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	combined, synthetic, realScans, ok := rolloutHealthFromSummary(string(raw))
	if !ok || combined["samples"] != 4 ||
		combined["candidate_p95_duration_ms"] != int64(250) {
		t.Fatalf("legacy combined health = %#v, ok=%v", combined, ok)
	}
	if synthetic != nil || realScans != nil {
		t.Fatalf("legacy summary fabricated separated evidence: %#v %#v", synthetic, realScans)
	}
}
