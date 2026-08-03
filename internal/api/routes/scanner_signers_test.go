package routes

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const testSignerInput = `{
	"name":"production release signer",
	"provider":"aws_kms",
	"algorithm":"ed25519",
	"key_reference":"aws-kms://us-east-1/123456789012/alias/wolf-release",
	"workload_identity":true,
	"identity":"arn:aws:iam::123456789012:role/wolf-release",
	"issuer":"https://sts.amazonaws.com",
	"subject":"arn:aws:iam::123456789012:role/wolf-release",
	"trust_root_reference":"kubernetes://wolf-system/aws-kms-roots"
}`

func TestScannerSignerCRUDMasksReferencesAndRotatesAtomically(t *testing.T) {
	withScannerSupplyChainStore(t)
	created := scannerRouteRequest(
		t, ScannerSupplyChainCreateSigner, http.MethodPost, "/signers",
		testSignerInput, nil, nil,
	)
	if created.Code != http.StatusCreated || created.Header().Get("ETag") != `"1"` {
		t.Fatalf("create signer = %d %s", created.Code, created.Body)
	}
	var envelope struct {
		Data scannerSignerView `json:"data"`
	}
	decodeScannerResponse(t, created, &envelope)
	if envelope.Data.ID == "" ||
		envelope.Data.KeyReference != "aws-kms://***" ||
		envelope.Data.TrustRootReference != "kubernetes://***" ||
		strings.Contains(created.Body.String(), "aws-kms://us-east-1") ||
		strings.Contains(created.Body.String(), "aws-kms-roots") {
		t.Fatalf("signer response leaked or omitted data: %s", created.Body)
	}

	params := map[string]string{"id": envelope.Data.ID}
	rotated := scannerRouteRequest(
		t, ScannerSupplyChainRotateSigner, http.MethodPost,
		"/signers/"+envelope.Data.ID+"/rotate", testSignerInput,
		map[string]string{"If-Match": `"1"`}, params,
	)
	if rotated.Code != http.StatusCreated || rotated.Header().Get("ETag") != `"2"` {
		t.Fatalf("rotate signer = %d %s", rotated.Code, rotated.Body)
	}
	var replacement struct {
		Data scannerSignerView `json:"data"`
	}
	decodeScannerResponse(t, rotated, &replacement)
	if replacement.Data.ID == envelope.Data.ID ||
		replacement.Data.RotatedFromID != envelope.Data.ID {
		t.Fatalf("replacement = %+v", replacement.Data)
	}

	stale := scannerRouteRequest(
		t, ScannerSupplyChainRotateSigner, http.MethodPost,
		"/signers/"+envelope.Data.ID+"/rotate", testSignerInput,
		map[string]string{"If-Match": `"1"`}, params,
	)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale rotate = %d %s", stale.Code, stale.Body)
	}

	revokeParams := map[string]string{"id": replacement.Data.ID}
	revoked := scannerRouteRequest(
		t, ScannerSupplyChainRevokeSigner, http.MethodPost,
		"/signers/"+replacement.Data.ID+"/revoke",
		`{"reason":"scheduled key retirement"}`,
		map[string]string{"If-Match": `"2"`}, revokeParams,
	)
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoke signer = %d %s", revoked.Code, revoked.Body)
	}
	decodeScannerResponse(t, revoked, &replacement)
	if replacement.Data.State != "revoked" ||
		replacement.Data.RevocationReason != "scheduled key retirement" {
		t.Fatalf("revoked signer = %+v", replacement.Data)
	}
}

func TestScannerSignerRejectsRawKeyAndUnknownFields(t *testing.T) {
	withScannerSupplyChainStore(t)
	raw := strings.Replace(
		testSignerInput,
		`"key_reference":"aws-kms://us-east-1/123456789012/alias/wolf-release"`,
		`"key_reference":"-----BEGIN PRIVATE KEY-----"`,
		1,
	)
	response := scannerRouteRequest(
		t, ScannerSupplyChainCreateSigner, http.MethodPost, "/signers",
		raw, nil, nil,
	)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("raw key = %d %s", response.Code, response.Body)
	}

	var object map[string]any
	if err := json.Unmarshal([]byte(testSignerInput), &object); err != nil {
		t.Fatal(err)
	}
	object["private_key"] = "forbidden"
	value, _ := json.Marshal(object)
	response = scannerRouteRequest(
		t, ScannerSupplyChainCreateSigner, http.MethodPost, "/signers",
		string(value), nil, nil,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown private_key = %d %s", response.Code, response.Body)
	}
}
