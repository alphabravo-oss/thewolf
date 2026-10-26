package pluginapi

import "testing"

func TestKindsStayDistinct(t *testing.T) {
	if len(Kinds()) < 5 {
		t.Fatal(Kinds())
	}
}
