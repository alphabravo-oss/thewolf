package scannerreleaseadapter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerreleasebackend"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
)

type testActions struct {
	result ActionResult
	calls  int
}

func (a *testActions) Execute(
	_ context.Context,
	_ scannerreleasebackend.AdapterLane,
	_ scannerreleasebackend.Invocation,
	_ string,
) (ActionResult, error) {
	a.calls++
	return a.result, nil
}

type testPublisher struct {
	request PublishRequest
	calls   int
}

func (p *testPublisher) Publish(_ context.Context, request PublishRequest) (PublishedArtifact, error) {
	p.calls++
	p.request = request
	storage := sha256Digest(append([]byte("oci-manifest:"), request.Payload...))
	return PublishedArtifact{
		URI:    "oci://registry.example/wolf/evidence@" + storage,
		Digest: storage, PayloadDigest: sha256Digest(request.Payload),
		MediaType: request.MediaType, SizeBytes: int64(len(request.Payload)),
		StorageMediaType: ociManifestMediaType, StorageSizeBytes: 256,
		ReadBackVerified: true,
	}, nil
}

func TestRunnerEmitsProductionAdapterContractWithDistinctPayloadAndStorageDigests(t *testing.T) {
	invocation := testInvocation(t, "manifest-validate", scannerpipeline.StepValidation)
	actions := &testActions{result: ActionResult{
		Payload: []byte(`{"validated":true}`),
		Summary: map[string]any{"validated": true},
	}}
	publisher := &testPublisher{}
	var output bytes.Buffer
	if err := (Runner{
		Lane:    scannerreleasebackend.AdapterLaneFixed,
		Actions: actions, Publisher: publisher,
	}).Run(context.Background(), encodeInvocation(t, invocation), &output); err != nil {
		t.Fatal(err)
	}
	if actions.calls != 1 || publisher.calls != 1 {
		t.Fatalf("calls actions=%d publisher=%d", actions.calls, publisher.calls)
	}
	var result scannerreleasebackend.BackendResult
	decodeTestJSON(t, output.Bytes(), &result)
	if result.Binding != invocation.Binding || result.ExternalOperationID != invocation.OperationID {
		t.Fatalf("result binding=%#v operation=%q", result.Binding, result.ExternalOperationID)
	}
	if result.Result.OutputDigest != sha256Digest(actions.result.Payload) {
		t.Fatalf("output digest=%q", result.Result.OutputDigest)
	}
	if result.Result.OutputDigest == strings.TrimPrefix(result.Result.OutputURI[strings.LastIndex(result.Result.OutputURI, "@")+1:], "") {
		t.Fatal("payload digest unexpectedly collapsed into OCI storage-envelope digest")
	}
	if err := scannerreleasebackend.ValidateAdapterResult(
		scannerreleasebackend.AdapterLaneFixed, invocation, result,
	); err != nil {
		t.Fatalf("production adapter validation: %v", err)
	}
}

func TestRunnerBindsSBOMArtifactToExactImageSubject(t *testing.T) {
	invocation := testInvocation(t, "sbom/default", scannerpipeline.StepEvidence)
	subject := "sha256:" + strings.Repeat("c", 64)
	invocation.Request.Dependencies = map[string]scannerreleaseworker.DependencyEvidence{
		"image-manifest/default": {
			OutputURI:    "oci://registry.example/wolf/default@" + subject,
			OutputDigest: subject,
		},
	}
	actions := &testActions{result: ActionResult{
		Payload: []byte(`{"spdxVersion":"SPDX-2.3"}`), MediaType: "application/spdx+json",
		SubjectURI:    invocation.Request.Dependencies["image-manifest/default"].OutputURI,
		SubjectDigest: subject,
	}}
	publisher := &testPublisher{}
	var output bytes.Buffer
	if err := (Runner{
		Lane:    scannerreleasebackend.AdapterLaneQuality,
		Actions: actions, Publisher: publisher,
	}).Run(context.Background(), encodeInvocation(t, invocation), &output); err != nil {
		t.Fatal(err)
	}
	if publisher.request.SubjectDigest != subject || publisher.request.SubjectURI != actions.result.SubjectURI {
		t.Fatalf("publisher subject=%#v", publisher.request)
	}
	var result scannerreleasebackend.BackendResult
	decodeTestJSON(t, output.Bytes(), &result)
	if err := scannerreleasebackend.ValidateAdapterResult(
		scannerreleasebackend.AdapterLaneQuality, invocation, result,
	); err != nil {
		t.Fatalf("production adapter validation: %v", err)
	}
}

func TestRunnerCarriesExactOCIManifestBytesForAnnotationVerification(t *testing.T) {
	invocation := testInvocation(t, "oci-annotations/default", scannerpipeline.StepEvidence)
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":42}],"annotations":{"org.opencontainers.image.source":"https://github.com/alphabravocompany/thewolf"}}`)
	subject := sha256Digest(manifest)
	invocation.Request.Dependencies = map[string]scannerreleaseworker.DependencyEvidence{
		"image-manifest/default": {
			OutputURI: "oci://registry.example/wolf/default@" + subject, OutputDigest: subject,
		},
	}
	actions := &testActions{result: ActionResult{
		Payload: []byte(`{"verified":true}`), MediaType: "application/json",
		SubjectURI:    invocation.Request.Dependencies["image-manifest/default"].OutputURI,
		SubjectDigest: subject, ImageManifestPayload: manifest,
		ImageManifestMediaType: "application/vnd.oci.image.index.v1+json",
	}}
	var output bytes.Buffer
	if err := (Runner{
		Lane: scannerreleasebackend.AdapterLaneQuality, Actions: actions,
		Publisher: &testPublisher{},
	}).Run(context.Background(), encodeInvocation(t, invocation), &output); err != nil {
		t.Fatal(err)
	}
	var result scannerreleasebackend.BackendResult
	decodeTestJSON(t, output.Bytes(), &result)
	evidence := result.Result.Summary["adapter_evidence"].(map[string]any)
	carried := evidence["image_manifest"].(map[string]any)
	if carried["digest"] != subject ||
		carried["payload_base64"] != base64.StdEncoding.EncodeToString(manifest) {
		t.Fatalf("exact OCI manifest evidence = %#v", carried)
	}
}

func TestRunnerReusesTheExactCommittedActionResult(t *testing.T) {
	invocation := testInvocation(t, "manifest-validate", scannerpipeline.StepValidation)
	actions := &testActions{result: ActionResult{
		Payload: []byte(`{"validated":true}`),
		Summary: map[string]any{"validated": true, "count": 1},
	}}
	publisher := &testPublisher{}
	runner := Runner{
		Lane: scannerreleasebackend.AdapterLaneFixed, Actions: actions, Publisher: publisher,
	}
	var first, second bytes.Buffer
	if err := runner.Run(context.Background(), encodeInvocation(t, invocation), &first); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background(), encodeInvocation(t, invocation), &second); err != nil {
		t.Fatal(err)
	}
	if actions.calls != 1 {
		t.Fatalf("committed action executed %d times", actions.calls)
	}
	if publisher.calls != 2 {
		t.Fatalf("publisher recovery was attempted %d times", publisher.calls)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("recovered adapter result changed:\nfirst=%s\nsecond=%s", first.Bytes(), second.Bytes())
	}
}

func TestRunnerPersistsEvidenceLargerThanTheInvocationAndStdoutLimit(t *testing.T) {
	invocation := testInvocation(t, "manifest-validate", scannerpipeline.StepValidation)
	payload := bytes.Repeat([]byte("measured-evidence\n"), (6<<20)/len("measured-evidence\n"))
	actions := &testActions{result: ActionResult{Payload: payload}}
	publisher := &testPublisher{}
	runner := Runner{
		Lane: scannerreleasebackend.AdapterLaneFixed, Actions: actions, Publisher: publisher,
	}
	for range 2 {
		if err := runner.Run(
			context.Background(), encodeInvocation(t, invocation), &bytes.Buffer{},
		); err != nil {
			t.Fatal(err)
		}
	}
	if actions.calls != 1 || !bytes.Equal(publisher.request.Payload, payload) {
		t.Fatalf("large evidence was not durably replayed: action calls=%d payload=%d", actions.calls, len(publisher.request.Payload))
	}
}

func TestActionJournalRejectsChangedInvocationAndMalformedState(t *testing.T) {
	invocation := testInvocation(t, "manifest-validate", scannerpipeline.StepValidation)
	commandID, err := scannerreleasebackend.AdapterCommandID(invocation.Action.Name)
	if err != nil {
		t.Fatal(err)
	}
	result := ActionResult{Payload: []byte(`{"validated":true}`)}
	if err := persistActionJournal(
		scannerreleasebackend.AdapterLaneFixed, invocation, commandID, result,
	); err != nil {
		t.Fatal(err)
	}
	changed := invocation
	changed.Request.CandidateID = "different-candidate"
	if _, _, err := loadActionJournal(
		scannerreleasebackend.AdapterLaneFixed, changed, commandID,
	); err == nil || !strings.Contains(err.Error(), "immutable binding mismatch") {
		t.Fatalf("changed invocation journal error = %v", err)
	}
	journalPath, err := adapterActionJournalPath(invocation)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(journalPath, "result.json"), []byte(`{"schema_version":"broken"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadActionJournal(
		scannerreleasebackend.AdapterLaneFixed, invocation, commandID,
	); err == nil {
		t.Fatal("malformed action journal was accepted")
	}
}

func TestActionJournalRejectsSymlinkedOwnedDirectory(t *testing.T) {
	invocation := testInvocation(t, "manifest-validate", scannerpipeline.StepValidation)
	outside := t.TempDir()
	root := filepath.Join(invocation.Request.Workspace, ".wolf-release-backend-journal")
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	if _, err := adapterActionJournalPath(invocation); err == nil {
		t.Fatal("symlinked action journal directory was accepted")
	}
}

func TestActionJournalRejectsPartialCommittedTransaction(t *testing.T) {
	invocation := testInvocation(t, "manifest-validate", scannerpipeline.StepValidation)
	commandID, err := scannerreleasebackend.AdapterCommandID(invocation.Action.Name)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := adapterActionJournalPath(invocation)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(committed, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(committed, "result.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadActionJournal(
		scannerreleasebackend.AdapterLaneFixed, invocation, commandID,
	); err == nil {
		t.Fatal("partial committed action transaction was accepted")
	}
}

func TestConcurrentIdenticalActionJournalsConverge(t *testing.T) {
	invocation := testInvocation(t, "manifest-validate", scannerpipeline.StepValidation)
	commandID, err := scannerreleasebackend.AdapterCommandID(invocation.Action.Name)
	if err != nil {
		t.Fatal(err)
	}
	result := ActionResult{
		Payload: []byte(`{"validated":true}`), Summary: map[string]any{"validated": true},
	}
	const writers = 16
	errorsByWriter := make(chan error, writers)
	var group sync.WaitGroup
	for range writers {
		group.Add(1)
		go func() {
			defer group.Done()
			errorsByWriter <- persistActionJournal(
				scannerreleasebackend.AdapterLaneFixed, invocation, commandID, result,
			)
		}()
	}
	group.Wait()
	close(errorsByWriter)
	for err := range errorsByWriter {
		if err != nil {
			t.Fatal(err)
		}
	}
	loaded, found, err := loadActionJournal(
		scannerreleasebackend.AdapterLaneFixed, invocation, commandID,
	)
	if err != nil || !found || !bytes.Equal(loaded.Payload, result.Payload) {
		t.Fatalf("converged journal found=%t result=%#v error=%v", found, loaded, err)
	}
}

func TestRunnerRejectsUnknownInputBeforeAction(t *testing.T) {
	invocation := testInvocation(t, "manifest-validate", scannerpipeline.StepValidation)
	value, err := json.Marshal(invocation)
	if err != nil {
		t.Fatal(err)
	}
	value = bytes.Replace(value, []byte(`{"operation_id"`), []byte(`{"unexpected":true,"operation_id"`), 1)
	actions := &testActions{}
	err = (Runner{
		Lane:    scannerreleasebackend.AdapterLaneFixed,
		Actions: actions, Publisher: &testPublisher{},
	}).Run(context.Background(), bytes.NewReader(value), &bytes.Buffer{})
	if err == nil || actions.calls != 0 {
		t.Fatalf("unknown-field error=%v action calls=%d", err, actions.calls)
	}
}

func TestProductionFixedCheckoutExecutesRealGitAndVerifiesCommit(t *testing.T) {
	if _, err := os.Stat(gitPath); err != nil {
		t.Skipf("fixed Git executable is unavailable: %v", err)
	}
	workspace := t.TempDir()
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	lockSource := filepath.Join(repositoryRoot, scannerlock.DefaultLockPath)
	lockValue, err := os.ReadFile(lockSource)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(workspace, scannerlock.DefaultLockPath)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, lockValue, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "adapter-test@example.invalid"},
		{"config", "user.name", "adapter-test"},
		{"add", "scanners/scanner-lock.yaml"},
		{"commit", "--quiet", "-m", "adapter checkout fixture"},
	} {
		command := exec.Command(gitPath, args...)
		command.Dir = workspace
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	command := exec.Command(gitPath, "rev-parse", "HEAD")
	command.Dir = workspace
	commit, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := scannerlock.LoadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	invocation := testInvocationAt(
		t, workspace, "checkout", scannerpipeline.StepCheckout,
		strings.TrimSpace(string(commit)), loaded.LockDigest,
	)
	publisher := &testPublisher{}
	var output bytes.Buffer
	if err := (Runner{
		Lane:    scannerreleasebackend.AdapterLaneFixed,
		Actions: productionActions{}, Publisher: publisher,
	}).Run(context.Background(), encodeInvocation(t, invocation), &output); err != nil {
		t.Fatal(err)
	}
	var result scannerreleasebackend.BackendResult
	decodeTestJSON(t, output.Bytes(), &result)
	if err := scannerreleasebackend.ValidateAdapterResult(
		scannerreleasebackend.AdapterLaneFixed, invocation, result,
	); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteEngineConfigurationIsStrictAndComplete(t *testing.T) {
	directory := t.TempDir()
	policyDigest := "sha256:" + strings.Repeat("a", 64)
	writeTestFile(t, filepath.Join(directory, "engine.json"), []byte(
		`{"schema_version":"wolf.scanner-release-engine/v1","host":"tcp://engine.example:2376","quality_network":"wolf-quality-fixtures","quality_network_policy_digest":"`+policyDigest+`","quality_targets":{"nuclei":"http://wolf-quality-nuclei:8080/"}}`,
	))
	for _, name := range []string{"ca.pem", "cert.pem", "key.pem", "config.json"} {
		writeTestFile(t, filepath.Join(directory, name), []byte("bounded-test-value"))
	}
	configuration, err := readEngineConfig(directory)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Host != "tcp://engine.example:2376" {
		t.Fatalf("remote engine host = %q", configuration.Host)
	}
	if configuration.QualityNetwork != "wolf-quality-fixtures" || configuration.QualityNetworkPolicyDigest != policyDigest {
		t.Fatalf("remote engine quality network = %#v", configuration)
	}
	if configuration.QualityTargets["nuclei"] != "http://wolf-quality-nuclei:8080/" {
		t.Fatalf("remote engine quality targets = %#v", configuration.QualityTargets)
	}

	writeTestFile(t, filepath.Join(directory, "engine.json"), []byte(
		`{"schema_version":"wolf.scanner-release-engine/v1","host":"unix:///var/run/docker.sock"}`,
	))
	if _, err := readEngineConfig(directory); err == nil {
		t.Fatal("local Docker socket was accepted as a managed engine")
	}
	writeTestFile(t, filepath.Join(directory, "engine.json"), []byte(
		`{"schema_version":"wolf.scanner-release-engine/v1","host":"tcp://user:secret@engine.example:2376"}`,
	))
	if _, err := readEngineConfig(directory); err == nil {
		t.Fatal("credential-bearing Docker engine URL was accepted")
	}
	writeTestFile(t, filepath.Join(directory, "engine.json"), []byte(
		`{"schema_version":"wolf.scanner-release-engine/v1","host":"tcp://engine.example:2376","quality_network":"wolf-quality-fixtures"}`,
	))
	if _, err := readEngineConfig(directory); err == nil {
		t.Fatal("quality network without an exact policy digest was accepted")
	}
	for _, target := range []string{
		"https://wolf-quality-nuclei:8080/",
		"http://127.0.0.1:8080/",
		"http://wolf-quality-nuclei:80/",
		"http://wolf-quality-nuclei:8080/path",
	} {
		writeTestFile(t, filepath.Join(directory, "engine.json"), []byte(
			`{"schema_version":"wolf.scanner-release-engine/v1","host":"tcp://engine.example:2376","quality_network":"wolf-quality-fixtures","quality_network_policy_digest":"`+policyDigest+`","quality_targets":{"nuclei":"`+target+`"}}`,
		))
		if _, err := readEngineConfig(directory); err == nil {
			t.Fatalf("unsafe quality target %q was accepted", target)
		}
	}
}

func TestRemoteEngineAcceptsProjectedSecretAndRejectsEscape(t *testing.T) {
	directory := t.TempDir()
	versionDirectory := filepath.Join(directory, "..2026_07_30_22_45_00")
	if err := os.Mkdir(versionDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(versionDirectory, "engine.json"), []byte(
		`{"schema_version":"wolf.scanner-release-engine/v1","host":"tcp://engine.example:2376"}`,
	))
	for _, name := range []string{"ca.pem", "cert.pem", "key.pem", "config.json"} {
		writeTestFile(t, filepath.Join(versionDirectory, name), []byte("bounded-test-value"))
	}
	if err := os.Symlink(filepath.Base(versionDirectory), filepath.Join(directory, "..data")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"engine.json", "ca.pem", "cert.pem", "key.pem", "config.json"} {
		if err := os.Symlink(filepath.Join("..data", name), filepath.Join(directory, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := readEngineConfig(directory); err != nil {
		t.Fatalf("Kubernetes projected Secret layout was rejected: %v", err)
	}
	if _, err := dockerCredentialFile(directory); err != nil {
		t.Fatalf("projected registry configuration was rejected: %v", err)
	}

	if err := os.Remove(filepath.Join(directory, "key.pem")); err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(t.TempDir(), "outside-key.pem")
	writeTestFile(t, escape, []byte("bounded-test-value"))
	if err := os.Symlink(escape, filepath.Join(directory, "key.pem")); err != nil {
		t.Fatal(err)
	}
	if _, err := readEngineConfig(directory); err == nil {
		t.Fatal("credential symlink escaping the projected volume was accepted")
	}
}

func TestSafeDockerEnvironmentSeparatesRegistryAndEngineCredentials(t *testing.T) {
	registry := t.TempDir()
	engine := t.TempDir()
	writeTestFile(t, filepath.Join(registry, "config.json"), []byte(`{"auths":{"registry.example":{"auth":"canary-registry-only"}}}`))
	writeTestFile(t, filepath.Join(engine, "engine.json"), []byte(
		`{"schema_version":"wolf.scanner-release-engine/v1","host":"tcp://engine.example:2376"}`,
	))
	for _, name := range []string{"ca.pem", "cert.pem", "key.pem"} {
		writeTestFile(t, filepath.Join(engine, name), []byte("canary-engine-only-"+name))
	}
	t.Setenv("WOLF_SCANNER_RELEASE_REGISTRY_CREDENTIAL_DIR", registry)
	t.Setenv("WOLF_SCANNER_RELEASE_ENGINE_CREDENTIAL_DIR", engine)

	environment, err := safeEnvironment(dockerPath)
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]string)
	for _, item := range environment {
		name, value, _ := strings.Cut(item, "=")
		values[name] = value
		if strings.Contains(item, "canary-registry-only") || strings.Contains(item, "canary-engine-only") {
			t.Fatalf("credential content leaked into command environment: %q", item)
		}
	}
	if values["DOCKER_CONFIG"] != registry || values["DOCKER_CERT_PATH"] != engine ||
		values["DOCKER_HOST"] != "tcp://engine.example:2376" || values["DOCKER_TLS_VERIFY"] != "1" {
		t.Fatalf("split Docker environment = %#v", values)
	}
	if _, exists := values["WOLF_SCANNER_RELEASE_REGISTRY_CREDENTIAL_DIR"]; exists {
		t.Fatal("registry credential mount path was forwarded under its internal name")
	}
	if _, exists := values["WOLF_SCANNER_RELEASE_ENGINE_CREDENTIAL_DIR"]; exists {
		t.Fatal("engine credential mount path was forwarded under its internal name")
	}

	t.Setenv("WOLF_SCANNER_RELEASE_REGISTRY_CREDENTIAL_DIR", engine)
	if _, err := safeEnvironment(dockerPath); err == nil {
		t.Fatal("engine credential directory was accepted as a registry configuration")
	}
}

func TestImmutableStepEvidenceAcceptsDistinctAdapterStorageEnvelope(t *testing.T) {
	payload := "sha256:" + strings.Repeat("a", 64)
	storage := "sha256:" + strings.Repeat("b", 64)
	result := scannerreleaseworker.StepResult{
		OutputURI: "oci://registry.example/wolf/evidence@" + storage, OutputDigest: payload,
		Summary: map[string]any{"adapter_evidence": map[string]any{
			"output_identity": "payload",
			"artifact": map[string]any{
				"uri":    "oci://registry.example/wolf/evidence@" + storage,
				"digest": storage, "payload_digest": payload,
				"storage_media_type": ociManifestMediaType,
				"storage_size_bytes": 256,
			},
		}},
	}
	if !immutableStepEvidence(result) {
		t.Fatal("valid adapter payload/storage identity was rejected")
	}
	result.Summary["adapter_evidence"].(map[string]any)["artifact"].(map[string]any)["payload_digest"] =
		"sha256:" + strings.Repeat("c", 64)
	if immutableStepEvidence(result) {
		t.Fatal("mismatched adapter payload identity was accepted")
	}
}

func writeTestFile(t *testing.T, path string, value []byte) {
	t.Helper()
	if err := os.WriteFile(path, value, 0o600); err != nil && !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
}

func testInvocation(t *testing.T, key string, kind scannerpipeline.StepKind) scannerreleasebackend.Invocation {
	t.Helper()
	return testInvocationAt(
		t, t.TempDir(), key, kind, strings.Repeat("a", 40),
		"sha256:"+strings.Repeat("b", 64),
	)
}

func testInvocationAt(
	t *testing.T,
	workspace, key string,
	kind scannerpipeline.StepKind,
	commit, lockDigest string,
) scannerreleasebackend.Invocation {
	t.Helper()
	request := scannerreleaseworker.StepRequest{
		BuildRunID: "build-adapter", CandidateID: "candidate-adapter",
		BuildAttempt: 1, Step: scannerpipeline.Step{
			Key: key, Kind: kind, Timeout: 5 * time.Minute, Required: true,
		},
		StepAttempt: 1, Workspace: workspace,
		DefinitionCommit: commit, LockDigest: lockDigest,
		PolicyID: "policy-adapter", PolicyRevision: 1,
		PlatformsJSON: `[{"key":"default","kind":"scanner","platforms":["linux/amd64","linux/arm64"]}]`,
	}
	request.LogicalOperationID = scannerreleaseworker.DeriveLogicalOperationID(request)
	invocation, err := scannerreleasebackend.PrepareInvocation(request)
	if err != nil {
		t.Fatal(err)
	}
	return invocation
}

func encodeInvocation(t *testing.T, invocation scannerreleasebackend.Invocation) *bytes.Reader {
	t.Helper()
	value, err := json.Marshal(invocation)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(value)
}

func decodeTestJSON(t *testing.T, value []byte, target any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatal(err)
	}
}
