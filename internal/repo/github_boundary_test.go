package repo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitHubCoordinatesConfineCacheAndClone(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WOLF_WORKSPACE_ROOT", root)
	cache, err := getCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, coordinate := range [][2]string{
		{"owner", "repo"}, {"../outside", "repo"}, {"owner", "../../outside"},
	} {
		if err := os.MkdirAll(filepath.Join(cache, "github", coordinate[0], coordinate[1], ".git"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if got := CachedGitHubPath("owner", "repo"); got != filepath.Join(cache, "github", "owner", "repo") {
		t.Fatalf("valid cache missing: %q", got)
	}
	for _, coordinate := range [][2]string{
		{"../outside", "repo"}, {"owner", "../../outside"}, {"owner", ".."},
		{"owner", "repo?query=1"}, {"owner", "repo#fragment"}, {"owner", "%2e%2e"},
	} {
		if got := CachedGitHubPath(coordinate[0], coordinate[1]); got != "" {
			t.Errorf("escaped cache path %q", got)
		}
		if _, err := CloneGitHub(coordinate[0], coordinate[1], "", ""); err == nil {
			t.Errorf("invalid clone coordinate accepted: %q", coordinate)
		}
	}
}
