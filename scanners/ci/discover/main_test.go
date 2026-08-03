package main

import (
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestValidateOptions(t *testing.T) {
	t.Parallel()
	valid := options{
		concurrency:     6,
		perToolTimeout:  30 * time.Second,
		overallTimeout:  15 * time.Minute,
		minimumCoverage: 0.75,
		definitionSHA:   strings.Repeat("a", 40),
	}
	if err := validateOptions(valid); err != nil {
		t.Fatalf("validateOptions(valid) = %v", err)
	}
	for name, mutate := range map[string]func(*options){
		"zero concurrency":   func(o *options) { o.concurrency = 0 },
		"excess concurrency": func(o *options) { o.concurrency = 33 },
		"bad coverage":       func(o *options) { o.minimumCoverage = 1.01 },
		"short overall":      func(o *options) { o.overallTimeout = time.Second },
		"unsafe sha":         func(o *options) { o.definitionSHA = "$(id)" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if err := validateOptions(candidate); err == nil {
				t.Fatal("validateOptions() unexpectedly succeeded")
			}
		})
	}
}

func TestMakeReportSortIndependentCountsAndCoverage(t *testing.T) {
	t.Parallel()
	results := []models.ScannerVersionCheck{
		{ToolName: "a", Status: models.ScannerVersionCurrent},
		{ToolName: "b", Status: models.ScannerVersionUpdateAvailable},
		{ToolName: "c", Status: models.ScannerVersionCheckFailed},
		{ToolName: "d", Status: models.ScannerVersionUnknown},
	}
	at := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	rep := makeReport(results, []byte("manifest"), strings.Repeat("b", 40), at)
	if rep.SchemaVersion != reportSchema {
		t.Fatalf("SchemaVersion = %q", rep.SchemaVersion)
	}
	if rep.Counts.Total != 4 || rep.Counts.Checked != 2 ||
		rep.Counts.Current != 1 || rep.Counts.UpdateAvailable != 1 ||
		rep.Counts.Failed != 1 || rep.Counts.Unknown != 1 {
		t.Fatalf("Counts = %+v", rep.Counts)
	}
	if rep.Coverage != 0.5 {
		t.Fatalf("Coverage = %v, want 0.5", rep.Coverage)
	}
	if !strings.HasPrefix(rep.ManifestSHA256, "sha256:") || len(rep.ManifestSHA256) != 71 {
		t.Fatalf("ManifestSHA256 = %q", rep.ManifestSHA256)
	}
}

func TestSanitizeError(t *testing.T) {
	t.Parallel()
	got := sanitizeError("bad\nheader\tvalue\x00")
	if got != "bad header value" {
		t.Fatalf("sanitizeError() = %q", got)
	}
	long := sanitizeError(strings.Repeat("x", 600))
	if len(long) > 504 || !strings.HasSuffix(long, "…") {
		t.Fatalf("long sanitized value has length %d and suffix %q", len(long), long[len(long)-3:])
	}
}

func TestMarkdownCell(t *testing.T) {
	t.Parallel()
	if got := markdownCell("a|b\nc"); got != `a\|b c` {
		t.Fatalf("markdownCell() = %q", got)
	}
}
