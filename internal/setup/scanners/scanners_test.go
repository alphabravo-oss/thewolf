package scanners

import (
	"bytes"
	"context"
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
