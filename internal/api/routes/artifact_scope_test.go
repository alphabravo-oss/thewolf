package routes_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestArtifactEndpointsBindRecordedPathsToTheirScan(t *testing.T) {
	e := setupTestEnv(t)
	repoID := e.createRepo(t)
	for _, endpoint := range []string{"download", "log", "json", "manifest"} {
		for _, mode := range []string{"foreign-path", "foreign-key", "file-alias", "hardlink", "scan-directory-alias", "legacy-collision", "valid-legacy"} {
			t.Run(endpoint+"/"+mode, func(t *testing.T) {
				scanID := "12345678" + uuid.NewString()[8:]
				foreignID := "12345678" + uuid.NewString()[8:]
				if err := e.Store.CreateScan(t.Context(), &models.Scan{ID: scanID, UserID: e.UserID, RepoID: repoID, Status: models.ScanStatusCompleted}); err != nil {
					t.Fatal(err)
				}
				kind, name := models.ArtifactJSON, "probe.json"
				if endpoint == "log" {
					kind, name = models.ArtifactLog, "probe.log"
				}
				if endpoint == "manifest" {
					kind, name = models.ArtifactManifest, "manifest.json"
				}
				payload, err := json.Marshal(map[string]any{"scan_id": scanID, "artifact_secret": "private-scan-evidence"})
				if err != nil {
					t.Fatal(err)
				}
				a, err := artifacts.Publish(t.Context(), foreignID, name, strings.NewReader(string(payload)))
				if err != nil {
					t.Fatal(err)
				}
				a.ScanID = scanID
				a.ArtifactType = kind
				switch mode {
				case "foreign-path":
					a.StorageKey = ""
				case "file-alias":
					path := filepath.Join(artifacts.Global.ScanDir(scanID), name)
					if err := os.Symlink(filepath.Join("..", filepath.FromSlash(a.StorageKey)), path); err != nil {
						t.Fatal(err)
					}
					a.FilePath = path
					a.StorageKey = ""
				case "hardlink":
					path := filepath.Join(artifacts.Global.ScanDir(scanID), name)
					if err := os.Link(a.FilePath, path); err != nil {
						t.Fatal(err)
					}
					a.FilePath = path
					a.StorageKey = ""
				case "scan-directory-alias":
					if err := os.Symlink(foreignID, filepath.Join(artifacts.Global.Root(), scanID)); err != nil {
						t.Fatal(err)
					}
					a.StorageKey = strings.Replace(a.StorageKey, foreignID, scanID, 1)
				case "legacy-collision", "valid-legacy":
					dir := filepath.Join(artifacts.Global.Root(), uuid.NewString()+"_20261023-120000_12345678")
					if err := os.Mkdir(dir, 0700); err != nil {
						t.Fatal(err)
					}
					bindingID := foreignID
					if mode == "valid-legacy" {
						bindingID = scanID
					}
					binding, err := json.Marshal(map[string]any{"scan_id": bindingID, "artifact_secret": "private-scan-evidence"})
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, "manifest.json"), binding, 0600); err != nil {
						t.Fatal(err)
					}
					a.FilePath = filepath.Join(dir, name)
					a.StorageKey = ""
					a.ChecksumSHA256 = ""
					if name != "manifest.json" {
						if err := os.WriteFile(a.FilePath, payload, 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				if err := e.Store.CreateScanArtifact(t.Context(), a); err != nil {
					t.Fatal(err)
				}
				path := "/api/scans/" + scanID
				switch endpoint {
				case "download":
					path += "/artifacts/" + a.ID + "/download"
				case "manifest":
					path += "/manifest"
				default:
					path += "/tools/probe/output"
				}
				w := e.doRequest(http.MethodGet, path, nil)
				if mode == "valid-legacy" {
					if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "private-scan-evidence") {
						t.Fatalf("valid legacy evidence: %d %s", w.Code, w.Body.String())
					}
				} else if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "artifact_scope") || strings.Contains(w.Body.String(), "private-scan-evidence") {
					t.Fatalf("unbound artifact: %d %s", w.Code, w.Body.String())
				}
			})
		}
	}
}
