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
// Anything that must not happen has to be "deny", never "ask". (The
// exhaustive "no ask anywhere" scan across every field of both documents
// lives in TestNoAskRulesAnywhere.)
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
}

// The "ask" scan must cover every rule surface in every document: an edit
// or bash rule left at "ask" degrades to allow under --auto regardless of
// which document or field it lives in, so this checks both fields of both
// Triage and Execute rather than just execute's bash map.
func TestNoAskRulesAnywhere(t *testing.T) {
	assertNoAsk := func(t *testing.T, label string, rules map[string]string) {
		t.Helper()
		for pattern, effect := range rules {
			if effect == "ask" {
				t.Errorf("%s[%q] = \"ask\" — degrades to allow under --auto", label, pattern)
			}
		}
	}

	triageDoc, err := Triage()
	if err != nil {
		t.Fatalf("Triage: %v", err)
	}
	var triage struct {
		Permission struct {
			Edit any               `json:"edit"`
			Bash map[string]string `json:"bash"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(triageDoc, &triage); err != nil {
		t.Fatalf("unmarshal triage: %v", err)
	}
	if triage.Permission.Edit == "ask" {
		t.Errorf("triage edit = \"ask\" — degrades to allow under --auto")
	}
	assertNoAsk(t, "triage.bash", triage.Permission.Bash)

	executeDoc, err := Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var execute struct {
		Permission struct {
			Edit map[string]string `json:"edit"`
			Bash map[string]string `json:"bash"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(executeDoc, &execute); err != nil {
		t.Fatalf("unmarshal execute: %v", err)
	}
	assertNoAsk(t, "execute.edit", execute.Permission.Edit)
	assertNoAsk(t, "execute.bash", execute.Permission.Bash)
}

// Triage must be as tightly locked down as execute: bash defaults to deny
// (a triage-side command nobody enumerated must be refused, not permitted)
// and edit must be the literal string "deny", never a map that could later
// grow an "ask" or "allow" entry by accident.
func TestTriageBashDefaultsToDeny(t *testing.T) {
	doc, err := Triage()
	if err != nil {
		t.Fatalf("Triage: %v", err)
	}
	var parsed struct {
		Permission struct {
			Edit any               `json:"edit"`
			Bash map[string]string `json:"bash"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := parsed.Permission.Bash["*"]; got != "deny" {
		t.Fatalf("bash[\"*\"] = %q, want \"deny\"", got)
	}
	if parsed.Permission.Edit != "deny" {
		t.Errorf("edit = %v, want \"deny\" — triage must never write", parsed.Permission.Edit)
	}
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
	// The bare binaries must stay refused too: "git *", "npm *", and "go *"
	// each reopen egress (push, install, get) that the deny default exists
	// to close. Only the scoped subcommands below may be allowed.
	for _, mustStayBare := range []string{"git *", "npm *", "go *"} {
		if parsed.Permission.Bash[mustStayBare] == "allow" {
			t.Errorf("bash[%q] = allow, want refusal via the deny default — only scoped subcommands may be allowed", mustStayBare)
		}
	}
	// The build tooling the agent legitimately needs stays allowed, scoped
	// to the subcommands it actually uses (edit, build, test, commit) —
	// never as the bare binary.
	for _, allowed := range []string{
		"git add *", "git commit *", "git checkout *", "git diff *", "git log *", "git status *",
		"go build *", "go test *", "go vet *", "gofmt *",
		"npm test *", "npm run *", "make *", "pytest *",
	} {
		if parsed.Permission.Bash[allowed] != "allow" {
			t.Errorf("bash[%q] = %q, want \"allow\"", allowed, parsed.Permission.Bash[allowed])
		}
	}
}
