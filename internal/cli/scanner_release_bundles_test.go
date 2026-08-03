package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScannerReleaseBundleCLIStreamsExportAndImport(t *testing.T) {
	bundle := bytes.Repeat([]byte("portable-bundle-data"), 32*1024)
	var imported []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet &&
			r.URL.Path == "/api/v1/scanner-supply-chain/releases/release-1/export":
			if r.URL.Query().Get("bundle_version") != "2" {
				http.Error(w, "expected complete v2 export", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", scannerReleaseBundleMediaType)
			w.Header().Set("X-Wolf-Manifest-Digest", "sha256:manifest")
			w.Header().Set("X-Wolf-Bundle-Signature-Status", "signed-ed25519")
			_, _ = w.Write(bundle)
		case r.Method == http.MethodPost &&
			r.URL.Path == "/api/v1/scanner-supply-chain/release-imports":
			if r.Header.Get("Idempotency-Key") != "transfer-1" ||
				r.Header.Get("X-Wolf-Import-Reason") != "approved transfer" {
				http.Error(w, "missing import headers", http.StatusBadRequest)
				return
			}
			if r.Header.Get("Content-Type") != scannerReleaseBundleMediaType {
				http.Error(w, "wrong content type", http.StatusUnsupportedMediaType)
				return
			}
			if r.URL.Query().Get("no_network") != "true" {
				http.Error(w, "expected no-network import", http.StatusBadRequest)
				return
			}
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			imported = raw
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				"release_id": "release-1", "integrity_verified": true,
				"signature_status": "verified-ed25519",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	destination := filepath.Join(t.TempDir(), "release.tar.zst")
	output, err := run(
		t, "scanner", "release", "export", "release-1",
		"--file", destination, "--server", server.URL,
	)
	if err != nil {
		t.Fatalf("export command: %v", err)
	}
	if !strings.Contains(output, "signed-ed25519") ||
		!strings.Contains(output, "sha256:manifest") {
		t.Fatalf("export output = %q", output)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, bundle) {
		t.Fatalf("exported bundle length = %d, want %d", len(got), len(bundle))
	}
	if _, err := run(
		t, "scanner", "release", "export", "release-1",
		"--file", destination, "--server", server.URL,
	); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second export error = %v", err)
	}

	output, err = run(
		t, "scanner", "release", "import", destination,
		"--reason", "approved transfer", "--idempotency-key", "transfer-1",
		"--no-network", "--server", server.URL, "--output", "json",
	)
	if err != nil {
		t.Fatalf("import command: %v", err)
	}
	if !bytes.Equal(imported, bundle) {
		t.Fatalf("imported bundle length = %d, want %d", len(imported), len(bundle))
	}
	if !strings.Contains(output, `"integrity_verified": true`) ||
		!strings.Contains(output, `"signature_status": "verified-ed25519"`) {
		t.Fatalf("import output = %q", output)
	}
}

func TestClientTransferErrorsPreserveAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"code":"release_bundle_signature_rejected","message":"key is not trusted"}}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "")

	var output bytes.Buffer
	_, err := client.Download(context.Background(), "/download", &output)
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.StatusCode != http.StatusUnprocessableEntity ||
		apiErr.Code != "release_bundle_signature_rejected" {
		t.Fatalf("download error = %#v", err)
	}
	_, err = client.Upload(
		context.Background(), http.MethodPost, "/upload", scannerReleaseBundleMediaType,
		strings.NewReader("bundle"), 6, nil,
	)
	apiErr, ok = err.(*APIError)
	if !ok || apiErr.Code != "release_bundle_signature_rejected" {
		t.Fatalf("upload error = %#v", err)
	}
}

func TestClientDownloadRejectsBundleDigestMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Wolf-Bundle-Digest", "sha256:"+strings.Repeat("0", 64))
		_, _ = w.Write([]byte("bundle"))
	}))
	defer server.Close()
	client := NewClient(server.URL, "")
	var output bytes.Buffer
	if _, err := client.Download(context.Background(), "/bundle", &output); err == nil ||
		!strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("download error = %v", err)
	}
}
