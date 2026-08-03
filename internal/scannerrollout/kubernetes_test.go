package scannerrollout

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestKubernetesControlPrePullsAppliesReadsBackAndRestoresDigests(t *testing.T) {
	t.Parallel()
	cluster := &fakeRolloutCluster{controls: make(map[string]string)}
	server := httptest.NewServer(cluster)
	t.Cleanup(server.Close)
	config := KubernetesConfig{
		BaseURL: server.URL, Namespace: "wolf", HTTP: server.Client(),
		PollInterval: time.Millisecond, PullTimeout: time.Second, AllowHTTP: true,
	}
	cache := KubernetesImageCache{Config: config}
	control := KubernetesControl{Config: config}
	now := time.Now().UTC()
	newAssignment := deploymentTestAssignment("new", "operation-new", now)
	newPlan := DeploymentPlan{
		ReleaseID:       newAssignment.ReleaseID,
		ManifestDigest:  newAssignment.ManifestDigest,
		ImageDigests:    newAssignment.ImageDigests,
		ImageReferences: newAssignment.ImageReferences,
	}
	verification, err := cache.Prepare(
		context.Background(), newAssignment.OperationID, newPlan,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !mapsEqual(verification.Digests, newAssignment.ImageDigests) ||
		verification.VerifiedAt.IsZero() || cluster.pullCreates != 1 ||
		cluster.pullDeletes != 1 {
		t.Fatalf("cache verification=%#v cluster=%#v", verification, cluster)
	}
	if err := control.Apply(context.Background(), newAssignment); err != nil {
		t.Fatal(err)
	}
	observation, err := control.Observe(context.Background(), newAssignment)
	if err != nil || !observationMatches(newAssignment, observation) {
		t.Fatalf("observation=%#v err=%v", observation, err)
	}
	if cluster.deploymentPatches != 1 {
		t.Fatalf("deployment patches = %d", cluster.deploymentPatches)
	}
	if err := control.Pause(context.Background(), newAssignment); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Observe(context.Background(), newAssignment); err == nil {
		t.Fatal("paused Kubernetes cohort reported ready")
	}
	if err := control.Resume(context.Background(), newAssignment); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Observe(context.Background(), newAssignment); err != nil {
		t.Fatal(err)
	}
	if err := control.Cancel(context.Background(), newAssignment); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Observe(context.Background(), newAssignment); err == nil {
		t.Fatal("cancelled Kubernetes cohort reported ready")
	}

	oldAssignment := deploymentTestAssignment("old", "operation-old", now.Add(time.Minute))
	oldAssignment.Rollback = true
	oldAssignment.PreviousReleaseID = "new"
	if err := control.Apply(context.Background(), oldAssignment); err != nil {
		t.Fatal(err)
	}
	observation, err = control.Observe(context.Background(), oldAssignment)
	if err != nil || observation.ReleaseID != "old" ||
		!mapsEqual(observation.ImageDigests, oldAssignment.ImageDigests) {
		t.Fatalf("rollback observation=%#v err=%v", observation, err)
	}
}

func TestKubernetesImageIDDigestAcceptsRuntimeStatusShapes(t *testing.T) {
	t.Parallel()
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, value := range []string{
		digest,
		"docker://" + digest,
		"containerd://" + digest,
		"cri-o://" + digest,
		"docker.io/library/alpine@" + digest,
		"docker-pullable://docker.io/library/alpine@" + digest,
	} {
		got, err := kubernetesImageIDDigest(value)
		if err != nil || got != digest {
			t.Fatalf("kubernetesImageIDDigest(%q) = %q, %v", value, got, err)
		}
	}
	for _, value := range []string{
		"",
		"docker.io/library/alpine:latest",
		"unknown://docker.io/library/alpine@" + digest,
		"docker.io/library/alpine@" + digest + "@" + digest,
		"docker.io/library/alpine@sha256:short",
	} {
		if got, err := kubernetesImageIDDigest(value); err == nil {
			t.Fatalf("kubernetesImageIDDigest(%q) accepted %q", value, got)
		}
	}
}

func TestKubernetesPrePullRejectsMissingMismatchedOrAmbiguousImageID(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	assignment := deploymentTestAssignment("new", "operation-new", now)
	plan := DeploymentPlan{
		ReleaseID:       assignment.ReleaseID,
		ManifestDigest:  assignment.ManifestDigest,
		ImageDigests:    assignment.ImageDigests,
		ImageReferences: assignment.ImageReferences,
	}
	wrongDigest := "sha256:" + strings.Repeat("f", 64)
	cases := []struct {
		name    string
		imageID string
		want    string
	}{
		{name: "missing", imageID: "", want: "returned no imageID"},
		{
			name:    "mismatch",
			imageID: "docker.io/library/alpine@" + wrongDigest,
			want:    "digest mismatch",
		},
		{
			name: "ambiguous",
			imageID: "docker.io/library/alpine@" + wrongDigest +
				"@" + assignment.ImageDigests["default"],
			want: "ambiguous digests",
		},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			cluster := &fakeRolloutCluster{
				controls:              make(map[string]string),
				statusImageIDOverride: &testCase.imageID,
			}
			server := httptest.NewServer(cluster)
			t.Cleanup(server.Close)
			cache := KubernetesImageCache{Config: KubernetesConfig{
				BaseURL: server.URL, Namespace: "wolf", HTTP: server.Client(),
				PollInterval: time.Millisecond, PullTimeout: time.Second,
				AllowHTTP: true,
			}}
			_, err := cache.Prepare(context.Background(), assignment.OperationID, plan)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("Prepare() error = %v, want substring %q", err, testCase.want)
			}
		})
	}
}

func TestInjectKubernetesJobAssignmentRejectsMutableOrWrongDigest(t *testing.T) {
	t.Parallel()
	assignment := deploymentTestAssignment("new", "operation-new", time.Now().UTC())
	job := func(image string) map[string]any {
		return map[string]any{
			"metadata": map[string]any{"name": "scanner-job"},
			"spec": map[string]any{"template": map[string]any{
				"metadata": map[string]any{},
				"spec": map[string]any{"containers": []any{
					map[string]any{"name": "scanner", "image": image},
				}},
			}},
		}
	}
	for _, image := range []string{
		"registry.example/wolf/scanners:latest",
		"registry.example/wolf/scanners@sha256:" + strings.Repeat("f", 64),
		"registry.example/wolf bad/scanners@" +
			assignment.ImageDigests["default"],
	} {
		if err := InjectKubernetesJobAssignment(job(image), assignment); err == nil {
			t.Fatalf("mutable/wrong image %q was accepted", image)
		}
	}
	exact := job(assignment.ImageReferences["default"])
	container := exact["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	container["env"] = []any{
		map[string]any{"name": "EXISTING", "value": "preserved"},
		map[string]any{"name": "WOLF_SCANNER_RELEASE_ID", "value": "stale"},
	}
	if err := InjectKubernetesJobAssignment(exact, assignment); err != nil {
		t.Fatal(err)
	}
	metadata := exact["metadata"].(map[string]any)
	annotations := metadata["annotations"].(map[string]string)
	if annotations["wolf.dev/scanner-release"] != assignment.ReleaseID ||
		annotations["wolf.dev/scanner-manifest-digest"] != assignment.ManifestDigest {
		t.Fatalf("Job annotations = %#v", annotations)
	}
	environment := container["env"].([]any)
	encodedEnvironment, err := json.Marshal(environment)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encodedEnvironment), `"EXISTING","value":"preserved"`) ||
		!strings.Contains(
			string(encodedEnvironment),
			`"WOLF_SCANNER_RELEASE_ID","value":"new"`,
		) ||
		strings.Contains(string(encodedEnvironment), `"value":"stale"`) {
		t.Fatalf("Job environment = %s", encodedEnvironment)
	}
}

type fakeRolloutCluster struct {
	mu                    sync.Mutex
	assignment            DeploymentAssignment
	control               string
	controls              map[string]string
	podContainers         []fakePodContainer
	statusImageIDOverride *string
	pullCreates           int
	pullDeletes           int
	deploymentPatches     int
}

type fakePodContainer struct {
	Name  string `json:"name"`
	Image string `json:"image"`
}

func (c *fakeRolloutCluster) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost &&
		r.URL.Path == "/api/v1/namespaces/wolf/pods":
		var pod struct {
			Spec struct {
				Containers []fakePodContainer `json:"containers"`
			} `json:"spec"`
		}
		if err := json.NewDecoder(r.Body).Decode(&pod); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c.podContainers = pod.Spec.Containers
		c.pullCreates++
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	case r.Method == http.MethodGet &&
		strings.HasPrefix(r.URL.Path, "/api/v1/namespaces/wolf/pods/"):
		statuses := make([]map[string]any, 0, len(c.podContainers))
		for _, container := range c.podContainers {
			digest := container.Image[strings.LastIndexByte(container.Image, '@')+1:]
			imageID := "docker-pullable://" +
				container.Image[:strings.LastIndexByte(container.Image, '@')] +
				"@" + digest
			if c.statusImageIDOverride != nil {
				imageID = *c.statusImageIDOverride
			}
			statuses = append(statuses, map[string]any{
				"name": container.Name,
				// containerd commonly normalizes status.image to a local image
				// ID; requested-reference identity comes from the Pod we created.
				"image":   "sha256:" + strings.Repeat("b", 64),
				"imageID": imageID,
				"state":   map[string]any{},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": map[string]any{"containerStatuses": statuses},
		})
	case r.Method == http.MethodDelete &&
		strings.HasPrefix(r.URL.Path, "/api/v1/namespaces/wolf/pods/"):
		c.pullDeletes++
		_, _ = io.WriteString(w, `{}`)
	case r.Method == http.MethodPatch &&
		strings.Contains(r.URL.Path, "/configmaps/"):
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if data, ok := payload["data"].(map[string]any); ok {
			if raw, exists := data["assignment.json"].(string); exists {
				if err := json.Unmarshal([]byte(raw), &c.assignment); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
			}
			if control, exists := data["control"].(string); exists {
				c.control = control
			}
		}
		_, _ = io.WriteString(w, `{}`)
	case r.Method == http.MethodGet &&
		strings.Contains(r.URL.Path, "/configmaps/"):
		raw, _ := json.Marshal(c.assignment)
		images, _ := json.Marshal(c.assignment.ImageDigests)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{
			"assignment.json": string(raw), "control": c.control,
			"release_id":         c.assignment.ReleaseID,
			"manifest_digest":    c.assignment.ManifestDigest,
			"image_digests.json": string(images),
			"operation_id":       c.assignment.OperationID,
		}})
	case r.Method == http.MethodGet &&
		r.URL.Path == "/apis/apps/v1/namespaces/wolf/deployments":
		annotations := map[string]string{}
		observed := int64(0)
		updated, available := 0, 0
		if c.deploymentPatches > 0 {
			annotations = map[string]string{
				"wolf.dev/scanner-release":              c.assignment.ReleaseID,
				"wolf.dev/scanner-manifest-digest":      c.assignment.ManifestDigest,
				"wolf.dev/scanner-assignment-operation": c.assignment.OperationID,
			}
			observed, updated, available = 1, 1, 1
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{
			map[string]any{
				"metadata": map[string]any{"name": "scanner-worker", "generation": 1},
				"spec": map[string]any{
					"replicas": 1,
					"template": map[string]any{
						"metadata": map[string]any{"annotations": annotations},
						"spec": map[string]any{"containers": []any{
							map[string]any{"name": "worker"},
						}},
					},
				},
				"status": map[string]any{
					"observedGeneration": observed,
					"updatedReplicas":    updated, "availableReplicas": available,
				},
			},
		}})
	case r.Method == http.MethodPatch &&
		r.URL.Path == "/apis/apps/v1/namespaces/wolf/deployments/scanner-worker":
		c.deploymentPatches++
		_, _ = io.WriteString(w, `{}`)
	default:
		http.NotFound(w, r)
	}
}
