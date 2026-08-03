package main

import "testing"

func TestManagedSourceURLRejectsCredentialBearingAndUnsupportedSchemes(t *testing.T) {
	t.Parallel()
	valid, err := managedSourceURL("https://git.example/acme/wolf.git")
	if err != nil || valid != "https://git.example/acme/wolf.git" {
		t.Fatalf("valid managed source URL = %q, err=%v", valid, err)
	}
	for _, value := range []string{
		"https://git.example/acme/wolf.git?access_token=secret",
		"https://user:secret@git.example/acme/wolf.git",
		"file:///srv/wolf", "data:text/plain,secret",
		"ssh://git@git.example/acme/wolf.git",
		"custom://git.example/acme/wolf.git",
	} {
		if accepted, err := managedSourceURL(value); err == nil {
			t.Errorf("managed source URL %q accepted as %q", value, accepted)
		}
	}
}

func TestManagedLaneIdentitiesMustRemainDistinct(t *testing.T) {
	t.Parallel()
	valid := []managedLaneIdentity{
		{name: "fixed", serviceAccount: "wolf-fixed", credentialSecrets: []string{"fixed-registry"}},
		{name: "quality", serviceAccount: "wolf-quality", credentialSecrets: []string{"quality-registry", "quality-engine"}},
		{name: "integration", serviceAccount: "wolf-integration", credentialSecrets: []string{"integration-registry", "integration-engine"}},
		{name: "signer", serviceAccount: "wolf-signer", credentialSecrets: []string{"signer-secret"}},
	}
	if err := validateManagedLaneIdentitySeparation(valid); err != nil {
		t.Fatal(err)
	}
	sharedAccount := append([]managedLaneIdentity(nil), valid...)
	sharedAccount[3].serviceAccount = sharedAccount[0].serviceAccount
	if err := validateManagedLaneIdentitySeparation(sharedAccount); err == nil {
		t.Fatal("shared signer/adapter service account was accepted")
	}
	sharedSecret := append([]managedLaneIdentity(nil), valid...)
	sharedSecret[2].credentialSecrets[0] = sharedSecret[1].credentialSecrets[0]
	if err := validateManagedLaneIdentitySeparation(sharedSecret); err == nil {
		t.Fatal("shared adapter credential secret was accepted")
	}
}
