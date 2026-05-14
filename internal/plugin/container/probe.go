package container

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// ErrImageMissing is returned by EnsureImage when the configured image is not
// present locally and the pull policy is Never.
var ErrImageMissing = errors.New("scanners image not present and pull policy is Never")

// ErrDockerUnavailable is returned by EnsureImage when the docker CLI is
// missing or the daemon is unreachable.
var ErrDockerUnavailable = errors.New("docker is unavailable")

// imageReadyCache memoizes ImageReady results. The wolf-slim startup probe
// is authoritative; once it passes, all plugins' CheckAvailable can skip the
// roundtrip to docker. We refresh the cache when EnsureImage runs.
type imageReadyState struct {
	mu    sync.RWMutex
	ready map[string]bool
}

var globalImageState = &imageReadyState{ready: map[string]bool{}}

func (s *imageReadyState) get(image string) (val, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.ready[image]
	return v, ok
}

func (s *imageReadyState) set(image string, v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready[image] = v
}

// ResetImageReadyCache forgets cached ImageReady results. Tests call this to
// avoid leak between cases; production code typically does not.
func ResetImageReadyCache() {
	globalImageState.mu.Lock()
	defer globalImageState.mu.Unlock()
	globalImageState.ready = map[string]bool{}
}

// ImageReady is the fast-path check used by every plugin's CheckAvailable().
// It returns true once EnsureImage has confirmed the image is locally present.
//
// We do not invoke `docker image inspect` here on every call — that would
// fork ~40 docker processes per scan just for availability. Instead, we
// trust the cache populated by EnsureImage at startup.
//
// If the cache is empty for cfg.Image (which means wolf failed to call
// EnsureImage at startup, or cfg is otherwise misconfigured), ImageReady
// returns false — plugins are skipped with an actionable error message.
func ImageReady(cfg *Config) bool {
	if cfg == nil || cfg.Disabled {
		return false
	}
	if cfg.Image == "" {
		return false
	}
	v, ok := globalImageState.get(cfg.Image)
	return ok && v
}

// EnsureImage verifies cfg.Image is available locally, pulling per cfg.PullPolicy.
// Call this once at wolf-slim startup. Returns nil on success.
//
//	PullIfNotPresent: pull if absent; no-op if present.
//	PullAlways:       always pull, even if cached.
//	PullNever:        return ErrImageMissing if absent.
//
// On success, ImageReady(cfg) starts returning true for cfg.Image.
func EnsureImage(ctx context.Context, cfg *Config) error {
	if cfg == nil {
		return errors.New("container config is nil")
	}
	if cfg.Disabled {
		return nil
	}
	if cfg.Image == "" {
		return errors.New("container image is empty")
	}

	if err := DockerAvailable(ctx); err != nil {
		return err
	}

	present := imageInspect(ctx, cfg.Image) == nil

	switch cfg.PullPolicy {
	case PullAlways:
		if err := imagePull(ctx, cfg.Image); err != nil {
			return fmt.Errorf("pull %q (policy=Always): %w", cfg.Image, err)
		}
	case PullNever:
		if !present {
			return fmt.Errorf("%w: image %q (preload with `docker pull %s` or `docker load`)",
				ErrImageMissing, cfg.Image, cfg.Image)
		}
	case PullIfNotPresent:
		fallthrough
	default:
		if !present {
			if err := imagePull(ctx, cfg.Image); err != nil {
				return fmt.Errorf("pull %q (policy=IfNotPresent): %w", cfg.Image, err)
			}
		}
	}

	globalImageState.set(cfg.Image, true)
	return nil
}

// DockerAvailable returns nil if `docker version --format {{.Server.Version}}`
// succeeds. Otherwise wraps ErrDockerUnavailable with diagnostic detail.
func DockerAvailable(ctx context.Context) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("%w: docker CLI not on PATH (install Docker, or mount /var/run/docker.sock into wolf-slim)", ErrDockerUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s (is the docker daemon running? is /var/run/docker.sock mounted into wolf-slim?)",
			ErrDockerUnavailable, trimSpaces(string(out)))
	}
	return nil
}

// imageInspect returns nil if the image is present locally, otherwise an error.
func imageInspect(ctx context.Context, image string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", image)
	// Suppress all output — exit code is the only signal we care about.
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

// imagePull pulls the named image. The function blocks until the pull
// completes; on a 6+ GB image and a slow link this can be 10+ minutes.
func imagePull(ctx context.Context, image string) error {
	// 30-minute pull cap — generous enough for a fresh 8 GB pull on a slow link.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "pull", image)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, trimSpaces(string(out)))
	}
	return nil
}

// trimSpaces is a tiny helper that avoids pulling in strings just for TrimSpace
// at the top of files that don't already import it.
func trimSpaces(s string) string {
	// Drop trailing newlines for cleaner error messages.
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
