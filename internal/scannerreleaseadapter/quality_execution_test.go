package scannerreleaseadapter

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/fix/qualification"
	"github.com/alphabravocompany/thewolf/internal/scannerquality"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworkspace"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

func TestQualityScopeAndRuntimeUseExactCandidateAndStableImages(t *testing.T) {
	digestA := "sha256:" + strings.Repeat("a", 64)
	digestB := "sha256:" + strings.Repeat("b", 64)
	digestC := "sha256:" + strings.Repeat("c", 64)
	contract := scannerlock.ParserContract{Status: "quality_policy", Format: "json"}
	lock := &scannerlock.Lock{
		Tools: map[string]scannerlock.Tool{
			"bandit": {IntegrationTier: "default", ParserContract: contract},
			"pmd":    {IntegrationTier: "bucket", Bucket: "jvm", ParserContract: contract},
			"trivy":  {IntegrationTier: "upstream", ParserContract: contract},
		},
		UpstreamImages: map[string]scannerlock.UpstreamImage{
			"trivy": {ResolvedReference: "registry.example/trivy@" + digestB, Digest: digestB},
		},
	}
	tools, err := lockedQualityScope(lock, "default")
	if err != nil || strings.Join(tools, ",") != "bandit,trivy" {
		t.Fatalf("default quality scope=%v error=%v", tools, err)
	}
	stable := scannerreleaseworkspace.StableRelease{
		Images: []scannerreleaseworkspace.StableImage{{
			Key: "default", Repository: "registry.example/wolf/scanners", Digest: digestC,
		}},
		Tools: []scannerreleaseworkspace.StableTool{
			{Key: "bandit", ImageKey: "default", Kind: "wolf", SourceReference: "pypi:bandit", ParserCompatibility: "quality_policy:json"},
			{Key: "trivy", ImageKey: "default", Kind: "upstream", SourceReference: "stable.example/trivy@" + digestC, SourceDigest: digestC, ParserCompatibility: "quality_policy:json"},
		},
	}
	stableImage, stableTools, err := stableQualityScope(stable, "default", lock)
	if err != nil || stableImage.Digest != digestC {
		t.Fatalf("stable scope image=%#v tools=%#v error=%v", stableImage, stableTools, err)
	}
	definition := &manifest.Manifest{Tools: map[string]manifest.Tool{
		"bandit": {}, "trivy": {Image: manifest.Image{Entrypoint: "trivy"}},
	}}
	candidate, err := qualityContainerConfig(
		"registry.example/wolf/candidate@"+digestA, digestA, tools, nil,
		lock, definition, "wolf-quality-corpus-test", "wolf-quality-database-test", "none", []string{"PATH=/usr/bin"},
	)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := qualityContainerConfig(
		stableImage.Repository+"@"+stableImage.Digest, stableImage.Digest, tools, stableTools,
		lock, definition, "wolf-quality-corpus-test", "wolf-quality-database-test", "none", []string{"PATH=/usr/bin"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.ImageFor("bandit") != "registry.example/wolf/candidate@"+digestA ||
		candidate.ImageFor("trivy") != "registry.example/trivy@"+digestB ||
		baseline.ImageFor("bandit") != stableImage.Repository+"@"+digestC ||
		baseline.ImageFor("trivy") != "stable.example/trivy@"+digestC ||
		candidate.DBVolume != "wolf-quality-database-test" ||
		baseline.DBVolume != "wolf-quality-database-test" ||
		candidate.ExtraEnv["TRIVY_SKIP_DB_UPDATE"] != "true" ||
		candidate.ExtraEnv["TRIVY_SKIP_JAVA_DB_UPDATE"] != "true" {
		t.Fatalf("candidate=%#v baseline=%#v", candidate, baseline)
	}
}

func TestQualityBuildContextIsDeterministicAndContainsNoHostPaths(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "src", "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	operation := "sha256:" + strings.Repeat("d", 64)
	first, err := qualityBuildContext(directory, "/scan", operation)
	if err != nil {
		t.Fatal(err)
	}
	second, err := qualityBuildContext(directory, "/scan", operation)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("quality build context is not deterministic")
	}
	reader := tar.NewReader(bytes.NewReader(first))
	seen := map[string]bool{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		seen[header.Name] = true
		if strings.Contains(header.Name, directory) || header.ModTime.Unix() != 0 {
			t.Fatalf("non-portable tar header: %#v", header)
		}
	}
	if !seen["Dockerfile"] || !seen["corpus/src/main.go"] {
		t.Fatalf("quality build context entries=%v", seen)
	}
}

func TestPrepareQualityDatabaseRootMatchesRuntimeCacheLayout(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "trivy-cache")
	for _, relative := range []string{"db/trivy.db", "db/metadata.json", "java-db/trivy-java.db", "java-db/metadata.json"} {
		path := filepath.Join(cache, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(relative), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := prepareQualityDatabaseRoot(cache)
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"trivy/db/trivy.db", "trivy/java-db/trivy-java.db"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("runtime cache %s: %v", relative, err)
		}
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatalf("unwrapped cache still exists: %v", err)
	}
}

func TestQualityNetworkInspectionRequiresInternalLabelledExactPolicy(t *testing.T) {
	digest := "sha256:" + strings.Repeat("e", 64)
	networkID := strings.Repeat("f", 64)
	inspection, err := json.Marshal([]qualityNetworkInspection{{
		ID: networkID, Name: "wolf-quality-fixtures", Driver: "bridge", Internal: true,
		Labels: map[string]string{
			"dev.wolf.scanner-release.quality-network": "true",
			"dev.wolf.scanner-release.policy-digest":   digest,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := validateQualityNetworkInspection("wolf-quality-fixtures", digest, inspection)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Mode != "controlled-internal" || evidence.ID != networkID || evidence.PolicyDigest != digest {
		t.Fatalf("network evidence = %#v", evidence)
	}

	var decoded []qualityNetworkInspection
	if err := json.Unmarshal(inspection, &decoded); err != nil {
		t.Fatal(err)
	}
	mutations := []func(*qualityNetworkInspection){
		func(value *qualityNetworkInspection) { value.Internal = false },
		func(value *qualityNetworkInspection) { value.Driver = "host" },
		func(value *qualityNetworkInspection) { value.Name = "bridge" },
		func(value *qualityNetworkInspection) { value.ID = "mutable" },
		func(value *qualityNetworkInspection) {
			delete(value.Labels, "dev.wolf.scanner-release.quality-network")
		},
		func(value *qualityNetworkInspection) {
			value.Labels["dev.wolf.scanner-release.policy-digest"] = "sha256:" + strings.Repeat("a", 64)
		},
	}
	for index, mutate := range mutations {
		value := decoded[0]
		value.Labels = map[string]string{}
		for key, label := range decoded[0].Labels {
			value.Labels[key] = label
		}
		mutate(&value)
		raw, _ := json.Marshal([]qualityNetworkInspection{value})
		if _, err := validateQualityNetworkInspection("wolf-quality-fixtures", digest, raw); err == nil {
			t.Fatalf("network inspection mutation %d was accepted", index)
		}
	}
}

func TestQualityToolTargetsAreExplicitAndBounded(t *testing.T) {
	executable := scannerquality.ToolPolicy{Strategy: "gated-executable"}
	engine := engineConfig{QualityTargets: map[string]string{
		"nuclei": "http://wolf-quality-nuclei:8080/",
	}}
	if target, err := qualityToolTarget("nuclei", executable, engine); err != nil ||
		target != "http://wolf-quality-nuclei:8080/" {
		t.Fatalf("Nuclei quality target=%q error=%v", target, err)
	}
	if target, err := qualityToolTarget("dockle", executable, engine); err != nil ||
		target != "/scan/dockle-image.tar" {
		t.Fatalf("Dockle quality target=%q error=%v", target, err)
	}
	if _, err := qualityToolTarget("nuclei", executable, engineConfig{}); err == nil {
		t.Fatal("Nuclei quality execution without a controlled target was accepted")
	}
	if target, err := qualityToolTarget(
		"codeql", scannerquality.ToolPolicy{Strategy: "structural"}, engineConfig{},
	); err != nil || target != "" {
		t.Fatalf("structural quality target=%q error=%v", target, err)
	}
}

func TestParseDockerMemoryUsesBinaryAndDecimalUnits(t *testing.T) {
	for input, expected := range map[string]int64{
		"12.5MiB / 8GiB": 13107200,
		"2GiB / 8GiB":    2 << 30,
		"750MB / 8GB":    750000000,
	} {
		actual, err := parseDockerMemory([]byte(input))
		if err != nil || actual != expected {
			t.Fatalf("parseDockerMemory(%q)=%d error=%v, want %d", input, actual, err, expected)
		}
	}
}

func TestFixerQualificationArgumentsAreImmutableAndHardened(t *testing.T) {
	reference := "registry.example/wolf/fixer@sha256:" + strings.Repeat("a", 64)
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		args, err := fixerQualificationArgs(platform, reference, "claude", "interactive-session")
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(args, " ")
		for _, required := range []string{
			"--pull=always", "--platform " + platform, "--network none", "--read-only",
			"--cap-drop ALL", "no-new-privileges=true", "--pids-limit 128",
			"--memory 512m", "--cpus 1", "/home/wolf:rw,noexec,nosuid,nodev",
			"/run/wolf-qualification:rw,nosuid,nodev,uid=1000,gid=1000,mode=0700",
			reference, "qualification --expected-variant claude --expected-auth-mode interactive-session --scratch /run/wolf-qualification",
		} {
			if !strings.Contains(joined, required) {
				t.Fatalf("qualification args missing %q: %s", required, joined)
			}
		}
		for _, forbidden := range []string{"docker.sock", "DOCKER_HOST", "DOCKER_CONFIG", " -v ", " --env "} {
			if strings.Contains(joined, forbidden) {
				t.Fatalf("qualification args contain %q: %s", forbidden, joined)
			}
		}
	}
	if _, err := fixerQualificationArgs("linux/amd64", "registry.example/wolf/fixer:latest", "base", "none"); err == nil {
		t.Fatal("mutable fixer reference was accepted")
	}
}

func TestFixerQualificationReportDecoderRejectsUnknownAndTrailingJSON(t *testing.T) {
	report := qualification.Report{
		SchemaVersion: qualification.SchemaVersion, Variant: "base", AuthMode: "none",
		InstalledCLIs: []string{}, SelectedTiers: []string{"api"},
		CompletedChecks: []string{
			"api-diff-contract", "api-malformed-response-rejected", "cli-boundary",
			"cli-command-failure-rejected", "cli-malformed-output-rejected",
			"cli-protocol-success", "cli-timeout-rejected", "missing-provider-rejected",
			"unauthenticated-api-fallback",
		},
	}
	value, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeFixerQualificationReport(value)
	if err != nil || qualification.ValidateReport(decoded, "base", "none") != nil {
		t.Fatalf("valid report rejected: %v", err)
	}
	if _, err := decodeFixerQualificationReport(append(value, []byte(` {}`)...)); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
	unknown := append(value[:len(value)-1], []byte(`,"unexpected":true}`)...)
	if _, err := decodeFixerQualificationReport(unknown); err == nil {
		t.Fatal("unknown report field was accepted")
	}
}
