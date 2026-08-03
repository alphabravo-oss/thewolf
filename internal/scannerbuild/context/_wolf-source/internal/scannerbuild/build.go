package scannerbuild

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Variant describes one wolf-built image: which Dockerfile builds it
// (relative to its ContextSubdir inside the embedded build context), what
// DockerHub repo base it uses, and what suffix that repo carries. The two
// build tables — Variants (scanners) and FixerVariants (the autonomous-fix
// engine containers) — share this shape and the same Build path.
type Variant struct {
	// Name is the variant selector used by callers and routes.
	Name string
	// Dockerfile is the Dockerfile filename relative to the build context
	// root for this variant (see ContextSubdir).
	Dockerfile string
	// ImageBase is the DockerHub repo base (e.g. "wolf-scanners" or
	// "wolf-fixer"). Bucket/engine suffixes are appended to it.
	ImageBase string
	// ImageSuffix is appended to ImageBase to form the repo (e.g. "-jvm",
	// "-claude"); empty for the primary image of a set.
	ImageSuffix string
	// ContextSubdir is the subdirectory of the materialized build context
	// the Dockerfile is resolved against. "" means the context root
	// (scanners); "fixer" means the fixer/ subtree.
	ContextSubdir string
}

// Variants is the scanner build table. "default" has no image suffix; the
// bucket images append "-jvm"/"-rust"/"-codeql" to the repo name.
var Variants = []Variant{
	{Name: "default", Dockerfile: "Dockerfile", ImageBase: imageBase, ImageSuffix: ""},
	{Name: "jvm", Dockerfile: "Dockerfile.jvm", ImageBase: imageBase, ImageSuffix: "-jvm"},
	{Name: "rust", Dockerfile: "Dockerfile.rust", ImageBase: imageBase, ImageSuffix: "-rust"},
	{Name: "codeql", Dockerfile: "Dockerfile.codeql", ImageBase: imageBase, ImageSuffix: "-codeql"},
}

// FixerVariants is the parallel build table for the autonomous-fix engine
// containers. They live under fixer/ and share the wolf-fixer repo base.
// "base" is the shared image the engine variants FROM; "claude"/"codex"
// add the respective agent CLI; "api" is CLI-free (the zero-auth fallback).
var FixerVariants = []Variant{
	{Name: "base", Dockerfile: "Dockerfile.base", ImageBase: fixerImageBase, ImageSuffix: "", ContextSubdir: fixerContextSubdir},
	{Name: "claude", Dockerfile: "Dockerfile.claude", ImageBase: fixerImageBase, ImageSuffix: "-claude", ContextSubdir: fixerContextSubdir},
	{Name: "codex", Dockerfile: "Dockerfile.codex", ImageBase: fixerImageBase, ImageSuffix: "-codex", ContextSubdir: fixerContextSubdir},
	{Name: "api", Dockerfile: "Dockerfile.api", ImageBase: fixerImageBase, ImageSuffix: "-api", ContextSubdir: fixerContextSubdir},
}

// VariantByName returns the scanner Variant with the given name, or false.
func VariantByName(name string) (Variant, bool) {
	for _, v := range Variants {
		if v.Name == name {
			return v, true
		}
	}
	return Variant{}, false
}

// FixerVariantByName returns the fixer Variant with the given name, or false.
func FixerVariantByName(name string) (Variant, bool) {
	for _, v := range FixerVariants {
		if v.Name == name {
			return v, true
		}
	}
	return Variant{}, false
}

// imageBase is the DockerHub repo for the default scanner image; bucket
// suffixes are appended to this. Kept here so the build path and the runtime
// resolver agree.
const imageBase = "wolf-scanners"

// fixerImageBase is the DockerHub repo base for the autonomous-fix engine
// containers; engine suffixes ("-claude"/"-codex"/"-api") are appended.
const fixerImageBase = "wolf-fixer"

// fixerContextSubdir is the materialized build-context subtree the fixer
// Dockerfiles are resolved against (mirrors the repo's fixer/ directory).
const fixerContextSubdir = "fixer"

// defaultNamespace is the DockerHub namespace used when a build request
// leaves Namespace empty.
const defaultNamespace = "alphabravodevops"

// BuildRequest is one image build. Push gates the only credential-requiring
// path: when false, no docker login happens and DockerHubUser/DockerHubPAT
// are ignored — the image is loaded into the local daemon instead.
type BuildRequest struct {
	Variant       string
	Namespace     string
	Version       string
	Push          bool
	DockerHubUser string
	DockerHubPAT  string
	// Platforms, when non-empty (e.g. "linux/amd64,linux/arm64"), builds a
	// multi-arch image via buildx --platform. A multi-platform build can only
	// be --push'ed (a manifest list can't be --load'ed into the local daemon),
	// so Push must be true when Platforms names more than one platform.
	Platforms string
}

// BuildResult reports what a Build produced.
type BuildResult struct {
	// Refs are the fully-qualified image references that were tagged.
	Refs []string
	// Digest is the image digest if it could be parsed from buildx output
	// (only meaningful on a --push build); otherwise empty.
	Digest string
	// LoadedLocally is true when the image was --load-ed into the local
	// daemon (i.e. a non-push build) rather than pushed to a registry.
	LoadedLocally bool
}

// activeRuntimeTagFn resolves the tag the runtime currently pulls. It is a
// package var so tests can pin it without touching the environment.
var activeRuntimeTagFn = activeRuntimeTag

// runStreamingFn runs the docker command and streams output. It is a package
// var so tests can stub it without invoking a real docker daemon.
var runStreamingFn = runStreaming

// runDockerLoginFn is the shell-free docker-login seam used by tests. The
// credential is supplied only through stdin and is never added to argv.
var runDockerLoginFn = runDockerLogin

// activeRuntimeTag returns the tag the runtime resolves from WOLF_SCANNERS_TAG,
// matching the default in internal/setup/scanners. Tagging a fresh local build
// with this tag means the next scan finds the image already loaded — no
// registry round-trip — even though the runtime default is a fixed version.
func activeRuntimeTag() string {
	if v := strings.TrimSpace(os.Getenv("WOLF_SCANNERS_TAG")); v != "" {
		return v
	}
	return "2.0.0"
}

// imageRepo returns "<namespace>/<base><suffix>" for a variant. The base is
// carried on the variant (wolf-scanners for scanners, wolf-fixer for fixer
// engine containers).
func imageRepo(namespace string, v Variant) string {
	return namespace + "/" + v.ImageBase + v.ImageSuffix
}

// tagList returns the deduped tag list for a build: the version, "latest",
// and the active runtime tag (so a local build is immediately picked up by
// the next scan). Order is stable: version, latest, runtime-tag; duplicates
// are dropped while preserving first occurrence.
func tagList(version, runtimeTag string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, t := range []string{version, "latest", runtimeTag} {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// buildArgs constructs the `docker buildx build ...` argv for a request. It is
// pure (no I/O) so it can be unit-tested without a docker daemon. The PAT is
// never referenced here — credentials never reach argv; login happens
// separately over stdin. The push flag selects --push vs --load:
//   - push=true  → --push (publishes to the registry)
//   - push=false → --load (loads into the local daemon; no creds needed)
func buildArgs(req BuildRequest, contextDir, dockerfile string, tags []string, repo string) []string {
	args := []string{
		"buildx", "build",
		"--file", dockerfile,
		"--build-arg", "WOLF_VERSION=" + req.Version,
	}
	if p := strings.TrimSpace(req.Platforms); p != "" {
		args = append(args, "--platform", p)
	}
	for _, t := range tags {
		args = append(args, "-t", repo+":"+t)
	}
	if req.Push {
		args = append(args, "--push")
	} else {
		args = append(args, "--load")
	}
	args = append(args, contextDir)
	return args
}

func buildContextPaths(contextRoot string, variant Variant) (string, string) {
	dockerfile := filepath.Join(contextRoot, variant.Dockerfile)
	if variant.ContextSubdir != "" {
		dockerfile = filepath.Join(contextRoot, variant.ContextSubdir, variant.Dockerfile)
	}
	return contextRoot, dockerfile
}

// IsMultiPlatform reports whether a platforms spec names more than one platform
// (so the build must be --push'ed, not --load'ed).
func IsMultiPlatform(platforms string) bool {
	return strings.Contains(strings.TrimSpace(platforms), ",")
}

// BumpPatch increments the patch component of a MAJOR.MINOR.PATCH version
// string ("2.0.0" -> "2.0.1"). A leading "v" is preserved. If the input isn't
// dotted-numeric semver, it falls back to appending ".1" so a build still
// produces a distinct, monotonic-ish tag rather than failing.
func BumpPatch(version string) string {
	v := strings.TrimSpace(version)
	prefix := ""
	if strings.HasPrefix(v, "v") {
		prefix, v = "v", v[1:]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		if v == "" {
			return prefix + "0.0.1"
		}
		return prefix + v + ".1"
	}
	major, e1 := strconv.Atoi(parts[0])
	minor, e2 := strconv.Atoi(parts[1])
	patch, e3 := strconv.Atoi(parts[2])
	if e1 != nil || e2 != nil || e3 != nil {
		return prefix + v + ".1"
	}
	return fmt.Sprintf("%s%d.%d.%d", prefix, major, minor, patch+1)
}

// echoCommand renders a docker invocation for logging. It is the single place
// that turns argv into a human string; because the PAT is never in argv (login
// uses --password-stdin) it cannot leak here either.
func echoCommand(argv []string) string {
	return "docker " + strings.Join(argv, " ")
}

// Build materializes the embedded context, optionally logs in to DockerHub
// (push only), then runs `docker buildx build`, streaming combined stdout+stderr
// line-by-line through onLine. The PAT is passed to `docker login` over stdin
// and never appears in argv or any echoed command.
func Build(ctx context.Context, req BuildRequest, onLine func(string)) (BuildResult, error) {
	if onLine == nil {
		onLine = func(string) {}
	}
	v, ok := VariantByName(req.Variant)
	if !ok {
		v, ok = FixerVariantByName(req.Variant)
	}
	if !ok {
		return BuildResult{}, fmt.Errorf("unknown variant %q", req.Variant)
	}
	namespace := strings.TrimSpace(req.Namespace)
	if namespace == "" {
		namespace = defaultNamespace
	}

	operationDir, err := os.MkdirTemp("", "wolf-scannerbuild-*")
	if err != nil {
		return BuildResult{}, fmt.Errorf("create temp context dir: %w", err)
	}
	defer os.RemoveAll(operationDir)
	contextRoot := filepath.Join(operationDir, "context")
	if err := os.Mkdir(contextRoot, 0o700); err != nil {
		return BuildResult{}, fmt.Errorf("create build context dir: %w", err)
	}
	dockerConfigDir := filepath.Join(operationDir, "docker-config")
	if err := os.Mkdir(dockerConfigDir, 0o700); err != nil {
		return BuildResult{}, fmt.Errorf("create ephemeral docker config dir: %w", err)
	}
	if err := Materialize(contextRoot); err != nil {
		return BuildResult{}, fmt.Errorf("materialize build context: %w", err)
	}

	// Login only on push — a local build needs no credentials at all.
	if req.Push {
		if err := dockerLogin(
			ctx, dockerConfigDir, req.DockerHubUser, req.DockerHubPAT, onLine,
		); err != nil {
			return BuildResult{}, err
		}
	}

	repo := imageRepo(namespace, v)
	tags := tagList(req.Version, activeRuntimeTagFn())
	// Every variant uses the materialized repository-shaped root as its context.
	// Fixer Dockerfiles live under fixer/, but COPY Wolf source and scanner lock
	// inputs from the repository root.
	contextDir, dockerfile := buildContextPaths(contextRoot, v)
	argv := append(
		[]string{"--config", dockerConfigDir},
		buildArgs(req, contextDir, dockerfile, tags, repo)...,
	)

	onLine(echoCommand(argv))
	if err := runStreamingFn(ctx, "docker", argv, onLine); err != nil {
		return BuildResult{}, err
	}

	refs := make([]string, 0, len(tags))
	for _, t := range tags {
		refs = append(refs, repo+":"+t)
	}
	return BuildResult{
		Refs:          refs,
		LoadedLocally: !req.Push,
	}, nil
}

// dockerLogin runs `docker login -u <user> --password-stdin`, feeding the PAT
// over stdin. The PAT is never in argv. The echoed command redacts it.
func dockerLogin(
	ctx context.Context,
	configDir, user, pat string,
	onLine func(string),
) error {
	if strings.TrimSpace(user) == "" || pat == "" {
		return fmt.Errorf("docker login requires a username and token")
	}
	argv := []string{
		"--config", configDir, "login", "-u", user, "--password-stdin",
	}
	onLine("docker --config [ephemeral] login -u [redacted] --password-stdin (token redacted)")
	return runDockerLoginFn(ctx, "docker", argv, pat, onLine)
}

func runDockerLogin(
	ctx context.Context,
	name string,
	argv []string,
	pat string,
	onLine func(string),
) error {
	cmd := exec.CommandContext(ctx, name, argv...)
	cmd.Stdin = strings.NewReader(pat)
	out, err := cmd.CombinedOutput()
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line != "" {
			onLine(line)
		}
	}
	if err != nil {
		return fmt.Errorf("%s login failed: %w", name, err)
	}
	return nil
}

// runStreaming runs a command, merging stdout+stderr and emitting each line
// through onLine as it arrives.
func runStreaming(ctx context.Context, name string, argv []string, onLine func(string)) error {
	cmd := exec.CommandContext(ctx, name, argv...)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		pw.Close()
		return fmt.Errorf("start %s: %w", name, err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			onLine(sc.Text())
		}
	}()

	waitErr := cmd.Wait()
	pw.Close()
	<-done
	if waitErr != nil {
		return fmt.Errorf("%s build failed: %w", name, waitErr)
	}
	return nil
}
