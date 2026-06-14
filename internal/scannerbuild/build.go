package scannerbuild

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Variant describes one of the four wolf-built scanner images: which
// Dockerfile builds it and what suffix its DockerHub image repo carries.
type Variant struct{ Name, Dockerfile, ImageSuffix string }

// Variants is the build table. "default" has no image suffix; the bucket
// images append "-jvm"/"-rust"/"-codeql" to the repo name.
var Variants = []Variant{
	{"default", "Dockerfile", ""},
	{"jvm", "Dockerfile.jvm", "-jvm"},
	{"rust", "Dockerfile.rust", "-rust"},
	{"codeql", "Dockerfile.codeql", "-codeql"},
}

// VariantByName returns the Variant with the given name, or false.
func VariantByName(name string) (Variant, bool) {
	for _, v := range Variants {
		if v.Name == name {
			return v, true
		}
	}
	return Variant{}, false
}

// imageBase is the DockerHub repo for the default image; bucket suffixes are
// appended to this. Kept here so the build path and the runtime resolver agree.
const imageBase = "wolf-scanners"

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

// imageRepo returns "<namespace>/<base><suffix>" for a variant.
func imageRepo(namespace string, v Variant) string {
	return namespace + "/" + imageBase + v.ImageSuffix
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
		return BuildResult{}, fmt.Errorf("unknown variant %q", req.Variant)
	}
	namespace := strings.TrimSpace(req.Namespace)
	if namespace == "" {
		namespace = defaultNamespace
	}

	dir, err := os.MkdirTemp("", "wolf-scannerbuild-*")
	if err != nil {
		return BuildResult{}, fmt.Errorf("create temp context dir: %w", err)
	}
	defer os.RemoveAll(dir)
	if err := Materialize(dir); err != nil {
		return BuildResult{}, fmt.Errorf("materialize build context: %w", err)
	}

	// Login only on push — a local build needs no credentials at all.
	if req.Push {
		if err := dockerLogin(ctx, req.DockerHubUser, req.DockerHubPAT, onLine); err != nil {
			return BuildResult{}, err
		}
	}

	repo := imageRepo(namespace, v)
	tags := tagList(req.Version, activeRuntimeTagFn())
	dockerfile := v.Dockerfile
	argv := buildArgs(req, dir, dockerfile, tags, repo)

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
func dockerLogin(ctx context.Context, user, pat string, onLine func(string)) error {
	if strings.TrimSpace(user) == "" || pat == "" {
		return fmt.Errorf("docker login requires a username and token")
	}
	argv := []string{"login", "-u", user, "--password-stdin"}
	onLine("docker login -u " + user + " --password-stdin (token redacted)")
	cmd := exec.CommandContext(ctx, "docker", argv...)
	cmd.Stdin = strings.NewReader(pat)
	out, err := cmd.CombinedOutput()
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line != "" {
			onLine(line)
		}
	}
	if err != nil {
		return fmt.Errorf("docker login failed: %w", err)
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
