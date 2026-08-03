package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadScannerProposalRequestIsStrictAndBounded(t *testing.T) {
	t.Parallel()
	request, err := readScannerProposalRequest(strings.NewReader(`{
  "candidate_id":"candidate-1",
  "definition_commit":"0123456789abcdef0123456789abcdef01234567",
  "selection":{"mode":"complete"},
  "risk_summary":{},
  "required_gates":["signature"],
  "source_date_epoch":1,
  "policy_id":"policy",
  "policy_revision":1,
  "idempotency_key":"candidate-1/proposal"
}`))
	if err != nil || request.CandidateID != "candidate-1" || len(request.RequiredGates) != 1 {
		t.Fatalf("request=%#v err=%v", request, err)
	}
	if _, err := readScannerProposalRequest(strings.NewReader(`{"candidate_id":"x","unknown":true}`)); err == nil {
		t.Fatal("unknown proposal request field was accepted")
	}
	if _, err := readScannerProposalRequest(bytes.NewReader(make([]byte, maximumScannerProposalRequest+1))); err == nil {
		t.Fatal("oversized proposal request was accepted")
	}
}

func TestReadScannerProposalCredentialPrefersOneExplicitSource(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	credential, err := readScannerProposalCredential(path, "")
	if err != nil || credential != "file-token" {
		t.Fatalf("credential=%q err=%v", credential, err)
	}
	if _, err := readScannerProposalCredential(path, "environment-token"); err == nil {
		t.Fatal("multiple proposal credential sources were accepted")
	}
	if _, err := readScannerProposalCredential("", "line-1\nline-2"); err == nil {
		t.Fatal("multiline proposal credential was accepted")
	}
}
