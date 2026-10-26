package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPStdioNegotiationAndNotification(t *testing.T) {
	t.Setenv("WOLF_MCP_ENABLED", "1")
	server, token := startServer(t)
	root := newRootForTest()
	root.SetArgs([]string{"mcp", "--server", server, "--token", token})
	root.SetIn(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"stdio-test","version":"1"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"wolf.repos.list","arguments":{}}}
`))
	var output bytes.Buffer
	root.SetOut(&output)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected three responses, no notification response; got %d", len(lines))
	}
	for i, line := range lines {
		var response struct {
			ID     int             `json:"id"`
			Error  json.RawMessage `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatal(err)
		}
		if response.ID != i+1 || len(response.Error) != 0 || len(response.Result) == 0 {
			t.Fatalf("bad RPC response: %s", line)
		}
	}
}

func TestScanAutomationRequestAndTerminalOutcomes(t *testing.T) {
	status := "completed"
	gate := "pass"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if r.Header.Get("Idempotency-Key") != "same-job" || body["client_reference"] != "build-1" || body["source"] == nil || body["profile"] != "pr" {
				t.Errorf("request parity lost: %#v", body)
			}
			fmt.Fprint(w, `{"data":{"id":"scan-1"}}`)
		} else if strings.HasSuffix(r.URL.Path, "/gate") {
			fmt.Fprintf(w, `{"data":{"evaluation":{"status":%q}}}`, gate)
		} else {
			fmt.Fprintf(w, `{"data":{"id":"scan-1","status":%q}}`, status)
		}
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "scan.json")
	if err := os.WriteFile(file, []byte(`{"source":{"kind":"git","url":"https://example.com/repo.git","ref":"abc123"},"profile":"full"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "scan", "create", "--request-file", file, "--profile", "pr", "--client-reference", "build-1", "--idempotency-key", "same-job", "--server", server.URL); err != nil {
		t.Fatal(err)
	}
	for _, status = range []string{"completed", "failed", "cancelled", "unknown"} {
		_, err := run(t, "scan", "wait", "scan-1", "--server", server.URL)
		if (err == nil) != (status == "completed") {
			t.Errorf("wait %s: %v", status, err)
		}
	}
	for _, gate = range []string{"pass", "warn", "pending", "incomplete", "fail", ""} {
		_, err := run(t, "scan", "gate", "scan-1", "--fail-exit-code", "--server", server.URL)
		if (err == nil) != (gate == "pass" || gate == "warn") {
			t.Errorf("gate %q: %v", gate, err)
		}
	}
}

func TestFixCreationCLIForwardsStableKeyAndIntent(t *testing.T) {
	for _, command := range []string{"fix", "agent"} {
		t.Run(command, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/api/v1/fixes" || r.Header.Get("Idempotency-Key") != "fix-request-1" {
					t.Errorf("wrong create boundary: %s %s %q", r.Method, r.URL.Path, r.Header.Get("Idempotency-Key"))
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["repo_id"] != "repo" || body["scan_id"] != "scan" || body["mode"] != "push" {
					t.Errorf("lost explicit publish intent: %+v", body)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"data":{"id":"same-job","mode":"push"}}`)
			}))
			defer server.Close()
			if _, err := run(t, command, "create", "--repo", "repo", "--scan", "scan", "--mode", "push", "--idempotency-key", "fix-request-1", "--server", server.URL); err != nil {
				t.Fatal(err)
			}
		})
	}
}
