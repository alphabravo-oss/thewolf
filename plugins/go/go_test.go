package goplug

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

func testdataPath(name string) string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "testdata", name)
}

func TestParseGosecOutput(t *testing.T) {
	data, err := os.ReadFile(testdataPath("gosec_output.json"))
	if err != nil {
		t.Fatalf("failed to read testdata: %v", err)
	}

	findings, err := parseGosecOutput(data)
	if err != nil {
		t.Fatalf("parseGosecOutput returned error: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	// First finding: G201, SQL injection
	f := findings[0]
	if f.Severity != models.SeverityHigh {
		t.Errorf("finding[0].Severity = %q, want %q", f.Severity, models.SeverityHigh)
	}
	if f.ToolName != "gosec" {
		t.Errorf("finding[0].ToolName = %q, want %q", f.ToolName, "gosec")
	}
	if f.RuleID != "G201" {
		t.Errorf("finding[0].RuleID = %q, want %q", f.RuleID, "G201")
	}
	if f.CWEID != "89" {
		t.Errorf("finding[0].CWEID = %q, want %q", f.CWEID, "89")
	}
	if f.FilePath != "db/queries.go" {
		t.Errorf("finding[0].FilePath = %q, want %q", f.FilePath, "db/queries.go")
	}
	if f.LineStart != 25 {
		t.Errorf("finding[0].LineStart = %d, want %d", f.LineStart, 25)
	}

	// Second finding: G401, weak crypto
	f = findings[1]
	if f.Severity != models.SeverityMedium {
		t.Errorf("finding[1].Severity = %q, want %q", f.Severity, models.SeverityMedium)
	}
	if f.RuleID != "G401" {
		t.Errorf("finding[1].RuleID = %q, want %q", f.RuleID, "G401")
	}
	if f.CWEID != "326" {
		t.Errorf("finding[1].CWEID = %q, want %q", f.CWEID, "326")
	}
	if f.FilePath != "crypto/hash.go" {
		t.Errorf("finding[1].FilePath = %q, want %q", f.FilePath, "crypto/hash.go")
	}
	if f.LineStart != 15 {
		t.Errorf("finding[1].LineStart = %d, want %d", f.LineStart, 15)
	}
}

func TestParseStaticcheckOutput(t *testing.T) {
	data, err := os.ReadFile(testdataPath("staticcheck_output.jsonl"))
	if err != nil {
		t.Fatalf("failed to read testdata: %v", err)
	}

	findings, err := parseStaticcheckOutput(data)
	if err != nil {
		t.Fatalf("parseStaticcheckOutput returned error: %v", err)
	}

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	// First finding: SA1000 (likely bug) is medium; staticcheck's "error" label carries no signal.
	f := findings[0]
	if f.Severity != models.SeverityMedium {
		t.Errorf("finding[0].Severity = %q, want %q", f.Severity, models.SeverityMedium)
	}
	if f.ToolName != "staticcheck" {
		t.Errorf("finding[0].ToolName = %q, want %q", f.ToolName, "staticcheck")
	}
	if f.RuleID != "SA1000" {
		t.Errorf("finding[0].RuleID = %q, want %q", f.RuleID, "SA1000")
	}
	if f.FilePath != "main.go" {
		t.Errorf("finding[0].FilePath = %q, want %q", f.FilePath, "main.go")
	}
	if f.LineStart != 15 {
		t.Errorf("finding[0].LineStart = %d, want %d", f.LineStart, 15)
	}

	// Second finding: SA4006, warning severity
	f = findings[1]
	if f.Severity != models.SeverityMedium {
		t.Errorf("finding[1].Severity = %q, want %q", f.Severity, models.SeverityMedium)
	}
	if f.RuleID != "SA4006" {
		t.Errorf("finding[1].RuleID = %q, want %q", f.RuleID, "SA4006")
	}
	if f.FilePath != "utils.go" {
		t.Errorf("finding[1].FilePath = %q, want %q", f.FilePath, "utils.go")
	}
	if f.LineStart != 30 {
		t.Errorf("finding[1].LineStart = %d, want %d", f.LineStart, 30)
	}
}

func TestParseGoKartOutput(t *testing.T) {
	findings, err := parseGoKartOutput([]byte(`{"runs":[{"results":[{"ruleId":"G107","message":{"text":"URL provided to HTTP request as taint input"},"level":"error","locations":[{"physicalLocation":{"artifactLocation":{"uri":"client.go"},"region":{"startLine":21,"endLine":21}}}]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].ToolName != "gokart" || findings[0].RuleID != "G107" || findings[0].LineStart != 21 || findings[0].Severity != models.SeverityHigh {
		t.Fatalf("gokart finding = %#v", findings)
	}
}

func TestGoKartExecutionContract(t *testing.T) {
	const finding = `{"runs":[{"results":[{"ruleId":"command_injection","message":{"text":"unsafe command"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"/scan/main.go"},"region":{"startLine":5}}}]}]}]}`
	for _, tc := range []struct {
		name, output, exit string
		count              int
		wantError          bool
	}{
		{"findings exit", finding, "1", 1, false},
		{"clean", `{"runs":[{"results":[]}]}`, "0", 0, false},
		{"crash after results", finding, "2", 1, true},
		{"failure with empty report", `{"runs":[{}]}`, "1", 0, true},
		{"invalid report", `{}`, "0", 0, true},
		{"crash banner", "starting scan", "2", 0, true},
		{"missing output", "", "0", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module fixture\n"), 0600); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(t.TempDir(), "output")
			if err := os.WriteFile(output, []byte(tc.output), 0600); err != nil {
				t.Fatal(err)
			}
			container.SetCommandFactory(func(ctx context.Context, _ *container.Config, opts container.Options, tool string, args ...string) *exec.Cmd {
				if tool != "gokart" || !slices.Equal(args, []string{"scan", "--sarif", "--exitCode", "./..."}) || opts.RepoDir != repo {
					t.Fatalf("wrong scanner invocation: %s %v, repo %s", tool, args, opts.RepoDir)
				}
				return exec.CommandContext(ctx, "sh", "-c", `cat "$1"; exit "$2"`, "gokart-test", output, tc.exit)
			})
			t.Cleanup(func() { container.SetCommandFactory(nil) })
			findings, err := (&GoKartPlugin{}).Execute(t.Context(), models.ExecuteOpts{RepoPath: repo, ContainerCfg: &container.Config{Image: "test"}})
			if len(findings) != tc.count || (err != nil) != tc.wantError {
				t.Fatalf("got %d findings and error %v; want %d findings, error=%v", len(findings), err, tc.count, tc.wantError)
			}
			if len(findings) > 0 && findings[0].FilePath != "main.go" {
				t.Fatalf("path was not normalized: %s", findings[0].FilePath)
			}
		})
	}
}

func TestParseGovulncheckOutput(t *testing.T) {
	data := []byte("{\"osv\":{\"id\":\"GO-2026-0001\",\"summary\":\"fixture vulnerability\",\"details\":\"fixture details\"}}\n{\"finding\":{\"osv\":\"GO-2026-0001\",\"trace\":[{\"position\":{\"filename\":\"main.go\",\"line\":14}}]}}\n")
	findings, err := parseGovulncheckOutput(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].ToolName != "govulncheck" || findings[0].RuleID != "GO-2026-0001" || findings[0].FilePath != "main.go" || findings[0].LineStart != 14 {
		t.Fatalf("govulncheck finding = %#v", findings)
	}
}

func TestStaticcheckSeverityFollowsCheckFamily(t *testing.T) {
	for code, want := range map[string]models.Severity{"SA5011": models.SeverityMedium, "U1000": models.SeverityLow, "ST1003": models.SeverityLow, "S1000": models.SeverityLow} {
		if got := mapStaticcheckSeverity(code); got != want {
			t.Errorf("%s = %s, want %s", code, got, want)
		}
	}
}
