package scannerreleasebackend

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
)

func TestAdapterEvidenceRequiresExactOperationCommandAndReadback(t *testing.T) {
	t.Parallel()
	invocation := Invocation{
		OperationID: "sha256:" + strings.Repeat("a", 64),
		Action: Action{
			Name: "candidate-stable-comparison/default", Kind: scannerpipeline.StepTest,
		},
	}
	digest := "sha256:" + strings.Repeat("b", 64)
	artifactDigest := "sha256:" + strings.Repeat("d", 64)
	result := BackendResult{
		ExternalOperationID: invocation.OperationID,
		Result: scannerreleaseworker.StepResult{
			OutputURI:    "oci://evidence.example/wolf/quality@" + artifactDigest,
			OutputDigest: digest,
			Summary: map[string]any{"adapter_evidence": map[string]any{
				"schema_version": AdapterEvidenceSchema,
				"lane":           AdapterLaneQuality, "action": invocation.Action.Name,
				"operation_id":    invocation.OperationID,
				"command_id":      "scanner-candidate-stable-comparison",
				"output_identity": "payload",
				"artifact": map[string]any{
					"uri":    "oci://evidence.example/wolf/quality@" + artifactDigest,
					"digest": artifactDigest, "payload_digest": digest, "media_type": "application/json",
					"size_bytes": 42, "storage_media_type": "application/vnd.oci.image.manifest.v1+json",
					"storage_size_bytes": 256, "read_back_verified": true,
				},
			}},
		},
	}
	if err := validateAdapterEvidence(AdapterLaneQuality, invocation, result); err != nil {
		t.Fatal(err)
	}
	mutated := result
	mutated.ExternalOperationID = "sha256:" + strings.Repeat("c", 64)
	if err := validateAdapterEvidence(AdapterLaneQuality, invocation, mutated); err == nil {
		t.Fatal("adapter evidence with wrong sink operation was accepted")
	}
	artifact := result.Result.Summary["adapter_evidence"].(map[string]any)["artifact"].(map[string]any)
	artifact["read_back_verified"] = false
	if err := validateAdapterEvidence(AdapterLaneQuality, invocation, result); err == nil {
		t.Fatal("adapter evidence without readback was accepted")
	}
}

func TestIntegrationAdapterRequiresRuntimeAndImageDigests(t *testing.T) {
	t.Parallel()
	operation := "sha256:" + strings.Repeat("a", 64)
	digest := "sha256:" + strings.Repeat("b", 64)
	invocation := Invocation{
		OperationID: operation,
		Action:      Action{Name: "kind-scanner-integration", Kind: scannerpipeline.StepIntegration},
	}
	result := BackendResult{
		ExternalOperationID: operation,
		Result: scannerreleaseworker.StepResult{
			OutputURI:    "https://evidence.example/" + digest,
			OutputDigest: digest,
			Summary: map[string]any{"adapter_evidence": map[string]any{
				"schema_version": AdapterEvidenceSchema,
				"lane":           AdapterLaneIntegration, "action": invocation.Action.Name,
				"operation_id": operation, "command_id": "kind-scanner-integration",
				"output_identity": "payload",
				"runtime":         "kind", "image_digests": map[string]string{"default": digest},
				"artifact": map[string]any{
					"uri":    "https://evidence.example/" + digest,
					"digest": digest, "payload_digest": digest, "media_type": "application/json",
					"size_bytes": 42, "storage_media_type": "application/vnd.oci.image.manifest.v1+json",
					"storage_size_bytes": 256, "read_back_verified": true,
				},
			}},
		},
	}
	if err := validateAdapterEvidence(AdapterLaneIntegration, invocation, result); err != nil {
		t.Fatal(err)
	}
	result.Result.Summary["adapter_evidence"].(map[string]any)["runtime"] = "synthetic"
	if err := validateAdapterEvidence(AdapterLaneIntegration, invocation, result); err == nil {
		t.Fatal("synthetic integration runtime was accepted")
	}
}

func TestSBOMAdapterEvidenceRequiresExactImageSubjectAndOCIReferrer(t *testing.T) {
	t.Parallel()
	operation := "sha256:" + strings.Repeat("a", 64)
	subject := "sha256:" + strings.Repeat("b", 64)
	referrer := "sha256:" + strings.Repeat("c", 64)
	payload := "sha256:" + strings.Repeat("e", 64)
	invocation := Invocation{
		OperationID: operation,
		Action:      Action{Name: "sbom/default", Kind: scannerpipeline.StepEvidence},
		Request: scannerreleaseworker.StepRequest{
			Dependencies: map[string]scannerreleaseworker.DependencyEvidence{
				"image-manifest/default": {OutputDigest: subject},
			},
		},
	}
	result := BackendResult{
		ExternalOperationID: operation,
		Result: scannerreleaseworker.StepResult{
			OutputURI:    "oci://registry.example/wolf-scanners@" + referrer,
			OutputDigest: payload,
			Summary: map[string]any{"adapter_evidence": map[string]any{
				"schema_version": AdapterEvidenceSchema,
				"lane":           AdapterLaneQuality, "action": invocation.Action.Name,
				"operation_id": operation, "command_id": "image-sbom",
				"output_identity": "payload",
				"subject_digest":  subject, "referrer_digest": referrer,
				"artifact": map[string]any{
					"uri":    "oci://registry.example/wolf-scanners@" + referrer,
					"digest": referrer, "payload_digest": payload,
					"media_type": "application/vnd.oci.image.manifest.v1+json",
					"size_bytes": 42, "storage_media_type": "application/vnd.oci.image.manifest.v1+json",
					"storage_size_bytes": 256, "read_back_verified": true,
				},
			}},
		},
	}
	if err := validateAdapterEvidence(AdapterLaneQuality, invocation, result); err != nil {
		t.Fatal(err)
	}
	result.Result.Summary["adapter_evidence"].(map[string]any)["subject_digest"] =
		"sha256:" + strings.Repeat("d", 64)
	if err := validateAdapterEvidence(AdapterLaneQuality, invocation, result); err == nil {
		t.Fatal("SBOM adapter evidence bound to the wrong image subject was accepted")
	}
}

func TestOCIAnnotationAdapterStrictlyBindsExactIndexToInvocationAndLock(t *testing.T) {
	t.Parallel()
	root, lock := copyLockFixture(t)
	request := testRequestAt(root, scannerpipeline.Step{
		Key: "oci-annotations/default", Kind: scannerpipeline.StepEvidence,
		Timeout: 30 * time.Minute, Required: true,
	})
	request.LockDigest = lock.LockDigest
	request.PlatformsJSON = `[{"key":"default","kind":"scanner","platforms":["linux/amd64"]}]`
	invocation := testInvocationFromRequest(request)
	reportDigest := "sha256:" + strings.Repeat("d", 64)
	reportPayload := "sha256:" + strings.Repeat("e", 64)

	validate := func(t *testing.T, annotations map[string]string, arbitrary bool) error {
		t.Helper()
		var payload []byte
		if arbitrary {
			payload, _ = json.Marshal(map[string]any{"annotations": annotations})
		} else {
			payload, _ = json.Marshal(map[string]any{
				"schemaVersion": 2,
				"mediaType":     "application/vnd.oci.image.index.v1+json",
				"manifests": []map[string]any{{
					"mediaType": "application/vnd.oci.image.manifest.v1+json",
					"digest":    "sha256:" + strings.Repeat("b", 64), "size": 123,
					"platform": map[string]string{"os": "linux", "architecture": "amd64"},
				}},
				"annotations": annotations,
			})
		}
		subject := testSHA256Digest(payload)
		invocation.Request.Dependencies = map[string]scannerreleaseworker.DependencyEvidence{
			"image-manifest/default": {OutputDigest: subject},
		}
		result := BackendResult{
			ExternalOperationID: invocation.OperationID,
			Result: scannerreleaseworker.StepResult{
				OutputURI:    "oci://evidence.example/wolf/annotations@" + reportDigest,
				OutputDigest: reportPayload,
				Summary: map[string]any{"adapter_evidence": map[string]any{
					"schema_version": AdapterEvidenceSchema,
					"lane":           AdapterLaneQuality, "action": invocation.Action.Name,
					"operation_id": invocation.OperationID, "command_id": "oci-annotation-verify",
					"output_identity": "payload", "subject_digest": subject,
					"artifact": map[string]any{
						"uri":    "oci://evidence.example/wolf/annotations@" + reportDigest,
						"digest": reportDigest, "payload_digest": reportPayload,
						"media_type": "application/json", "size_bytes": 42,
						"storage_media_type": "application/vnd.oci.image.manifest.v1+json",
						"storage_size_bytes": 256, "read_back_verified": true,
					},
					"image_manifest": map[string]any{
						"digest": subject, "media_type": "application/vnd.oci.image.index.v1+json",
						"payload_base64": base64.StdEncoding.EncodeToString(payload),
					},
				}},
			},
		}
		return validateAdapterEvidence(AdapterLaneQuality, invocation, result)
	}

	valid := requiredOCIAnnotations(imageTrustBinding{
		Source: wolfImageSource, DefinitionCommit: request.DefinitionCommit,
		LockDigest: lock.LockDigest, CandidateID: request.CandidateID,
		ImageKind: "scanner", Variant: "default",
	})
	if err := validate(t, valid, false); err != nil {
		t.Fatal(err)
	}
	wrongRevision := cloneStrings(valid)
	wrongRevision[annotationRevision] = strings.Repeat("f", 40)
	if err := validate(t, wrongRevision, false); err == nil {
		t.Fatal("OCI index bound to the wrong definition revision was accepted")
	}
	if err := validate(t, valid, true); err == nil {
		t.Fatal("arbitrary JSON with trusted-looking annotations was accepted")
	}
}

func TestAdapterBackendExecutesFullInvocationProtocolThroughKubernetesJob(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workspace := filepath.Join(root, "candidate-one")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	request := testRequestAt(workspace, scannerpipeline.Step{
		Key: "candidate-stable-comparison/default", Kind: scannerpipeline.StepTest,
		Timeout: 30 * time.Minute, Required: true,
	})
	invocation := testInvocationFromRequest(request)
	sandbox := invocation
	sandbox.Request.Workspace = "/workspace"
	digest := "sha256:" + strings.Repeat("b", 64)
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatal(err)
			}
			args := nestedSliceStrings(t, captured, "spec", "template", "spec", "containers", "0", "args")
			if argumentAfter(args, "--adapter") != "/usr/local/bin/wolf-release-adapter" {
				t.Fatalf("adapter argv = %#v", args)
			}
			resultPath := argumentAfter(args, "--result")
			resultPath = filepath.Join(workspace, strings.TrimPrefix(resultPath, "/workspace/"))
			if err := os.MkdirAll(filepath.Dir(resultPath), 0o700); err != nil {
				t.Fatal(err)
			}
			result := BackendResult{
				Binding: invocation.Binding, ExternalOperationID: invocation.OperationID,
				Result: scannerreleaseworker.StepResult{
					OutputURI:    "oci://evidence.example/wolf/quality@" + digest,
					OutputDigest: digest,
					Summary: map[string]any{"adapter_evidence": map[string]any{
						"schema_version": AdapterEvidenceSchema,
						"lane":           AdapterLaneQuality, "action": invocation.Action.Name,
						"operation_id":    invocation.OperationID,
						"command_id":      "scanner-candidate-stable-comparison",
						"output_identity": "payload",
						"artifact": map[string]any{
							"uri":    "oci://evidence.example/wolf/quality@" + digest,
							"digest": digest, "payload_digest": digest,
							"media_type": "application/json",
							"size_bytes": 42, "storage_media_type": "application/vnd.oci.image.manifest.v1+json",
							"storage_size_bytes": 256, "read_back_verified": true,
						},
					}},
				},
			}
			value, _ := json.Marshal(result)
			if err := os.WriteFile(resultPath, value, 0o600); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"metadata":{"annotations":{"wolf.dev/invocation-digest":"` +
				invocationDigest(sandbox) + `"}},"status":{"succeeded":1}}`))
		case http.MethodDelete:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	backend := AdapterBackend{
		Lane: AdapterLaneQuality,
		Kubernetes: KubernetesBackend{
			APIServer: server.URL, Namespace: "release-builds", Token: "test-token",
			HTTPClient: server.Client(), WorkspacePVC: "release-workspace",
			WorkspaceRoot: root,
			Image:         "registry.example/wolf-quality@sha256:" + strings.Repeat("d", 64),
			Program:       "/usr/local/bin/wolf", AdapterPath: "/usr/local/bin/wolf-release-adapter",
			PollInterval: time.Millisecond, JobTTLSeconds: 60,
			Platforms: []string{"linux/amd64"},
		},
	}
	result, err := backend.Execute(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if result.Result.OutputDigest != digest || result.ExternalOperationID != invocation.OperationID {
		t.Fatalf("Kubernetes adapter result = %#v", result)
	}
	labels := nestedMapAny(t, captured, "spec", "template", "metadata", "labels")
	if labels[KubernetesExecutionLaneLabel] != string(AdapterLaneQuality) {
		t.Fatalf("quality adapter pod lane labels = %#v", labels)
	}
}
