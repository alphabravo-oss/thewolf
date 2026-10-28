package routes

import (
	"encoding/json"
	"os"
	"testing"
)

// Release builds override AppVersion via ldflags; images built without them
// (Enterprise overlay) fall back to the default, so it must track the release.
func TestAppVersionMatchesUIPackage(t *testing.T) {
	b, err := os.ReadFile("../../../ui/package.json")
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct{ Version string }
	if err := json.Unmarshal(b, &pkg); err != nil {
		t.Fatal(err)
	}
	if AppVersion != pkg.Version {
		t.Fatalf("routes.AppVersion %q != ui/package.json version %q; bump both", AppVersion, pkg.Version)
	}
}
