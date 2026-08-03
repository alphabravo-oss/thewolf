package permission

import (
	"encoding/json"
	"os"
	"testing"
)

func assertGolden(t *testing.T, got []byte, golden string) {
	t.Helper()
	want, err := os.ReadFile("testdata/" + golden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var gotDoc, wantDoc any
	if err := json.Unmarshal(got, &gotDoc); err != nil {
		t.Fatalf("generated doc is not valid JSON: %v", err)
	}
	if err := json.Unmarshal(want, &wantDoc); err != nil {
		t.Fatalf("golden is not valid JSON: %v", err)
	}
	gotNorm, _ := json.Marshal(gotDoc)
	wantNorm, _ := json.Marshal(wantDoc)
	if string(gotNorm) != string(wantNorm) {
		t.Errorf("permission document drift.\n got: %s\nwant: %s", gotNorm, wantNorm)
	}
}

func TestTriageIsReadOnly(t *testing.T) {
	doc, err := Triage()
	if err != nil {
		t.Fatalf("Triage: %v", err)
	}
	assertGolden(t, doc, "triage.json")

	var parsed struct {
		Permission struct {
			Edit any `json:"edit"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Permission.Edit != "deny" {
		t.Errorf("triage edit = %v, want \"deny\" — triage must never write", parsed.Permission.Edit)
	}
}

func TestExecuteDeniesDangerousPaths(t *testing.T) {
	doc, err := Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertGolden(t, doc, "execute.json")
}

// The hard deny list must hold under --auto, where "ask" degrades to allow.
// Anything that must not happen has to be "deny", never "ask".
func TestNoAskRulesForDangerousActions(t *testing.T) {
	doc, err := Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var parsed struct {
		Permission struct {
			Edit map[string]string `json:"edit"`
			Bash map[string]string `json:"bash"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mustDeny := map[string][]string{
		"edit": {".github/**", "**/*.pem", "**/*.key"},
		"bash": {"rm -rf *", "curl *", "sudo *"},
	}
	for tool, patterns := range mustDeny {
		rules := parsed.Permission.Edit
		if tool == "bash" {
			rules = parsed.Permission.Bash
		}
		for _, p := range patterns {
			if rules[p] != "deny" {
				t.Errorf("%s[%q] = %q, want \"deny\"", tool, p, rules[p])
			}
		}
	}

	// No rule anywhere may be "ask": under --auto it silently becomes allow.
	for pattern, effect := range parsed.Permission.Bash {
		if effect == "ask" {
			t.Errorf("bash[%q] = \"ask\" — degrades to allow under --auto", pattern)
		}
	}
}

// bash defaults to deny so a command nobody enumerated is refused rather
// than permitted. This is the difference between an allowlist and a
// blocklist, and it is the whole reason yolo mode is survivable.
func TestExecuteBashDefaultsToDeny(t *testing.T) {
	doc, err := Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var parsed struct {
		Permission struct {
			Bash map[string]string `json:"bash"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := parsed.Permission.Bash["*"]; got != "deny" {
		t.Fatalf("bash[\"*\"] = %q, want \"deny\"", got)
	}
	// Commands nobody thought to denylist must still be refused.
	for _, unlisted := range []string{"nc *", "ssh *", "chmod *", "dd *", "base64 *"} {
		if effect, present := parsed.Permission.Bash[unlisted]; present && effect == "allow" {
			t.Errorf("bash[%q] = allow, want refusal via the deny default", unlisted)
		}
	}
	// The build tooling the agent legitimately needs stays allowed.
	for _, allowed := range []string{"git *", "npm *", "go *"} {
		if parsed.Permission.Bash[allowed] != "allow" {
			t.Errorf("bash[%q] = %q, want \"allow\"", allowed, parsed.Permission.Bash[allowed])
		}
	}
}
