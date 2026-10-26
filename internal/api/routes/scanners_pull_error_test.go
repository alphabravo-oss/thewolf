package routes

import (
	"errors"
	"testing"
)

// Real Docker daemon messages, so the UI can tell operators what to do.
func TestClassifyPullError(t *testing.T) {
	for msg, want := range map[string]string{
		`failed to resolve reference "docker.io/zricethezav/gitleaks:v8.30.1": unexpected status from HEAD request to https://registry-1.docker.io/v2/zricethezav/gitleaks/manifests/v8.30.1: 429 Too Many Requests`: "registry_rate_limited",
		`toomanyrequests: You have reached your pull rate limit.`:                                              "registry_rate_limited",
		`pull access denied for acme/private, repository does not exist or may require 'docker login': denied`: "registry_auth_failed",
		`manifest for anchore/grype:v0.0.0 not found: manifest unknown: manifest unknown`:                      "image_not_found",
		`Get "https://ghcr.io/v2/": dial tcp: lookup ghcr.io: no such host`:                                    "registry_unreachable",
		`exit status 1: something else`:                                                                        "pull_failed",
	} {
		if _, got := classifyPullError(errors.New(msg)); got != want {
			t.Errorf("%q => %s, want %s", msg, got, want)
		}
	}
}
