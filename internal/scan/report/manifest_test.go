package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestRefreshManifestCountsRejectsInvalidIdentityAndPreservesHistory(t *testing.T) {
	for _, data := range []string{`{`, `null`, `{"scan_id":"other"}`, `{"scan_id":"scan","counts":{"raw_findings":-1}}`} {
		if _, err := RefreshManifestCounts([]byte(data), "scan", 1, "", nil); err == nil {
			t.Fatalf("invalid manifest accepted: %s", data)
		}
	}
	raw := []byte(`{"scan_id":"scan","counts":{"raw_findings":12},"baseline_scan_id":"old","producer_counter":9007199254740993}`)
	updated, err := RefreshManifestCounts(raw, "scan", 4, "new", []models.Finding{{Severity: models.SeverityHigh}, {Suppressed: true}})
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(updated, &m); err != nil {
		t.Fatal(err)
	}
	if m.BaselineScanID != "new" || m.FindingsRevision == nil || *m.FindingsRevision != 4 || m.Counts.RawFindings != 12 || m.Counts.AfterDedupe != 2 || m.Counts.Visible != 1 || m.Counts.Suppressed != 1 {
		t.Fatalf("incorrect live fields: %+v", m)
	}
	if !strings.Contains(string(updated), "9007199254740993") {
		t.Fatal("producer numeric precision lost")
	}
	updated, err = RefreshManifestCounts(updated, "scan", 5, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(updated), "baseline_scan_id") {
		t.Fatal("stale baseline retained")
	}
	if !strings.Contains(string(raw), `"baseline_scan_id":"old"`) {
		t.Fatal("input artifact mutated")
	}
}
