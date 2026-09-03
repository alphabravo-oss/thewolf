package license

import "testing"

func TestCommunityNeverValidAndNeverConceals(t *testing.T) {
	var c Community
	st := c.Inspect("anything")
	if st.Valid || st.CommercialLicense || !st.DataIntact || !st.CommunityFallback {
		t.Fatalf("%+v", st)
	}
	if err := c.Install("blob"); err != ErrCommunityBinary {
		t.Fatalf("install: %v", err)
	}
}
