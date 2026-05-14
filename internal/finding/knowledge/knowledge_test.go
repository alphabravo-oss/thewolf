package knowledge

import "testing"

func TestValidateNoDanglingStrategies(t *testing.T) {
	if dangling := Validate(); len(dangling) > 0 {
		t.Fatalf("knowledge base references missing strategies: %v", dangling)
	}
}

func TestRegistryNonEmpty(t *testing.T) {
	entryCount, stratCount := Stats()
	if entryCount == 0 {
		t.Errorf("knowledge base has no rule entries — at least one data_<tool>.go init() must register entries")
	}
	if stratCount == 0 {
		t.Errorf("knowledge base has no fix strategies")
	}
}

func TestCategorize_ExactLookup(t *testing.T) {
	fc, fs := Categorize("gosec", "G201")
	if fc != "sql-injection" {
		t.Errorf("Categorize(gosec, G201) fineCategory = %q, want sql-injection", fc)
	}
	if fs != "parameterize-query" {
		t.Errorf("Categorize(gosec, G201) fixStrategy = %q, want parameterize-query", fs)
	}
}

func TestCategorize_TrivyCVEFallback(t *testing.T) {
	fc, fs := Categorize("trivy", "CVE-2024-12345")
	if fc != "vulnerable-dependency" || fs != "update-vulnerable-dependency" {
		t.Errorf("Categorize(trivy, CVE-...) = %q,%q; want vulnerable-dependency,update-vulnerable-dependency", fc, fs)
	}
}

func TestCategorize_SemgrepPrefixFallback(t *testing.T) {
	fc, fs := Categorize("semgrep", "go.lang.security.audit.sqli.some-new-rule")
	if fc != "sql-injection" || fs != "parameterize-query" {
		t.Errorf("Categorize(semgrep, sqli prefix) = %q,%q; want sql-injection,parameterize-query", fc, fs)
	}
}

func TestCategorize_Unknown(t *testing.T) {
	fc, fs := Categorize("unknown-tool", "whatever")
	if fc != "" || fs != "" {
		t.Errorf("unknown rule should return empty, got %q,%q", fc, fs)
	}
}

func TestGosec_CoreRulesPresent(t *testing.T) {
	// Quick sanity that the high-impact rules made it in.
	for _, id := range []string{"G201", "G401", "G404", "G304"} {
		if _, ok := Lookup("gosec", id); !ok {
			t.Errorf("missing gosec entry %s", id)
		}
	}
}

func TestGitleaks_CommonSecretsPresent(t *testing.T) {
	for _, id := range []string{"generic-api-key", "aws-access-token", "github-pat", "private-key"} {
		if _, ok := Lookup("gitleaks", id); !ok {
			t.Errorf("missing gitleaks entry %s", id)
		}
	}
}
