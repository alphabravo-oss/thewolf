//go:build integration

// Runs the pinned ZAP image against a local target through the DAST proxy.
// Requires docker and the pinned image (see scanners/tools.yaml):
//
//	go test -tags=integration -run TestZAPBaselineThroughProxy ./plugins/security/
package security

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

func TestZAPBaselineThroughProxy(t *testing.T) {
	const token = "Bearer zap-e2e-secret-7f3c"
	var authorized atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == token {
			authorized.Add(1)
		}
		// No security headers, and the credential reflected, so ZAP has
		// passive alerts to raise and Wolf has a secret to redact.
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><a href="/login">login</a><form action="/login" method="post"><input name="q"></form>` + r.Header.Get("Authorization") + `</body></html>`))
	}))
	defer target.Close()

	cfg := &container.Config{Image: "unused", UpstreamTools: container.DefaultUpstreamTools(), PullPolicy: container.PullNever, Network: "none", AllowNetwork: "bridge", UID: os.Getuid(), GID: os.Getgid(), Memory: "2g", CPUs: "2"}
	var raw []byte
	findings, err := (&ZAPPlugin{}).Execute(t.Context(), models.ExecuteOpts{
		ContainerCfg: cfg,
		Timeout:      10 * time.Minute,
		DAST: &models.DASTExecution{
			Target:  models.DASTTarget{URL: target.URL, Mode: "baseline", ApprovedBy: "admin", AllowPrivate: true},
			Headers: map[string]string{"Authorization": token},
		},
		OnRawOutput: func(data []byte, _ string) { raw = data },
	})
	if err != nil {
		t.Fatal(err)
	}
	if authorized.Load() == 0 {
		t.Fatal("ZAP requests did not carry the approved auth header")
	}
	if len(findings) == 0 {
		t.Fatal("ZAP baseline raised no alerts against a target without security headers")
	}
	for _, f := range findings {
		if f.ToolName != "zap" || !strings.HasPrefix(f.FilePath, target.URL) {
			t.Fatalf("finding not attributed to the approved target: %+v", f)
		}
		if strings.Contains(f.CodeSnippet+f.SARIFData+f.Description, "zap-e2e-secret") {
			t.Fatal("auth secret leaked into a finding")
		}
	}
	if strings.Contains(string(raw), "zap-e2e-secret") {
		t.Fatal("auth secret leaked into the raw report")
	}
}
