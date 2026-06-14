package scannerbuild

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testCtx() context.Context { return context.Background() }

// stubRunStreaming swaps the package-level runner with fn (ignoring ctx) and
// returns a restore func. fn receives (name, argv, onLine).
func stubRunStreaming(fn func(name string, argv []string, onLine func(string)) error) func() {
	prev := runStreamingFn
	runStreamingFn = func(_ context.Context, name string, argv []string, onLine func(string)) error {
		return fn(name, argv, onLine)
	}
	return func() { runStreamingFn = prev }
}

func TestMaterialize(t *testing.T) {
	dir := t.TempDir()
	if err := Materialize(dir); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	dockerfile := filepath.Join(dir, "Dockerfile")
	if _, err := os.Stat(dockerfile); err != nil {
		t.Fatalf("expected Dockerfile materialized: %v", err)
	}

	goTools := filepath.Join(dir, "install", "go-tools.sh")
	info, err := os.Stat(goTools)
	if err != nil {
		t.Fatalf("expected install/go-tools.sh materialized: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("install/go-tools.sh is not executable, mode=%v", info.Mode().Perm())
	}
}

func TestTagListDedup(t *testing.T) {
	cases := []struct {
		name       string
		version    string
		runtimeTag string
		want       []string
	}{
		{
			name:       "distinct tags preserved",
			version:    "3.1.0",
			runtimeTag: "dev",
			want:       []string{"3.1.0", "latest", "dev"},
		},
		{
			name:       "runtime tag equals version is deduped",
			version:    "2.0.0",
			runtimeTag: "2.0.0",
			want:       []string{"2.0.0", "latest"},
		},
		{
			name:       "runtime tag equals latest is deduped",
			version:    "9.9.9",
			runtimeTag: "latest",
			want:       []string{"9.9.9", "latest"},
		},
		{
			name:       "empty runtime tag dropped",
			version:    "1.2.3",
			runtimeTag: "",
			want:       []string{"1.2.3", "latest"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tagList(tc.version, tc.runtimeTag)
			if !equalStrings(got, tc.want) {
				t.Fatalf("tagList(%q,%q) = %v, want %v", tc.version, tc.runtimeTag, got, tc.want)
			}
		})
	}
}

func TestBuildArgsLocalUsesLoadNotPush(t *testing.T) {
	req := BuildRequest{Variant: "default", Namespace: "acme", Version: "2.0.0", Push: false}
	tags := tagList(req.Version, "dev")
	argv := buildArgs(req, "/ctx", "Dockerfile", tags, "acme/wolf-scanners")

	if !containsArg(argv, "--load") {
		t.Fatalf("local build must use --load, argv=%v", argv)
	}
	if containsArg(argv, "--push") {
		t.Fatalf("local build must NOT use --push, argv=%v", argv)
	}
	// The build context dir is the final positional arg.
	if argv[len(argv)-1] != "/ctx" {
		t.Fatalf("expected context dir last, argv=%v", argv)
	}
	// Tags applied: version + latest + runtime tag.
	for _, want := range []string{"acme/wolf-scanners:2.0.0", "acme/wolf-scanners:latest", "acme/wolf-scanners:dev"} {
		if !containsArg(argv, want) {
			t.Fatalf("missing tag %q in argv=%v", want, argv)
		}
	}
}

func TestBuildArgsPushUsesPushNotLoad(t *testing.T) {
	req := BuildRequest{Variant: "default", Namespace: "acme", Version: "2.0.0", Push: true}
	tags := tagList(req.Version, "dev")
	argv := buildArgs(req, "/ctx", "Dockerfile", tags, "acme/wolf-scanners")

	if !containsArg(argv, "--push") {
		t.Fatalf("push build must use --push, argv=%v", argv)
	}
	if containsArg(argv, "--load") {
		t.Fatalf("push build must NOT use --load, argv=%v", argv)
	}
}

// TestBuildArgsNoLoginAndNoPATLeak verifies the central security invariant:
// the build argv (and its echoed string) never contains the PAT, and for a
// push=false request no docker login is constructed at all. The argv builder
// is pure, so we exercise it directly with a sentinel PAT.
func TestBuildArgsNoPATLeak(t *testing.T) {
	const pat = "dckr_pat_SUPER_SECRET_TOKEN_VALUE"
	req := BuildRequest{
		Variant:       "default",
		Namespace:     "acme",
		Version:       "2.0.0",
		Push:          true,
		DockerHubUser: "operator",
		DockerHubPAT:  pat,
	}
	tags := tagList(req.Version, "dev")
	argv := buildArgs(req, "/ctx", "Dockerfile", tags, "acme/wolf-scanners")

	for _, a := range argv {
		if strings.Contains(a, pat) {
			t.Fatalf("PAT leaked into build argv: %v", argv)
		}
	}
	if strings.Contains(echoCommand(argv), pat) {
		t.Fatalf("PAT leaked into echoed command: %q", echoCommand(argv))
	}
}

// TestLocalBuildConstructsNoLogin asserts the local-first contract at the
// plan level: a push=false build neither emits a docker login line nor
// references credentials. We drive Build with a stubbed runner so no real
// docker is invoked, and capture every streamed line.
func TestLocalBuildConstructsNoLogin(t *testing.T) {
	t.Setenv("WOLF_SCANNERS_TAG", "dev")

	var argvSeen []string
	restore := stubRunStreaming(func(_ string, argv []string, onLine func(string)) error {
		argvSeen = argv
		onLine("#1 building")
		return nil
	})
	defer restore()

	const pat = "dckr_pat_NEVER_LOGS"
	var lines []string
	res, err := Build(testCtx(), BuildRequest{
		Variant:       "default",
		Namespace:     "acme",
		Version:       "2.0.0",
		Push:          false,
		DockerHubUser: "operator",
		DockerHubPAT:  pat,
	}, func(s string) { lines = append(lines, s) })
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !res.LoadedLocally {
		t.Fatalf("expected LoadedLocally=true for push=false")
	}

	joined := strings.Join(lines, "\n")
	if strings.Contains(strings.ToLower(joined), "docker login") {
		t.Fatalf("push=false build must construct NO docker login; got logs:\n%s", joined)
	}
	if strings.Contains(joined, pat) {
		t.Fatalf("PAT leaked into streamed output:\n%s", joined)
	}
	if !containsArg(argvSeen, "--load") || containsArg(argvSeen, "--push") {
		t.Fatalf("push=false must build with --load not --push, argv=%v", argvSeen)
	}
	// version (2.0.0) + latest + runtime tag (dev), deduped.
	wantRefs := []string{
		"acme/wolf-scanners:2.0.0",
		"acme/wolf-scanners:latest",
		"acme/wolf-scanners:dev",
	}
	if !equalStrings(res.Refs, wantRefs) {
		t.Fatalf("refs = %v, want %v", res.Refs, wantRefs)
	}
}

func containsArg(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
