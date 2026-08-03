package kubernetes

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
	"sync"
	"testing"
	"time"

	scannercontainer "github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scannerruntime"
)

func TestCreateJobUsesHardenedReadOnlySourceMount(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(workspace, "sources", "repo")
	if err := os.MkdirAll(source, 0o750); err != nil {
		t.Fatal(err)
	}

	var captured map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization header = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()

	client, err := NewClient(testConfig(server.URL, workspace))
	if err != nil {
		t.Fatal(err)
	}
	invocation := scannerruntime.Invocation{
		Image: "scanner:test", Command: []string{"scanner"}, Args: []string{"--json"},
		WorkingDir: scannercontainer.ScanMountPoint, ScanID: "scan-123", ToolName: "semgrep",
		UserID: "user-456", LeaseToken: "lease-789", Attempt: 2,
		NetworkClass: "offline", Memory: "512Mi", CPUs: "500m", Timeout: time.Minute,
		Mounts: []scannerruntime.Mount{{
			Source: source, Target: scannercontainer.ScanMountPoint, ReadOnly: true,
		}},
	}
	jobName, resultDir, err := client.createJob(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(jobName, "wolf-scan-123-semgrep-") {
		t.Fatalf("job name = %q", jobName)
	}
	if !strings.HasPrefix(resultDir, filepath.Join(workspace, ".wolf-results")) {
		t.Fatalf("result dir %q is outside workspace", resultDir)
	}
	labels := nestedMap(t, captured, "metadata", "labels")
	if labels["wolf.dev/user"] != "user-456" || labels["wolf.dev/lease"] != "lease-789" ||
		labels["wolf.dev/attempt"] != "2" {
		t.Fatalf("missing execution identity labels: %#v", labels)
	}

	podSpec := nestedMap(t, captured, "spec", "template", "spec")
	if podSpec["automountServiceAccountToken"] != false {
		t.Fatal("scanner pod must not mount a service-account token")
	}
	security := nestedMap(t, podSpec, "securityContext")
	if security["runAsNonRoot"] != true || security["fsGroup"] != float64(1000) {
		t.Fatalf("unexpected pod security context: %#v", security)
	}
	containers := nestedSlice(t, podSpec, "containers")
	scanner := containers[0].(map[string]interface{})
	containerSecurity := nestedMap(t, scanner, "securityContext")
	if containerSecurity["allowPrivilegeEscalation"] != false || containerSecurity["readOnlyRootFilesystem"] != true {
		t.Fatalf("unexpected scanner security context: %#v", containerSecurity)
	}
	mounts := nestedSlice(t, scanner, "volumeMounts")
	var sourceMount, resultMount map[string]interface{}
	for _, raw := range mounts {
		mount := raw.(map[string]interface{})
		if mount["mountPath"] == scannercontainer.ScanMountPoint {
			sourceMount = mount
		}
		if mount["mountPath"] == "/results" {
			resultMount = mount
		}
	}
	if sourceMount == nil {
		t.Fatal("scanner source mount is missing")
	}
	if sourceMount["readOnly"] != true || sourceMount["subPath"] != "sources/repo" {
		t.Fatalf("unexpected source mount: %#v", sourceMount)
	}
	if resultMount == nil {
		t.Fatal("scanner result mount is missing")
	}
	resultSubPath, ok := resultMount["subPath"].(string)
	if !ok || !strings.HasPrefix(resultSubPath, ".wolf-results/scan-123/semgrep-") {
		t.Fatalf("result mount is not scoped to this Job: %#v", resultMount)
	}
	args := interfaceStrings(t, scanner["args"])
	if got := argumentValue(t, args, "--stdout"); got != "/results/stdout" {
		t.Fatalf("stdout path = %q", got)
	}
	if got := argumentValue(t, args, "--max-output-bytes"); got != "16777216" {
		t.Fatalf("output limit = %q", got)
	}
	resources := nestedMap(t, scanner, "resources")
	limits := nestedMap(t, resources, "limits")
	if limits["memory"] != "512Mi" || limits["cpu"] != "500m" {
		t.Fatalf("unexpected resource limits: %#v", limits)
	}
	volumes := nestedSlice(t, podSpec, "volumes")
	wolfBin := volumes[0].(map[string]interface{})
	emptyDir := nestedMap(t, wolfBin, "emptyDir")
	if emptyDir["medium"] != "Memory" || emptyDir["sizeLimit"] != "128Mi" {
		t.Fatalf("Wolf helper volume must be bounded memory storage: %#v", emptyDir)
	}
}

func TestExecuteReturnsCapturedOutputAndDeletesJob(t *testing.T) {
	workspace := t.TempDir()
	var mu sync.Mutex
	deleted := false
	var createdResultDir string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			var job map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
				t.Fatal(err)
			}
			podSpec := nestedMap(t, job, "spec", "template", "spec")
			scanner := nestedSlice(t, podSpec, "containers")[0].(map[string]interface{})
			mounts := nestedSlice(t, scanner, "volumeMounts")
			var resultSubPath string
			for _, raw := range mounts {
				mount := raw.(map[string]interface{})
				if mount["mountPath"] == "/results" {
					resultSubPath, _ = mount["subPath"].(string)
				}
			}
			if resultSubPath == "" {
				t.Fatal("result subPath is missing")
			}
			resultDir := filepath.Join(workspace, filepath.FromSlash(resultSubPath))
			createdResultDir = resultDir
			if err := os.MkdirAll(resultDir, 0o770); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(resultDir, "stdout"), []byte("scanner output\n"), 0o660); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(resultDir, "stderr"), []byte("scanner warning\n"), 0o660); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(resultDir, "exit-code"), []byte("7\n"), 0o660); err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(w, `{}`)
		case http.MethodGet:
			_, _ = io.WriteString(w, `{"status":{"succeeded":1}}`)
		case http.MethodDelete:
			mu.Lock()
			deleted = true
			mu.Unlock()
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), testConfig(server.URL, workspace), scannerruntime.Invocation{
		Image: "scanner:test", Command: []string{"scanner"}, Args: []string{"--version"},
		WorkingDir: "/tmp", ScanID: "scan-result", ToolName: "tool", Timeout: time.Minute,
	}, &stdout, &stderr)
	if code != 7 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if stdout.String() != "scanner output\n" || stderr.String() != "scanner warning\n" {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
	mu.Lock()
	wasDeleted := deleted
	mu.Unlock()
	if !wasDeleted {
		t.Fatal("completed scanner Job was not deleted")
	}
	if createdResultDir == "" {
		t.Fatal("scanner result directory was not captured")
	}
	if _, err := os.Stat(createdResultDir); !os.IsNotExist(err) {
		t.Fatalf("scanner result directory was not removed: %v", err)
	}
}

func TestCreateJobRejectsSourceOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	client, err := NewClient(testConfig("http://127.0.0.1:1", workspace))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = client.createJob(context.Background(), scannerruntime.Invocation{
		Image: "scanner:test", WorkingDir: scannercontainer.ScanMountPoint, ToolName: "tool",
		Mounts: []scannerruntime.Mount{{Source: outside, Target: scannercontainer.ScanMountPoint, ReadOnly: true}},
	})
	if err == nil || !strings.Contains(err.Error(), "must be inside WOLF_WORKSPACE_ROOT") {
		t.Fatalf("expected workspace boundary error, got %v", err)
	}
}

func TestCreateJobMaterializesStdinForWrapper(t *testing.T) {
	workspace := t.TempDir()
	var args []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var job map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
			t.Fatal(err)
		}
		podSpec := nestedMap(t, job, "spec", "template", "spec")
		scanner := nestedSlice(t, podSpec, "containers")[0].(map[string]interface{})
		args = interfaceStrings(t, scanner["args"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()

	client, err := NewClient(testConfig(server.URL, workspace))
	if err != nil {
		t.Fatal(err)
	}
	_, resultDir, err := client.createJob(context.Background(), scannerruntime.Invocation{
		Image: "scanner:test", Command: []string{"scanner"}, WorkingDir: "/tmp",
		ScanID: "stdin-scan", ToolName: "stdin-tool", Stdin: "scanner input",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdinArgument := argumentValue(t, args, "--stdin")
	if stdinArgument != "/results/stdin" {
		t.Fatalf("stdin wrapper argument = %q", stdinArgument)
	}
	data, err := os.ReadFile(filepath.Join(resultDir, "stdin"))
	if err != nil || string(data) != "scanner input" {
		t.Fatalf("stdin materialization: data=%q err=%v", data, err)
	}
}

func TestExecuteRejectsOversizedResultWithoutForwardingPartialOutput(t *testing.T) {
	workspace := t.TempDir()
	cfg := testConfig("", workspace)
	cfg.MaxOutputBytes = 8
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			var job map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
				t.Fatal(err)
			}
			podSpec := nestedMap(t, job, "spec", "template", "spec")
			scanner := nestedSlice(t, podSpec, "containers")[0].(map[string]interface{})
			var resultSubPath string
			for _, raw := range nestedSlice(t, scanner, "volumeMounts") {
				mount := raw.(map[string]interface{})
				if mount["mountPath"] == "/results" {
					resultSubPath, _ = mount["subPath"].(string)
				}
			}
			resultDir := filepath.Join(workspace, filepath.FromSlash(resultSubPath))
			if err := os.MkdirAll(resultDir, 0o770); err != nil {
				t.Fatal(err)
			}
			for name, contents := range map[string]string{
				"stdout": "012345678", "stderr": "", "exit-code": "0\n",
			} {
				if err := os.WriteFile(filepath.Join(resultDir, name), []byte(contents), 0o660); err != nil {
					t.Fatal(err)
				}
			}
			_, _ = io.WriteString(w, `{}`)
		case http.MethodGet:
			_, _ = io.WriteString(w, `{"status":{"succeeded":1}}`)
		case http.MethodDelete:
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()
	cfg.APIServer = server.URL

	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), cfg, scannerruntime.Invocation{
		Image: "scanner:test", Command: []string{"scanner"}, WorkingDir: "/tmp",
		ScanID: "oversized", ToolName: "tool", Timeout: time.Minute,
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("forwarded partial stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "exceeds 8-byte limit") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestReconcileAbandonedJobsDeletesOnlyStaleOwnership(t *testing.T) {
	workspace := t.TempDir()
	var deleted []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			if !strings.Contains(r.URL.Query().Get("labelSelector"), "wolf-scanner") {
				t.Fatalf("missing scanner label selector: %q", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"items":[
				{"metadata":{"name":"active-job","labels":{"wolf.dev/scan":"active","wolf.dev/lease":"lease-a"}}},
				{"metadata":{"name":"stale-job","labels":{"wolf.dev/scan":"stale","wolf.dev/lease":"lease-b"}}}
			]}`)
		case http.MethodDelete:
			deleted = append(deleted, filepath.Base(r.URL.Path))
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	count, err := ReconcileAbandonedJobs(
		context.Background(),
		testConfig(server.URL, workspace),
		func(_ context.Context, scanID, lease string) (bool, error) {
			return scanID == "active" && lease == "lease-a", nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(deleted) != 1 || deleted[0] != "stale-job" {
		t.Fatalf("deleted count=%d jobs=%#v", count, deleted)
	}
}

func TestRuntimeRefIsDeterministicAndIdentifiesAttempt(t *testing.T) {
	first := RuntimeRef(
		"12345678-aaaa-bbbb-cccc-123456789abc",
		"an-extremely-long-scanner-tool-name-that-needs-truncation",
		3,
		"87654321-dddd-eeee-ffff-123456789abc",
	)
	second := RuntimeRef(
		"12345678-aaaa-bbbb-cccc-123456789abc",
		"an-extremely-long-scanner-tool-name-that-needs-truncation",
		3,
		"87654321-dddd-eeee-ffff-123456789abc",
	)
	if first != second {
		t.Fatalf("RuntimeRef is not stable: %q %q", first, second)
	}
	if !strings.Contains(first, "wolf.dev/attempt=3") || !strings.Contains(first, "wolf.dev/lease=87654321") {
		t.Fatalf("RuntimeRef does not identify ownership: %q", first)
	}
}

func testConfig(apiServer, workspace string) Config {
	return Config{
		APIServer: apiServer, Namespace: "wolf", Token: "test-token",
		WorkspacePVC: "wolf-workspace", WorkspaceRoot: workspace,
		WolfImage: "wolf:test", ServiceAccount: "wolf-scanner",
		PollInterval: time.Millisecond, JobTTLSeconds: 60, DefaultTimeout: time.Minute,
		MaxOutputBytes: DefaultScannerOutputMaxBytes,
		NetworkClass:   "offline", ImagePullPolicy: "IfNotPresent",
	}
}

func nestedMap(t *testing.T, value map[string]interface{}, keys ...string) map[string]interface{} {
	t.Helper()
	current := value
	for _, key := range keys {
		next, ok := current[key].(map[string]interface{})
		if !ok {
			t.Fatalf("%s is not a map in %#v", key, current)
		}
		current = next
	}
	return current
}

func nestedSlice(t *testing.T, value map[string]interface{}, key string) []interface{} {
	t.Helper()
	slice, ok := value[key].([]interface{})
	if !ok {
		t.Fatalf("%s is not a slice in %#v", key, value)
	}
	return slice
}

func interfaceStrings(t *testing.T, value interface{}) []string {
	t.Helper()
	raw, ok := value.([]interface{})
	if !ok {
		t.Fatalf("value is not a string slice: %#v", value)
	}
	result := make([]string, len(raw))
	for i, item := range raw {
		result[i], ok = item.(string)
		if !ok {
			t.Fatalf("item %d is not a string: %#v", i, item)
		}
	}
	return result
}

func argumentValue(t *testing.T, args []string, name string) string {
	t.Helper()
	for index, argument := range args {
		if argument == name && index+1 < len(args) {
			return args[index+1]
		}
	}
	t.Fatalf("argument %s not found in %#v", name, args)
	return ""
}
