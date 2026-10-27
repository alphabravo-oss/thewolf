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
