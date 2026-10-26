package artifacts

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestArtifactReadsStayWithinTheirScan(t *testing.T) {
	previous := Global
	t.Cleanup(func() { Global = previous })
	if err := Init(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	foreign, err := Publish(t.Context(), "foreign-scan", "probe.log", strings.NewReader("foreign secret"))
	if err != nil {
		t.Fatal(err)
	}
	mine, err := Publish(t.Context(), "owned-scan", "probe.log", strings.NewReader("own evidence"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", ".", "..", "a/b", `a\b`, "bad\x00id"} {
		bad := *mine
		bad.ScanID = id
		if data, err := ReadArtifact(bad); err == nil || len(data) != 0 {
			t.Fatalf("invalid scan ID accepted: %q, %q %v", id, data, err)
		}
	}
	for _, key := range []bool{false, true} {
		t.Run(map[bool]string{false: "absolute path", true: "storage key"}[key], func(t *testing.T) {
			bad := *foreign
			bad.ScanID = mine.ScanID
			if !key {
				bad.StorageKey = ""
			}
			if data, err := ReadArtifact(bad); err == nil || len(data) != 0 {
				t.Fatalf("cross-scan read: %q %v", data, err)
			}
		})
	}
	for _, alias := range []struct {
		name, target string
		allow        bool
	}{
		{"safe.log", filepath.Base(filepath.Dir(mine.FilePath)) + "/probe.log", true},
		{"foreign.log", filepath.Join("..", filepath.FromSlash(foreign.StorageKey)), false},
		{"foreign-dir", filepath.Join("..", foreign.ScanID, filepath.Base(filepath.Dir(foreign.FilePath))), false},
	} {
		t.Run(alias.name, func(t *testing.T) {
			path := filepath.Join(Global.Root(), mine.ScanID, alias.name)
			if err := os.Symlink(alias.target, path); err != nil {
				t.Fatal(err)
			}
			if alias.name == "foreign-dir" {
				path = filepath.Join(path, "probe.log")
			}
			a := models.ScanArtifact{ID: "legacy", ScanID: mine.ScanID, FilePath: path}
			data, err := ReadArtifact(a)
			if alias.allow {
				if err != nil || string(data) != "own evidence" {
					t.Fatalf("same-scan link: %q %v", data, err)
				}
			} else if err == nil || len(data) != 0 {
				t.Fatalf("foreign link read: %q %v", data, err)
			}
		})
	}
	// A hardlink does not traverse a path, so root confinement alone cannot reject it.
	hardlink := filepath.Join(Global.Root(), mine.ScanID, "hardlink.log")
	if err := os.Link(foreign.FilePath, hardlink); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadArtifact(models.ScanArtifact{ID: "hardlink", ScanID: mine.ScanID, FilePath: hardlink}); err == nil || len(data) != 0 {
		t.Fatalf("hardlink read: %q %v", data, err)
	}
	if err := os.Remove(hardlink); err != nil {
		t.Fatal(err)
	}
	// A per-scan directory itself must not alias a sibling, for reads or writes.
	if err := os.Symlink(foreign.ScanID, filepath.Join(Global.Root(), "alias-scan")); err != nil {
		t.Fatal(err)
	}
	bad := *foreign
	bad.ScanID = "alias-scan"
	bad.StorageKey = strings.Replace(foreign.StorageKey, foreign.ScanID, "alias-scan", 1)
	if data, err := ReadArtifact(bad); err == nil || len(data) != 0 {
		t.Fatalf("directory alias read: %q %v", data, err)
	}
	before, err := os.ReadDir(filepath.Join(Global.Root(), foreign.ScanID))
	if err != nil {
		t.Fatal(err)
	}
	if a, err := Publish(t.Context(), "alias-scan", "probe.log", strings.NewReader("intrusion")); err == nil || a != nil {
		t.Fatalf("directory alias publication: %+v %v", a, err)
	}
	after, err := os.ReadDir(filepath.Join(Global.Root(), foreign.ScanID))
	if err != nil || len(after) != len(before) {
		t.Fatalf("foreign scan mutated: %d -> %d, %v", len(before), len(after), err)
	}
}

func TestLegacyNamedArtifactRequiresFullManifestIdentity(t *testing.T) {
	previous := Global
	t.Cleanup(func() { Global = previous })
	if err := Init(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	const scanID = "12345678-aaaa-bbbb-cccc-123456789abc"
	for _, tc := range []struct {
		name, manifest string
		allow          bool
	}{
		{"matching", `{"scan_id":"` + scanID + `"}`, true},
		{"same-prefix-foreign", `{"scan_id":"12345678-eeee-bbbb-cccc-123456789abc"}`, false},
		{"missing", "", false},
		{"empty", "{}", false},
		{"malformed", "{", false},
		{"trailing", `{"scan_id":"` + scanID + `"}{}`, false},
		{"oversized", `{"scan_id":"` + scanID + `","padding":"` + strings.Repeat("x", 1<<20) + `"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(Global.Root(), tc.name+"_20261023-120000_12345678")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if tc.manifest != "" {
				if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(tc.manifest), 0600); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(dir, "probe.log")
			if err := os.WriteFile(path, []byte("legacy evidence"), 0600); err != nil {
				t.Fatal(err)
			}
			data, err := ReadArtifact(models.ScanArtifact{ID: "legacy", ScanID: scanID, FilePath: path})
			if tc.allow {
				if err != nil || string(data) != "legacy evidence" {
					t.Fatalf("legacy read: %q %v", data, err)
				}
			} else if err == nil || len(data) != 0 {
				t.Fatalf("unbound legacy read: %q %v", data, err)
			}
		})
	}
}

func TestScanArtifactRootRemainsPinnedOnRename(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"mine", "foreign"} {
		if err := os.Mkdir(filepath.Join(base, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, name, "probe"), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	parent, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	root, err := openScanRoot(parent, "mine")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Rename(filepath.Join(base, "mine"), filepath.Join(base, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("foreign", filepath.Join(base, "mine")); err != nil {
		t.Fatal(err)
	}
	f, err := openRegular(root, "probe")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	closeErr := f.Close()
	if err != nil || closeErr != nil || string(data) != "mine" {
		t.Fatalf("pinned read: %q %v %v", data, err, closeErr)
	}
	if err := root.WriteFile("new", []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "foreign", "new")); !os.IsNotExist(err) {
		t.Fatalf("foreign write: %v", err)
	}
	if replacement, err := openScanRoot(parent, "mine"); err == nil {
		replacement.Close()
		t.Fatal("replacement alias accepted")
	}
}
