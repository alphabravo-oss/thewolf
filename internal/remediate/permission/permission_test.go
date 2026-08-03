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

// Both documents must root-deny. OpenCode's permission map has roughly
// fifteen keys and an unset key defaults to allow, so an enumerated blocklist
// grants every key nobody thought to name — including webfetch and websearch,
// which are network egress the bash allowlist never sees. Only the read-side
// keys below (plus todowrite in execute) may be re-allowed above the root.
func TestBothDocumentsRootDeny(t *testing.T) {
	triageDoc, err := Triage()
	if err != nil {
		t.Fatalf("Triage: %v", err)
	}
	executeDoc, err := Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Keys OpenCode must never grant: each is either egress (webfetch,
	// websearch), a way to spawn an unconstrained sub-agent (task, skill), or
	// an interactive escape (question, doom_loop, lsp).
	forbidden := []string{"task", "skill", "webfetch", "websearch", "lsp", "question", "doom_loop"}
	for _, tc := range []struct {
		label   string
		doc     []byte
		allowed []string
	}{
		{"triage", triageDoc, []string{"read", "glob", "grep", "list"}},
		{"execute", executeDoc, []string{"read", "glob", "grep", "list", "todowrite"}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			var parsed struct {
				Permission map[string]any `json:"permission"`
			}
			if err := json.Unmarshal(tc.doc, &parsed); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := parsed.Permission["*"]; got != "deny" {
				t.Fatalf("permission[\"*\"] = %v, want \"deny\" — unset keys default to allow", got)
			}
			for _, key := range tc.allowed {
				if got := parsed.Permission[key]; got != "allow" {
					t.Errorf("permission[%q] = %v, want \"allow\"", key, got)
				}
			}
			for _, key := range forbidden {
				if _, present := parsed.Permission[key]; present {
					t.Errorf("permission[%q] is named — naming it overrides the root deny", key)
				}
			}
		})
	}

	// todowrite is execute-only: triage reads and reports, it never tracks
	// work, so it has no reason to escape the root deny.
	var triage struct {
		Permission map[string]any `json:"permission"`
	}
	if err := json.Unmarshal(triageDoc, &triage); err != nil {
		t.Fatalf("unmarshal triage: %v", err)
	}
	if _, present := triage.Permission["todowrite"]; present {
		t.Error("triage names todowrite — the read-only document must not grant it")
	}
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
		// The opencode.* / .opencode entries matter because OpenCode ranks
		// project config above the config Wolf passes by environment
		// variable: an agent that writes one outranks its own permissions.
		"edit": {
			".github/**", "**/*.pem", "**/*.key",
			"opencode.json", "opencode.jsonc", ".opencode/**",
		},
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
