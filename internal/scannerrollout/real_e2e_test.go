package scannerrollout

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

const rolloutE2ERollbackRecoveryLimit = 2 * time.Minute

func TestRealComposeCohortLifecycleAndRollback(t *testing.T) {
	if os.Getenv("WOLF_RUN_ROLLOUT_COMPOSE_E2E") != "1" {
		t.Skip("set WOLF_RUN_ROLLOUT_COMPOSE_E2E=1 via the gated E2E script")
	}
	adapter := requiredE2EEnvironment(t, "WOLF_ROLLOUT_COMPOSE_E2E_ADAPTER")
	newPlan := e2eDeploymentPlan(
		t, "candidate", requiredE2EEnvironment(t, "WOLF_ROLLOUT_E2E_NEW_IMAGE"),
	)
	oldPlan := e2eDeploymentPlan(
		t, "stable", requiredE2EEnvironment(t, "WOLF_ROLLOUT_E2E_OLD_IMAGE"),
	)
	workloadRoot := t.TempDir()
	stateRoot := t.TempDir()
	environment := append(
		os.Environ(), "WOLF_ROLLOUT_COMPOSE_E2E_ROOT="+workloadRoot,
	)
	cache := DockerImageCache{Path: "docker", Environment: environment}
	control := ComposeControl{
		StateRoot: stateRoot,
		Runner: CommandComposeRunner{
			Path: adapter, Environment: environment, MaxOutputBytes: 1 << 20,
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	candidate := e2eAssignment(t, "compose", "candidate-operation", newPlan, false)
	cacheEvidence, err := cache.Prepare(ctx, candidate.OperationID, newPlan)
	if err != nil || !mapsEqual(cacheEvidence.Digests, newPlan.ImageDigests) {
		t.Fatalf("candidate pre-pull = %#v, %v", cacheEvidence, err)
	}
	if err := control.Apply(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = control.Cancel(context.Background(), candidate) }()
	if observed, err := control.Observe(ctx, candidate); err != nil ||
		!observationMatches(candidate, observed) {
		t.Fatalf("candidate observation = %#v, %v", observed, err)
	}
	if err := control.Pause(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Observe(ctx, candidate); err == nil {
		t.Fatal("paused real Compose cohort reported ready")
	}
	if err := control.Resume(ctx, candidate); err != nil {
		t.Fatal(err)
	}

	restored := e2eAssignment(t, "compose", "rollback-operation", oldPlan, true)
	restored.PreviousReleaseID = candidate.ReleaseID
	cacheEvidence, err = cache.Prepare(ctx, restored.OperationID, oldPlan)
	if err != nil || !mapsEqual(cacheEvidence.Digests, oldPlan.ImageDigests) {
		t.Fatalf("rollback pre-pull = %#v, %v", cacheEvidence, err)
	}
	rollbackStarted := time.Now()
	if err := control.Apply(ctx, restored); err != nil {
		t.Fatal(err)
	}
	if observed, err := control.Observe(ctx, restored); err != nil ||
		!observationMatches(restored, observed) {
		t.Fatalf("rollback observation = %#v, %v", observed, err)
	}
	recordE2ERollbackRecovery(t, "compose", rollbackStarted, restored)
	if err := control.Cancel(ctx, restored); err != nil {
		t.Fatal(err)
	}
}

func TestRealKindCohortJobAndRollback(t *testing.T) {
	if os.Getenv("WOLF_RUN_ROLLOUT_KIND_E2E") != "1" {
		t.Skip("set WOLF_RUN_ROLLOUT_KIND_E2E=1 via the gated E2E script")
	}
	baseURL := requiredE2EEnvironment(t, "WOLF_ROLLOUT_KIND_E2E_API")
	namespace := requiredE2EEnvironment(t, "WOLF_ROLLOUT_KIND_E2E_NAMESPACE")
	image := requiredE2EEnvironment(t, "WOLF_ROLLOUT_E2E_NEW_IMAGE")
	oldImage := requiredE2EEnvironment(t, "WOLF_ROLLOUT_E2E_OLD_IMAGE")
	if image == oldImage {
		t.Fatal("Kind rollout E2E requires distinct candidate and stable images")
	}
	config := KubernetesConfig{
		BaseURL: baseURL, Namespace: namespace,
		Token:        requiredE2EEnvironment(t, "WOLF_ROLLOUT_KIND_E2E_TOKEN"),
		CAFile:       requiredE2EEnvironment(t, "WOLF_ROLLOUT_KIND_E2E_CA_FILE"),
		PollInterval: 500 * time.Millisecond, PullTimeout: 5 * time.Minute,
		AllowHTTP: strings.HasPrefix(baseURL, "http://"),
	}
	if err := ValidateKubernetesConfig(config); err != nil {
		t.Fatal(err)
	}
	cache := KubernetesImageCache{Config: config}
	control := KubernetesControl{Config: config}
	plan := e2eDeploymentPlan(t, "candidate", image)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	candidate := e2eAssignment(t, "kind", "candidate-operation", plan, false)
	evidence, err := cache.Prepare(ctx, candidate.OperationID, plan)
	if err != nil || !mapsEqual(evidence.Digests, plan.ImageDigests) {
		t.Fatalf("Kind pre-pull = %#v, %v", evidence, err)
	}
	if err := control.Apply(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	if observed, err := control.Observe(ctx, candidate); err != nil ||
		!observationMatches(candidate, observed) {
		t.Fatalf("Kind candidate observation = %#v, %v", observed, err)
	}
	if err := control.Pause(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Observe(ctx, candidate); err == nil {
		t.Fatal("paused real Kubernetes cohort reported ready")
	}
	if err := control.Resume(ctx, candidate); err != nil {
		t.Fatal(err)
	}

	client, err := newRolloutKubernetesClient(config)
	if err != nil {
		t.Fatal(err)
	}
	fixture := map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{
			"name": "wolf-rollout-e2e-source", "namespace": namespace,
		},
		"data": map[string]string{
			"main.py": "import subprocess\nsubprocess.call('printf rollout-fixture', shell=True)\n",
		},
	}
	if err := client.request(
		ctx, http.MethodPost,
		"/api/v1/namespaces/"+url.PathEscape(namespace)+"/configmaps",
		"application/json", fixture, nil,
	); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = client.request(
			context.Background(), http.MethodDelete,
			"/api/v1/namespaces/"+url.PathEscape(namespace)+
				"/configmaps/wolf-rollout-e2e-source",
			"application/json",
			map[string]any{"apiVersion": "v1", "kind": "DeleteOptions"}, nil,
		)
	}()

	job := map[string]any{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": map[string]any{
			"generateName": "wolf-rollout-e2e-",
			"namespace":    namespace,
		},
		"spec": map[string]any{
			"template": map[string]any{
				"metadata": map[string]any{},
				"spec": map[string]any{
					"restartPolicy":                "Never",
					"automountServiceAccountToken": false,
					"securityContext": map[string]any{
						"runAsNonRoot": true, "runAsUser": 1000, "runAsGroup": 1000,
						"seccompProfile": map[string]any{"type": "RuntimeDefault"},
					},
					"containers": []any{map[string]any{
						"name": "scanner", "image": image,
						"args": []string{"bandit", "-r", "/fixture", "-f", "json", "--exit-zero"},
						"securityContext": map[string]any{
							"allowPrivilegeEscalation": false,
							"readOnlyRootFilesystem":   true,
							"capabilities":             map[string]any{"drop": []string{"ALL"}},
						},
						"volumeMounts": []any{map[string]any{
							"name": "source", "mountPath": "/fixture", "readOnly": true,
						}},
					}},
					"volumes": []any{map[string]any{
						"name":      "source",
						"configMap": map[string]any{"name": "wolf-rollout-e2e-source"},
					}},
				},
			},
		},
	}
	if err := InjectKubernetesJobAssignment(job, candidate); err != nil {
		t.Fatal(err)
	}
	var createdJob struct {
		Metadata struct {
			Name        string            `json:"name"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := client.request(
		ctx, http.MethodPost,
		"/apis/batch/v1/namespaces/"+url.PathEscape(namespace)+"/jobs",
		"application/json", job, &createdJob,
	); err != nil {
		t.Fatal(err)
	}
	if createdJob.Metadata.Annotations["wolf.dev/scanner-release"] !=
		candidate.ReleaseID {
		t.Fatalf("created Job annotations = %#v", createdJob.Metadata.Annotations)
	}
	waitForRealKindScannerJob(t, ctx, client, namespace, createdJob.Metadata.Name)
	defer func() {
		_ = client.request(
			context.Background(), http.MethodDelete,
			"/apis/batch/v1/namespaces/"+url.PathEscape(namespace)+
				"/jobs/"+url.PathEscape(createdJob.Metadata.Name),
			"application/json",
			map[string]any{"apiVersion": "v1", "kind": "DeleteOptions"},
			nil,
		)
	}()

	restoredPlan := e2eDeploymentPlan(t, "stable", oldImage)
	rollbackCacheEvidence, err := cache.Prepare(ctx, "rollback-operation", restoredPlan)
	if err != nil || !mapsEqual(rollbackCacheEvidence.Digests, restoredPlan.ImageDigests) {
		t.Fatalf("Kind rollback pre-pull = %#v, %v", rollbackCacheEvidence, err)
	}
	restored := e2eAssignment(t, "kind", "rollback-operation", restoredPlan, true)
	restored.PreviousReleaseID = candidate.ReleaseID
	if err := control.Cancel(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	rollbackStarted := time.Now()
	if err := control.Apply(ctx, restored); err != nil {
		t.Fatal(err)
	}
	if observed, err := control.Observe(ctx, restored); err != nil ||
		!observationMatches(restored, observed) {
		t.Fatalf("Kind rollback observation = %#v, %v", observed, err)
	}
	recordE2ERollbackRecovery(t, "kind", rollbackStarted, restored)
}

func waitForRealKindScannerJob(
	t *testing.T,
	ctx context.Context,
	client *rolloutKubernetesClient,
	namespace, name string,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for {
		var job struct {
			Status struct {
				Succeeded int `json:"succeeded"`
				Failed    int `json:"failed"`
			} `json:"status"`
		}
		if err := client.request(
			ctx, http.MethodGet,
			"/apis/batch/v1/namespaces/"+url.PathEscape(namespace)+
				"/jobs/"+url.PathEscape(name),
			"", nil, &job,
		); err != nil {
			t.Fatal(err)
		}
		if job.Status.Failed > 0 {
			t.Fatal("real Kind scanner Job failed")
		}
		if job.Status.Succeeded == 1 {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("real Kind scanner Job timed out")
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}

	var pods struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	selector := url.QueryEscape("job-name=" + name)
	if err := client.request(
		ctx, http.MethodGet,
		"/api/v1/namespaces/"+url.PathEscape(namespace)+
			"/pods?labelSelector="+selector,
		"", nil, &pods,
	); err != nil {
		t.Fatal(err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("real Kind scanner Job pods = %d, want 1", len(pods.Items))
	}
	raw, err := client.requestBytes(
		ctx, http.MethodGet,
		"/api/v1/namespaces/"+url.PathEscape(namespace)+
			"/pods/"+url.PathEscape(pods.Items[0].Metadata.Name)+"/log",
	)
	if err != nil {
		t.Fatal(err)
	}
	if start := bytes.IndexByte(raw, '{'); start >= 0 {
		raw = raw[start:]
	}
	var output struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(raw, &output); err != nil || len(output.Results) == 0 {
		t.Fatalf("real Kind scanner output did not contain Bandit findings: %v", err)
	}
}

func e2eDeploymentPlan(t *testing.T, releaseID, reference string) DeploymentPlan {
	t.Helper()
	at := strings.LastIndexByte(reference, '@')
	if at < 1 || !validSyntheticDigest(reference[at+1:]) {
		t.Fatalf("E2E image %q is not an exact sha256 reference", reference)
	}
	digest := reference[at+1:]
	return DeploymentPlan{
		ReleaseID: releaseID, ManifestDigest: digestSynthetic([]byte("manifest-" + releaseID)),
		ImageDigests: map[string]string{"default": digest},
		ImageReferences: map[string]string{
			"default": reference,
		},
	}
}

func e2eAssignment(
	t *testing.T,
	target, operationID string,
	plan DeploymentPlan,
	rollback bool,
) DeploymentAssignment {
	t.Helper()
	return DeploymentAssignment{
		OperationID: operationID, RolloutID: "real-e2e", Target: target,
		CohortID: "real-e2e-canary", CohortName: "canary",
		ReleaseID: plan.ReleaseID, ManifestDigest: plan.ManifestDigest,
		ImageDigests:    cloneStrings(plan.ImageDigests),
		ImageReferences: cloneStrings(plan.ImageReferences),
		CachedDigests:   cloneStrings(plan.ImageDigests),
		Rollback:        rollback, AppliedAt: time.Now().UTC(),
	}
}

func requiredE2EEnvironment(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required by this explicitly enabled E2E test", name)
	}
	return value
}

func recordE2ERollbackRecovery(
	t *testing.T,
	target string,
	started time.Time,
	assignment DeploymentAssignment,
) {
	t.Helper()
	duration := time.Since(started)
	if duration > rolloutE2ERollbackRecoveryLimit {
		t.Fatalf(
			"%s rollback exact-digest recovery took %s, policy limit is %s",
			target, duration, rolloutE2ERollbackRecoveryLimit,
		)
	}
	evidence, err := json.Marshal(map[string]any{
		"schemaVersion":  "wolf.scanners/rollout-e2e-evidence/v1",
		"target":         target,
		"releaseId":      assignment.ReleaseID,
		"operationId":    assignment.OperationID,
		"durationMs":     duration.Milliseconds(),
		"limitMs":        rolloutE2ERollbackRecoveryLimit.Milliseconds(),
		"exactDigests":   assignment.ImageDigests,
		"productionLike": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("rollback recovery evidence: %s", evidence)
}
