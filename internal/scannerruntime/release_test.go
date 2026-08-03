package scannerruntime

import (
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scannerbundle"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func TestReleaseSnapshotRoutesImmutableImagesWithoutMutatingBase(t *testing.T) {
	t.Parallel()
	digest := func(char string) string { return "sha256:" + strings.Repeat(char, 64) }
	ref := func(name, char string) string { return "registry.example/" + name + "@" + digest(char) }
	manifest := scannerbundle.ReleaseManifest{
		SchemaVersion:     scannerbundle.ManifestSchema,
		ReleaseID:         "scanner-set-2026.31.1",
		LockDigest:        digest("a"),
		DefinitionCommit:  strings.Repeat("b", 40),
		BuildPolicyDigest: digest("c"),
		GeneratedAt:       time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC),
		Images: []scannerbundle.ReleaseImage{
			{
				Key:       "default",
				Kind:      "wolf",
				Reference: ref("wolf-scanners", "d"),
				Digest:    digest("d"),
				Platforms: map[string]string{"linux/amd64": digest("e")},
				Required:  true,
			},
			{
				Key:       "jvm",
				Kind:      "wolf",
				Reference: ref("wolf-scanners-jvm", "f"),
				Digest:    digest("f"),
				Platforms: map[string]string{"linux/amd64": digest("0")},
				Tools:     []string{"detekt", "pmd"},
				Required:  true,
			},
			{
				Key:        "trivy",
				Kind:       "upstream",
				Reference:  ref("trivy", "1"),
				Digest:     digest("1"),
				Platforms:  map[string]string{"linux/amd64": digest("2")},
				Tools:      []string{"trivy"},
				Entrypoint: "trivy",
				Required:   true,
			},
		},
	}
	snapshot, err := SnapshotFromManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	base := &container.Config{
		Image:          "legacy:tag",
		ImageOverrides: map[string]string{"detekt": "legacy-jvm:tag"},
		UpstreamTools: map[string]container.ToolImageSpec{
			"trivy": {Image: "legacy-trivy:tag"},
		},
		PullPolicy: container.PullNever,
		Network:    "none",
		Memory:     "2g",
		CPUs:       "1",
	}
	applied, err := snapshot.Apply(base)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Image != ref("wolf-scanners", "d") {
		t.Fatalf("default image = %q", applied.Image)
	}
	if applied.ImageFor("detekt") != ref("wolf-scanners-jvm", "f") {
		t.Fatalf("detekt image = %q", applied.ImageFor("detekt"))
	}
	if applied.ImageFor("trivy") != ref("trivy", "1") {
		t.Fatalf("trivy image = %q", applied.ImageFor("trivy"))
	}
	if applied.ImageFor("bandit") != ref("wolf-scanners", "d") {
		t.Fatalf("default tool image = %q", applied.ImageFor("bandit"))
	}
	if applied.Network != "none" || applied.Memory != "2g" || applied.CPUs != "1" {
		t.Fatalf("runtime controls changed: %#v", applied)
	}
	if base.Image != "legacy:tag" || base.ImageOverrides["detekt"] != "legacy-jvm:tag" {
		t.Fatal("Apply mutated the base runtime configuration")
	}
}

func TestReleaseSnapshotRejectsMutableReferenceAndDuplicateTool(t *testing.T) {
	t.Parallel()
	manifest := minimalRuntimeManifest()
	manifest.Images[0].Reference = "registry.example/wolf-scanners:stable"
	if _, err := SnapshotFromManifest(manifest); err == nil || !strings.Contains(err.Error(), "reference must end") {
		t.Fatalf("mutable reference error = %v", err)
	}

	manifest = minimalRuntimeManifest()
	manifest.Images = append(manifest.Images,
		scannerbundle.ReleaseImage{
			Key:       "other",
			Kind:      "wolf",
			Reference: "registry.example/other@" + manifest.Images[0].Digest,
			Digest:    manifest.Images[0].Digest,
			Platforms: manifest.Images[0].Platforms,
			Tools:     []string{"tool"},
		},
		scannerbundle.ReleaseImage{
			Key:       "upstream",
			Kind:      "upstream",
			Reference: "registry.example/upstream@" + manifest.Images[0].Digest,
			Digest:    manifest.Images[0].Digest,
			Platforms: manifest.Images[0].Platforms,
			Tools:     []string{"tool"},
		},
	)
	if _, err := SnapshotFromManifest(manifest); err == nil || !strings.Contains(err.Error(), "assigned to both") {
		t.Fatalf("duplicate tool error = %v", err)
	}
}

func TestSnapshotFromInventoryRequiresExactDigestAndToolRouting(t *testing.T) {
	t.Parallel()
	manifestDigest := "sha256:" + strings.Repeat("a", 64)
	defaultDigest := "sha256:" + strings.Repeat("b", 64)
	upstreamDigest := "sha256:" + strings.Repeat("c", 64)
	release := scannerrelease.Release{
		ID: "scanner-set-1", ManifestDigest: manifestDigest, LockDigest: digestForRuntime("d"),
		State: scannerrelease.ReleaseStable, SignerIdentity: "trusted-signer", PolicyRevision: 1,
	}
	images := []scannerrelease.ReleaseImage{
		trustedRuntimeImage("default", defaultDigest, "e"),
		trustedRuntimeImage("trivy", upstreamDigest, "f"),
	}
	tools := []scannerrelease.ReleaseTool{
		{ToolKey: "semgrep", MetadataJSON: `{"image_key":"default","kind":"wolf"}`},
		{ToolKey: "trivy", MetadataJSON: `{"image_key":"trivy","kind":"upstream","entrypoint":"trivy"}`},
	}
	references := map[string]string{
		"default": "registry.example/security/default@" + defaultDigest,
		"trivy":   "registry.example/security/trivy@" + upstreamDigest,
	}
	snapshot, err := SnapshotFromInventory(release, tools, images, references)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ReleaseID != release.ID || snapshot.ManifestDigest != manifestDigest ||
		snapshot.ImageFor("semgrep") != references["default"] ||
		snapshot.ImageFor("trivy") != references["trivy"] {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	cfg, err := snapshot.Apply(container.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpstreamTools["trivy"].Entrypoint != "trivy" ||
		cfg.ImageOverrides["semgrep"] != snapshot.ImageFor("semgrep") {
		t.Fatalf("runtime config = %#v", cfg)
	}

	badReferences := map[string]string{
		"default": "registry.example/security/default@" + upstreamDigest,
		"trivy":   references["trivy"],
	}
	if _, err := SnapshotFromInventory(release, tools, images, badReferences); err == nil {
		t.Fatal("mismatched image digest was accepted")
	}
	revoked := release
	revoked.State = scannerrelease.ReleaseRevoked
	if _, err := SnapshotFromInventory(revoked, tools, images, references); err == nil {
		t.Fatal("revoked release was accepted")
	}
}

func TestSnapshotFromInventoryRejectsUnverifiedSupplyChainEvidence(t *testing.T) {
	t.Parallel()

	release := scannerrelease.Release{
		ID: "release", ManifestDigest: digestForRuntime("a"), LockDigest: digestForRuntime("b"),
		State: scannerrelease.ReleaseStable, SignerIdentity: "trusted", PolicyRevision: 1,
	}
	image := trustedRuntimeImage("default", digestForRuntime("c"), "d")
	image.SignatureStatus = "unsigned"
	_, err := SnapshotFromInventory(
		release,
		[]scannerrelease.ReleaseTool{{
			ToolKey: "semgrep", MetadataJSON: `{"image_key":"default","kind":"wolf"}`,
		}},
		[]scannerrelease.ReleaseImage{image},
		map[string]string{"default": "registry.example/scanner@" + image.Digest},
	)
	if err == nil || !strings.Contains(err.Error(), "incomplete signature") {
		t.Fatalf("unverified inventory error = %v", err)
	}
}

func trustedRuntimeImage(key, digest, evidenceCharacter string) scannerrelease.ReleaseImage {
	return scannerrelease.ReleaseImage{
		ImageKey: key, Digest: digest,
		PlatformDigests: `{"linux/amd64":"` + digestForRuntime(evidenceCharacter) + `"}`,
		SignatureStatus: "verified", ProvenanceDigest: digestForRuntime("1"),
		SBOMDigest: digestForRuntime("2"),
	}
}

func digestForRuntime(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}

func minimalRuntimeManifest() scannerbundle.ReleaseManifest {
	digest := "sha256:" + strings.Repeat("a", 64)
	return scannerbundle.ReleaseManifest{
		SchemaVersion:     scannerbundle.ManifestSchema,
		ReleaseID:         "scanner-set-2026.31.1",
		LockDigest:        digest,
		DefinitionCommit:  strings.Repeat("b", 40),
		BuildPolicyDigest: digest,
		GeneratedAt:       time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC),
		Images: []scannerbundle.ReleaseImage{
			{
				Key:       "default",
				Kind:      "wolf",
				Reference: "registry.example/wolf-scanners@" + digest,
				Digest:    digest,
				Platforms: map[string]string{"linux/amd64": digest},
				Required:  true,
			},
		},
	}
}
