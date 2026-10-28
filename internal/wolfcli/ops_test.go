package wolfcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestInitWritesFiles(t *testing.T) {
	dir := t.TempDir()
	if err := writeWolfInit(dir, false); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".wolf.yaml", filepath.Join(".github", "workflows", "wolf.yml")} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
	}
	if err := writeWolfInit(dir, false); err == nil {
		t.Fatal("expected refuse without --force")
	}
	if err := writeWolfInit(dir, true); err != nil {
		t.Fatalf("force: %v", err)
	}
}

func TestBackupRestoreAreRetired(t *testing.T) {
	for _, cmd := range []*cobra.Command{newBackupCmd(), newRestoreCmd()} {
		if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "pg_dump") {
			t.Fatalf("%s: %v", cmd.Name(), err)
		}
	}
}
