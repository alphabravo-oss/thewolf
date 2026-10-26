package security

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestParseZAPOutput(t *testing.T) {
	report := `{"site":[{"alerts":[{"pluginid":"10038","name":"Content Security Policy (CSP) Header Not Set","riskcode":"2","confidence":"3","desc":"No CSP.","solution":"Set CSP.","cweid":"693","wascid":"15",
"instances":[{"uri":"http://app.test/login","method":"GET","param":"","evidence":""},{"uri":"http://app.test/","method":"POST","param":"q","evidence":"x"}]}]}]}`
	findings, err := parseZAPOutput([]byte(report))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("want one finding per instance, got %d", len(findings))
	}
	f := findings[1]
	if f.ToolName != "zap" || f.Severity != models.SeverityMedium || f.Confidence != "high" || f.CWEID != "CWE-693" ||
		f.RuleID != "10038" || f.FilePath != "http://app.test/ [POST param=q]" {
		t.Fatalf("unexpected finding: %+v", f)
	}
}
