package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannerreleasebackend"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworkspace"
	"github.com/alphabravocompany/thewolf/internal/scannersigning"
)

type stepExecutorFunc func(
	context.Context,
	scannerreleaseworker.StepRequest,
) (scannerreleaseworker.StepResult, error)

func (f stepExecutorFunc) Execute(
	ctx context.Context,
	request scannerreleaseworker.StepRequest,
) (scannerreleaseworker.StepResult, error) {
	return f(ctx, request)
}

type stepSignerAdapter struct {
	private ed25519.PrivateKey
	public  ed25519.PublicKey
}

func (a stepSignerAdapter) Sign(
	_ context.Context,
	request scannersigning.Request,
) (scannersigning.Result, error) {
	payload, err := base64.RawStdEncoding.DecodeString(request.Payload)
	if err != nil {
		return scannersigning.Result{}, err
	}
	der, err := x509.MarshalPKIXPublicKey(a.public)
	if err != nil {
		return scannersigning.Result{}, err
	}
	signature := ed25519.Sign(a.private, payload)
	signatureDigest := scannersigning.DigestValue(signature)
	artifactDigest := scannersigning.DigestValue(append([]byte("oci-signature-envelope:"), signature...))
	return scannersigning.Result{
		SchemaVersion: scannersigning.ResultSchema,
		OperationID:   request.OperationID, RequestDigest: request.RequestDigest,
		ProfileDigest: request.ProfileDigest, Algorithm: request.Profile.Algorithm,
		Signature:    base64.RawStdEncoding.EncodeToString(signature),
		SignatureURI: "oci://registry.example/wolf-signatures@" + artifactDigest,
		PublicKeyPEM: string(pem.EncodeToMemory(&pem.Block{
			Type: "PUBLIC KEY", Bytes: der,
		})),
		KeyVersion: "7", Identity: request.Profile.Identity,
		Issuer: request.Profile.Issuer, Subject: request.Profile.Subject,
		TrustRootReference: request.TrustRootReference, TrustVerified: true,
		ExternalOperationID:       request.OperationID,
		SignatureArtifactDigest:   artifactDigest,
		SignatureMediaType:        "application/vnd.dev.cosign.simplesigning.v1+json",
		SignatureArtifactSize:     512,
		SignatureReadBackVerified: true,
		ArtifactSubjectDigest:     request.Artifact.Digest,
		StoredSignatureDigest:     signatureDigest,
	}, nil
}

func TestScannerReleaseSigningStepUsesBoundAdapterAndOperationID(t *testing.T) {
	t.Parallel()
	invocation, err := scannerreleasebackend.PrepareInvocation(
		stepBridgeRequest(t, "signature/default", scannerpipeline.StepEvidence),
	)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	profile := scannerrelease.SignerProfile{
		ID: "aws-release", Name: "AWS release", Provider: scannerrelease.SignerAWSKMS,
		Algorithm:        "ed25519",
		KeyReference:     "aws-kms://us-east-1/123456789012/alias/release",
		WorkloadIdentity: true, Identity: "role/wolf-release",
		Issuer: "https://sts.amazonaws.com", Subject: "role/wolf-release",
		TrustRootReference: "kubernetes://wolf/aws-roots",
		State:              scannerrelease.SignerActive, Revision: 1,
	}
	result, err := executeSignerInvocation(
		context.Background(), invocation, profile,
		scannersigning.Artifact{
			URI:       "oci://registry.example/wolf/scanner@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Digest:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			MediaType: "application/vnd.oci.image.manifest.v1+json",
		},
		stepSignerAdapter{private: private, public: public}, t.TempDir(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExternalOperationID != invocation.OperationID ||
		result.Binding != invocation.Binding ||
		result.Result.OutputDigest == "" ||
		!strings.HasPrefix(result.Result.OutputURI, "oci://") ||
		result.Result.Summary["signing_evidence"] == nil {
		t.Fatalf("signing result = %#v", result)
	}
}

func TestSigningArtifactRehydratesFromDurableLedgerInFreshWorkspace(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		action, dependency, mediaType string
	}{
		{
			action: "signature/default", dependency: "candidate-publish/default",
			mediaType: "application/vnd.oci.image.index.v1+json",
		},
		{
			action: "release-manifest-signature", dependency: "release-manifest",
			mediaType: "application/vnd.wolf.scanner-release-manifest.v1+json",
		},
	} {
		t.Run(test.action, func(t *testing.T) {
			request := stepBridgeRequest(t, test.action, scannerpipeline.StepEvidence)
			invocation, err := scannerreleasebackend.PrepareInvocation(request)
			if err != nil {
				t.Fatal(err)
			}
			digest := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			dependency := scannerpipeline.Step{
				Key: test.dependency, Kind: scannerpipeline.StepEvidence,
				Timeout: time.Minute, Required: true,
			}
			binding := scannerreleaseworkspace.NewBinding(
				request.BuildRunID, request.CandidateID, request.BuildAttempt,
				request.DefinitionCommit, request.LockDigest, request.PolicyID, request.PolicyRevision,
			)
			if err := scannerreleaseworkspace.WriteEvidence(
				request.Workspace, dependency, 1, binding,
				scannerreleaseworker.StepResult{
					OutputURI:    "oci://registry.example/wolf/artifact@" + digest,
					OutputDigest: digest, Summary: map[string]any{"media_type": test.mediaType},
				},
			); err != nil {
				t.Fatal(err)
			}
			artifact, err := readOrDeriveSigningArtifact(invocation)
			if err != nil {
				t.Fatal(err)
			}
			if artifact.Digest != digest || artifact.MediaType != test.mediaType {
				t.Fatalf("rehydrated signing artifact = %#v", artifact)
			}
		})
	}
}

func TestSigningArtifactRejectsWorkspaceDescriptorThatDiffersFromDurableEvidence(t *testing.T) {
	t.Parallel()
	request := stepBridgeRequest(t, "signature/default", scannerpipeline.StepEvidence)
	invocation, err := scannerreleasebackend.PrepareInvocation(request)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("b", 64)
	binding := scannerreleaseworkspace.NewBinding(
		request.BuildRunID, request.CandidateID, request.BuildAttempt,
		request.DefinitionCommit, request.LockDigest, request.PolicyID, request.PolicyRevision,
	)
	if err := scannerreleaseworkspace.WriteEvidence(
		request.Workspace,
		scannerpipeline.Step{Key: "candidate-publish/default", Kind: scannerpipeline.StepPublish, Timeout: time.Minute, Required: true},
		1, binding,
		scannerreleaseworker.StepResult{
			OutputURI:    "oci://registry.example/wolf/default@" + digest,
			OutputDigest: digest,
			Summary:      map[string]any{"media_type": "application/vnd.oci.image.index.v1+json"},
		},
	); err != nil {
		t.Fatal(err)
	}
	descriptorDirectory := filepath.Join(request.Workspace, ".wolf-signing", "requests")
	if err := os.MkdirAll(descriptorDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	wrongDigest := "sha256:" + strings.Repeat("c", 64)
	descriptor, err := json.Marshal(signingArtifactDocument{
		SchemaVersion: signingArtifactSchema,
		Artifact: scannersigning.Artifact{
			URI:    "oci://registry.example/wolf/default@" + wrongDigest,
			Digest: wrongDigest, MediaType: "application/vnd.oci.image.index.v1+json",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(descriptorDirectory, "signature--default.json"), descriptor, 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := readOrDeriveSigningArtifact(invocation); err == nil ||
		!strings.Contains(err.Error(), "does not match durable") {
		t.Fatalf("mismatched signing descriptor error = %v", err)
	}
}

func TestScannerReleaseStepBridgeBindsLegacyCommandResult(t *testing.T) {
	t.Parallel()
	invocation, err := scannerreleasebackend.PrepareInvocation(
		stepBridgeRequest(t, "manifest-validate", scannerpipeline.StepValidation),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executeScannerReleaseInvocation(
		context.Background(), invocation,
		stepExecutorFunc(func(
			_ context.Context,
			request scannerreleaseworker.StepRequest,
		) (scannerreleaseworker.StepResult, error) {
			if request.Step.Key != "manifest-validate" {
				t.Fatalf("step = %q", request.Step.Key)
			}
			return scannerreleaseworker.StepResult{
				OutputDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Binding != invocation.Binding ||
		result.Result.OutputDigest == "" ||
		result.ExternalOperationID != "" {
		t.Fatalf("bridge result = %#v", result)
	}
}

func TestScannerReleaseStepBridgeRejectsExternalSideEffects(t *testing.T) {
	t.Parallel()
	invocation, err := scannerreleasebackend.PrepareInvocation(
		stepBridgeRequest(t, "signature/default", scannerpipeline.StepEvidence),
	)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	_, err = executeScannerReleaseInvocation(
		context.Background(), invocation,
		stepExecutorFunc(func(
			context.Context,
			scannerreleaseworker.StepRequest,
		) (scannerreleaseworker.StepResult, error) {
			called = true
			return scannerreleaseworker.StepResult{}, nil
		}),
	)
	if !errors.Is(err, scannerreleasebackend.ErrUnsupportedStep) {
		t.Fatalf("external action error = %v", err)
	}
	if called {
		t.Fatal("legacy executor ran for an externally side-effecting action")
	}
}

func TestScannerReleaseAdapterBridgeUsesFullInvocationProtocol(t *testing.T) {
	t.Parallel()
	invocation, err := scannerreleasebackend.PrepareInvocation(
		stepBridgeRequest(t, "candidate-stable-comparison/default", scannerpipeline.StepTest),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executeScannerReleaseAdapterInvocation(
		context.Background(), invocation, scannerReleaseAdapterTestExecutable(t, "success"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Binding != invocation.Binding || result.ExternalOperationID != invocation.OperationID ||
		result.Result.OutputDigest == "" {
		t.Fatalf("adapter bridge result = %#v", result)
	}
}

func TestScannerReleaseAdapterBridgeRejectsOversizeStreams(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"oversize-stdout", "oversize-stderr"} {
		t.Run(mode, func(t *testing.T) {
			invocation, err := scannerreleasebackend.PrepareInvocation(
				stepBridgeRequest(t, "candidate-stable-comparison/default", scannerpipeline.StepTest),
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = executeScannerReleaseAdapterInvocation(
				context.Background(), invocation, scannerReleaseAdapterTestExecutable(t, mode),
			)
			if err == nil || !strings.Contains(err.Error(), "exceeds") {
				t.Fatalf("oversize %s error = %v", mode, err)
			}
			if strings.Contains(err.Error(), "adapter-super-secret") {
				t.Fatalf("oversize %s leaked adapter stderr: %v", mode, err)
			}
		})
	}
}

func scannerReleaseAdapterTestExecutable(t *testing.T, mode string) string {
	t.Helper()
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "adapter")
	script := fmt.Sprintf(
		"#!/bin/sh\nWOLF_TEST_SCANNER_RELEASE_ADAPTER=%s exec %q -test.run '^TestScannerReleaseAdapterHelperProcess$'\n",
		mode, testExecutable,
	)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScannerReleaseAdapterHelperProcess(t *testing.T) {
	mode := os.Getenv("WOLF_TEST_SCANNER_RELEASE_ADAPTER")
	if mode == "" {
		return
	}
	var invocation scannerreleasebackend.Invocation
	if err := json.NewDecoder(os.Stdin).Decode(&invocation); err != nil {
		os.Exit(2)
	}
	digest := "sha256:" + strings.Repeat("b", 64)
	result := scannerreleasebackend.BackendResult{
		Binding: invocation.Binding, ExternalOperationID: invocation.OperationID,
		Result: scannerreleaseworker.StepResult{
			OutputURI:    "oci://evidence.example/wolf/quality@" + digest,
			OutputDigest: digest,
		},
	}
	value, err := json.Marshal(result)
	if err != nil {
		os.Exit(2)
	}
	_, _ = os.Stdout.Write(value)
	if mode == "oversize-stdout" {
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), scannerReleaseStepInputLimit+1))
	}
	if mode == "oversize-stderr" {
		_, _ = os.Stderr.Write([]byte("token=adapter-super-secret "))
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("x"), scannerReleaseStepInputLimit+1))
	}
	os.Exit(0)
}

func stepBridgeRequest(
	t *testing.T,
	key string,
	kind scannerpipeline.StepKind,
) scannerreleaseworker.StepRequest {
	t.Helper()
	request := scannerreleaseworker.StepRequest{
		BuildRunID: "build-1", CandidateID: "candidate-1",
		BuildAttempt: 1, StepAttempt: 1, Workspace: t.TempDir(),
		DefinitionCommit: "1111111111111111111111111111111111111111",
		LockDigest:       "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PolicyID:         "policy-1", PolicyRevision: 7,
		Step: scannerpipeline.Step{
			Key: key, Kind: kind, Timeout: time.Minute, Required: true,
		},
	}
	request.LogicalOperationID = scannerreleaseworker.DeriveLogicalOperationID(request)
	return request
}
