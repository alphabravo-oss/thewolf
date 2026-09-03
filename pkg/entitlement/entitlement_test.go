package entitlement

import "testing"

func TestCommunityDeniesEnterpriseCapabilities(t *testing.T) {
	var c Community
	if !c.Allows(Scan) || !c.Allows(Fix) || !c.Allows(Factory) {
		t.Fatal("community capabilities must be granted")
	}
	if c.Allows(Identity) || c.Allows(Intelligence) || c.Allows("cloud.billing") {
		t.Fatal("enterprise/cloud capabilities must be denied")
	}
	if c.Licensed() {
		t.Fatal("community binary has no commercial license")
	}
	SetActive(Community{})
	if Licensed() {
		t.Fatal("active community is not commercially licensed")
	}
	SetActive(nil)
}

func TestSyntheticCommunityLimits(t *testing.T) {
	t.Setenv(LimitsEnv, "")
	SetActive(Community{})
	lim := CommunityLimits()
	if lim.Repos != SyntheticRepos || lim.Users != SyntheticUsers || lim.Workers != SyntheticWorkers {
		t.Fatalf("synthetic = %+v", lim)
	}
	if lim.Source != SourceSynthetic || lim.Enforced {
		t.Fatalf("unforced = %+v", lim)
	}
	t.Setenv(LimitsEnv, "1")
	if !EnforceCommunityLimits() {
		t.Fatal("opt-in Community limits should enforce")
	}
	SetActive(denyAll{})
	if EnforceCommunityLimits() {
		t.Fatal("non-Community checker must not enforce Community limits")
	}
	SetActive(nil)
}

type denyAll struct{}

func (denyAll) Allows(string) bool { return false }
