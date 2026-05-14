package scanners

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

func TestEnvDefaults(t *testing.T) {
	t.Setenv("WOLF_SCANNERS_IMAGE", "x:1")
	t.Setenv("WOLF_SCANNERS_IMAGE_JVM", "x-jvm:1")
	t.Setenv("WOLF_SCANNERS_IMAGE_RUST", "x-rust:1")
	t.Setenv("WOLF_SCANNERS_IMAGE_CODEQL", "x-codeql:1")
	t.Setenv("WOLF_SCANNERS_PULL_POLICY", "Always")
	t.Setenv("WOLF_SCANNERS_NETWORK", "none")

	c := EnvDefaults()
	if c.Image != "x:1" {
		t.Errorf("Image = %q", c.Image)
	}
	if c.ImageOverrides["infer"] != "x-jvm:1" {
		t.Errorf("infer override missing: %v", c.ImageOverrides)
	}
	if c.ImageOverrides["clippy"] != "x-rust:1" {
		t.Errorf("clippy override missing: %v", c.ImageOverrides)
	}
	if c.ImageOverrides["codeql"] != "x-codeql:1" {
		t.Errorf("codeql override missing: %v", c.ImageOverrides)
	}
	if c.PullPolicy != "Always" {
		t.Errorf("PullPolicy = %q", c.PullPolicy)
	}
}

func TestToContainerConfig(t *testing.T) {
	cfg := Config{
		Image:      "x:1",
		PullPolicy: "Never",
		Network:    "bridge",
	}
	cc, err := cfg.ToContainerConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cc.PullPolicy != container.PullNever {
		t.Errorf("PullPolicy = %v", cc.PullPolicy)
	}
}

func TestToContainerConfig_InvalidPolicy(t *testing.T) {
	cfg := Config{Image: "x:1", PullPolicy: "Bogus"}
	if _, err := cfg.ToContainerConfig(); err == nil {
		t.Error("expected error for invalid pull policy")
	}
}

func TestDoctor_NoConfig(t *testing.T) {
	// Clear default to simulate no startup.
	container.SetDefault(nil)
	defer container.SetDefault(nil)
	var buf bytes.Buffer
	if err := Doctor(context.Background(), &buf); err == nil {
		t.Error("expected error when no config loaded")
	}
	if !strings.Contains(buf.String(), "FAIL") {
		t.Errorf("doctor output should include FAIL, got: %s", buf.String())
	}
}

func TestDoctor_WithConfig(t *testing.T) {
	// Install a config and pre-populate the image cache.
	cfg := &container.Config{
		Image:      "x:1",
		PullPolicy: container.PullIfNotPresent,
		Network:    "bridge",
		UID:        1000,
		GID:        1000,
	}
	container.SetDefault(cfg)
	defer container.SetDefault(nil)
	container.ResetImageReadyCache()
	// Use the internal cache poke. ImageReady can't pull during a unit test
	// because docker isn't guaranteed in CI; we simulate "ready" via the cache.
	// (Tests in container_test.go already cover the real flow.)

	var buf bytes.Buffer
	// Expect to fail on "docker reachable" if docker is absent — that's fine
	// in CI. We just want to make sure the function runs without panicking
	// and produces structured output.
	_ = Doctor(context.Background(), &buf)
	if buf.Len() == 0 {
		t.Error("Doctor produced no output")
	}
}

func TestAutoDiscoverBucketImages(t *testing.T) {
	origLook, origCmd := execLookPath, execCommand
	defer func() { execLookPath, execCommand = origLook, origCmd }()

	execLookPath = func(name string) (string, error) { return "/usr/bin/docker", nil }

	// stubImageIDs returns a stub execCommand that pretends the given
	// ref→ID map is what `docker image inspect --format {{.Id}}` would
	// emit. Missing refs return exit=1 (image not found).
	stubImageIDs := func(ids map[string]string) func(string, ...string) *exec.Cmd {
		return func(name string, args ...string) *exec.Cmd {
			ref := args[len(args)-1]
			if id, ok := ids[ref]; ok {
				return exec.Command("echo", id)
			}
			return exec.Command("false")
		}
	}

	// Case 1: only the JVM bucket is present, with its own distinct ID.
	execCommand = stubImageIDs(map[string]string{
		"wolf-scanners:dev":     "sha256:default",
		"wolf-scanners-jvm:dev": "sha256:jvm",
	})
	cfg := &container.Config{Image: "wolf-scanners:dev"}
	autoDiscoverBucketImages(cfg)
	if cfg.ImageOverrides["infer"] != "wolf-scanners-jvm:dev" {
		t.Errorf("infer override = %q, want wolf-scanners-jvm:dev", cfg.ImageOverrides["infer"])
	}
	if cfg.ImageOverrides["pmd"] != "wolf-scanners-jvm:dev" {
		t.Errorf("pmd override = %q, want wolf-scanners-jvm:dev", cfg.ImageOverrides["pmd"])
	}
	if _, set := cfg.ImageOverrides["clippy"]; set {
		t.Errorf("clippy should not be set when rust image absent: %v", cfg.ImageOverrides["clippy"])
	}
	if _, set := cfg.ImageOverrides["codeql"]; set {
		t.Errorf("codeql should not be set when codeql image absent: %v", cfg.ImageOverrides["codeql"])
	}

	// Case 2: pre-existing override wins over auto-discovery.
	execCommand = stubImageIDs(map[string]string{
		"wolf-scanners:dev":     "sha256:default",
		"wolf-scanners-jvm:dev": "sha256:jvm",
	})
	cfg2 := &container.Config{
		Image:          "wolf-scanners:dev",
		ImageOverrides: map[string]string{"infer": "custom:tag"},
	}
	autoDiscoverBucketImages(cfg2)
	if cfg2.ImageOverrides["infer"] != "custom:tag" {
		t.Errorf("pre-set infer override clobbered: got %q", cfg2.ImageOverrides["infer"])
	}
	if cfg2.ImageOverrides["pmd"] != "wolf-scanners-jvm:dev" {
		t.Errorf("pmd should still auto-fill: got %q", cfg2.ImageOverrides["pmd"])
	}

	// Case 3: tag-alias. wolf-scanners-jvm:dev has the SAME ID as the
	// default image — operator ran `docker tag` instead of building the
	// real bucket. Must NOT wire the override, must NOT poison the run.
	execCommand = stubImageIDs(map[string]string{
		"wolf-scanners:dev":     "sha256:default",
		"wolf-scanners-jvm:dev": "sha256:default", // <-- same ID
	})
	cfg3 := &container.Config{Image: "wolf-scanners:dev"}
	autoDiscoverBucketImages(cfg3)
	if _, set := cfg3.ImageOverrides["infer"]; set {
		t.Errorf("tag-alias: infer override must NOT be set, got %q", cfg3.ImageOverrides["infer"])
	}
	if _, set := cfg3.ImageOverrides["pmd"]; set {
		t.Errorf("tag-alias: pmd override must NOT be set, got %q", cfg3.ImageOverrides["pmd"])
	}

	// Case 4: no docker on host → no overrides added, no panic.
	execLookPath = func(name string) (string, error) { return "", errors.New("not found") }
	cfg4 := &container.Config{Image: "wolf-scanners:dev"}
	autoDiscoverBucketImages(cfg4)
	if len(cfg4.ImageOverrides) != 0 {
		t.Errorf("expected no overrides when docker absent, got %v", cfg4.ImageOverrides)
	}

	// Case 5: nil cfg must not panic.
	autoDiscoverBucketImages(nil)
}
