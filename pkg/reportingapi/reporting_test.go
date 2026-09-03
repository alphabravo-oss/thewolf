package reportingapi

import "testing"

func TestStubReporter(t *testing.T) {
	var r Reporter = Stub{ID: "enterprise.proof-report"}
	if r.Name() != "enterprise.proof-report" {
		t.Fatal(r.Name())
	}
}
