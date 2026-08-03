package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func TestSignerProfileRotationAndRevocationAreVersioned(t *testing.T) {
	t.Parallel()
	store, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repository := store.ScannerReleases()
	ctx := context.Background()
	profile := &scannerrelease.SignerProfile{
		ID: "signer-original", Name: "production",
		Provider: scannerrelease.SignerAWSKMS, Algorithm: "ecdsa-p256-sha256",
		KeyReference:     "aws-kms://arn:aws:kms:us-east-1:123:key/original",
		WorkloadIdentity: true, Identity: "arn:aws:iam::123:role/wolf",
		Issuer: "https://sts.amazonaws.com", Subject: "wolf-builder",
		TrustRootReference: "secret://signing/root",
		State:              scannerrelease.SignerActive, Revision: 1, CreatedBy: "admin",
	}
	if err := repository.CreateSignerProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	replacement := &scannerrelease.SignerProfile{
		ID: "signer-rotated", Provider: scannerrelease.SignerAWSKMS,
		Algorithm:        "ecdsa-p256-sha256",
		KeyReference:     "aws-kms://arn:aws:kms:us-east-1:123:key/rotated",
		WorkloadIdentity: true, Identity: "arn:aws:iam::123:role/wolf",
		Issuer: "https://sts.amazonaws.com", Subject: "wolf-builder",
		TrustRootReference: "secret://signing/root", CreatedBy: "admin",
	}
	if err := repository.RotateSignerProfile(
		ctx, profile.ID, 99, replacement,
	); !errors.Is(err, scannerrelease.ErrVersionConflict) {
		t.Fatalf("stale rotation error = %v", err)
	}
	if err := repository.RotateSignerProfile(ctx, profile.ID, 1, replacement); err != nil {
		t.Fatal(err)
	}
	original, err := repository.GetSignerProfile(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := repository.GetSignerProfile(ctx, replacement.ID)
	if err != nil {
		t.Fatal(err)
	}
	if original.State != scannerrelease.SignerDisabled ||
		rotated.State != scannerrelease.SignerActive ||
		rotated.Revision != 2 || rotated.RotatedFromID != profile.ID ||
		rotated.Name != profile.Name {
		t.Fatalf("original=%#v rotated=%#v", original, rotated)
	}
	at := time.Now().UTC()
	if err := repository.RevokeSignerProfile(
		ctx, rotated.ID, rotated.Revision,
		"provider reported key compromise", "security-admin", at,
	); err != nil {
		t.Fatal(err)
	}
	revoked, err := repository.GetSignerProfile(ctx, rotated.ID)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.State != scannerrelease.SignerRevoked ||
		revoked.RevokedAt == nil || revoked.RevokedBy != "security-admin" ||
		revoked.RevocationReason == "" {
		t.Fatalf("revoked profile = %#v", revoked)
	}
}
