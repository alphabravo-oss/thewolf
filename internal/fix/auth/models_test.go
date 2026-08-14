package auth

import (
	"strings"
	"testing"
)

func TestParseOpenCodeModelsVerboseIncludesSol(t *testing.T) {
	raw := `
openai/gpt-5.4
{
  "id": "gpt-5.4",
  "providerID": "openai",
  "name": "GPT-5.4",
  "limit": {"context": 256000},
  "variants": {"low": {}, "medium": {}, "high": {}}
}
openai/gpt-5.6-sol
{
  "id": "gpt-5.6-sol",
  "providerID": "openai",
  "name": "GPT-5.6 Sol",
  "limit": {"context": 500000},
  "variants": {"none": {}, "low": {}, "medium": {}, "high": {}, "xhigh": {}}
}
`
	got := parseOpenCodeModelsVerbose([]byte(raw))
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	sol := got[1]
	if sol.ID != "openai/gpt-5.6-sol" || sol.Label != "GPT-5.6 Sol" || sol.ContextK != 500 {
		t.Fatalf("sol = %+v", sol)
	}
	if !sol.Default {
		t.Fatal("sol should be the preferred default")
	}
	if len(sol.Efforts) < 4 {
		t.Fatalf("sol efforts = %+v", sol.Efforts)
	}
	var ids []string
	for _, e := range sol.Efforts {
		ids = append(ids, e.ID)
	}
	if !strings.Contains(strings.Join(ids, ","), "high") {
		t.Fatalf("missing high: %v", ids)
	}
}

func TestParseOpenCodeModelIDs(t *testing.T) {
	got := parseOpenCodeModelIDs([]byte("openai/gpt-5.6-sol\nopenai/gpt-5.6-sol-fast\nnot a model\n"))
	if len(got) != 2 || got[0].ID != "openai/gpt-5.6-sol" {
		t.Fatalf("%+v", got)
	}
}

func TestStripANSI(t *testing.T) {
	if got := stripANSI("\x1b[0mOpenAI oauth\x1b[39m"); got != "OpenAI oauth" {
		t.Fatalf("got %q", got)
	}
}
