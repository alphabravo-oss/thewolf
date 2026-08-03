package scannerbuild

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	for _, relative := range []string{"go.mod", "go.sum", "cmd/wolf", "internal", "plugins"} {
		if _, err := os.Stat(filepath.Join(dir, relative)); err != nil {
			t.Fatalf("expected fixer build source %s materialized: %v", relative, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "_wolf-source")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("encoded source prefix leaked into Docker context: %v", err)
	}
}

func TestEmbeddedFixerContextContainsEveryLocalCopySource(t *testing.T) {
	root := t.TempDir()
	if err := Materialize(root); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	for _, variant := range FixerVariants {
		contextDir, dockerfile := buildContextPaths(root, variant)
		if contextDir != root {
			t.Errorf("%s context=%q, want repository root %q", variant.Name, contextDir, root)
			continue
		}
		validateLocalCopySources(t, contextDir, dockerfile)
	}
}

func validateLocalCopySources(t *testing.T, contextDir, dockerfile string) {
	t.Helper()
	input, err := os.Open(dockerfile)
	if err != nil {
		t.Errorf("open Dockerfile %s: %v", dockerfile, err)
		return
	}
	defer input.Close()
	scanner := bufio.NewScanner(input)
	line := 0
	for scanner.Scan() {
		line++
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || fields[0] != "COPY" || strings.HasPrefix(fields[1], "--from=") {
			continue
		}
		for _, source := range fields[1 : len(fields)-1] {
			if strings.ContainsAny(source, "$*?[{") {
				t.Fatalf("unsupported dynamic COPY source at %s:%d: %s", dockerfile, line, source)
			}
			if _, err := os.Stat(filepath.Join(contextDir, filepath.FromSlash(source))); err != nil {
				t.Errorf("local COPY source %q at %s:%d is unavailable: %v", source, dockerfile, line, err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Errorf("read Dockerfile %s: %v", dockerfile, err)
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

func TestBuildUsesEphemeralDockerConfigAndRemovesIt(t *testing.T) {
	const token = "dckr_pat_EPHEMERAL_ONLY"
	cases := []struct {
		name   string
		build  func(context.Context) error
		cancel bool
	}{
		{
			name: "success",
			build: func(context.Context) error {
				return nil
			},
		},
		{
			name: "failure",
			build: func(context.Context) error {
				return errors.New("build failed")
			},
		},
		{
			name:   "cancellation",
			cancel: true,
			build: func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			previousLogin := runDockerLoginFn
			previousBuild := runStreamingFn
			defer func() {
				runDockerLoginFn = previousLogin
				runStreamingFn = previousBuild
			}()
			var loginConfig, buildConfig string
			var loginArgs, buildArgsSeen []string
			var loginStdin string
			runDockerLoginFn = func(
				_ context.Context,
				name string,
				argv []string,
				stdin string,
				_ func(string),
			) error {
				if name != "docker" {
					t.Fatalf("login command = %q", name)
				}
				loginArgs = append([]string(nil), argv...)
				loginConfig = argumentAfter(argv, "--config")
				loginStdin = stdin
				assertPrivateDirectory(t, loginConfig)
				return nil
			}
			runStreamingFn = func(
				ctx context.Context,
				name string,
				argv []string,
				_ func(string),
			) error {
				if name != "docker" {
					t.Fatalf("build command = %q", name)
				}
				buildArgsSeen = append([]string(nil), argv...)
				buildConfig = argumentAfter(argv, "--config")
				assertPrivateDirectory(t, buildConfig)
				return testCase.build(ctx)
			}
			ctx := context.Background()
			var cancel context.CancelFunc
			if testCase.cancel {
				ctx, cancel = context.WithCancel(ctx)
				go cancel()
			}
			var lines []string
			_, _ = Build(ctx, BuildRequest{
				Variant: "default", Namespace: "acme", Version: "2.0.0",
				Push: true, DockerHubUser: "registry-user",
				DockerHubPAT: token,
			}, func(line string) { lines = append(lines, line) })
			if loginConfig == "" || loginConfig != buildConfig {
				t.Fatalf("login config=%q build config=%q", loginConfig, buildConfig)
			}
			if loginStdin != token {
				t.Fatal("registry token was not delivered exactly through stdin")
			}
			for _, value := range append(loginArgs, buildArgsSeen...) {
				if strings.Contains(value, token) {
					t.Fatalf("token leaked into argv: %#v", value)
				}
			}
			joined := strings.Join(lines, "\n")
			if strings.Contains(joined, token) ||
				strings.Contains(joined, "registry-user") {
				t.Fatalf("credential material leaked into logs: %s", joined)
			}
			if _, err := os.Stat(loginConfig); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("ephemeral config survived %s: %v", testCase.name, err)
			}
		})
	}
}

func TestConcurrentBuildsUseDistinctEphemeralDockerConfigs(t *testing.T) {
	previousLogin := runDockerLoginFn
	previousBuild := runStreamingFn
	defer func() {
		runDockerLoginFn = previousLogin
		runStreamingFn = previousBuild
	}()
	var mutex sync.Mutex
	var loginConfigs, buildConfigs []string
	runDockerLoginFn = func(
		_ context.Context,
		_ string,
		argv []string,
		_ string,
		_ func(string),
	) error {
		config := argumentAfter(argv, "--config")
		assertPrivateDirectory(t, config)
		mutex.Lock()
		loginConfigs = append(loginConfigs, config)
		mutex.Unlock()
		return nil
	}
	runStreamingFn = func(
		_ context.Context,
		_ string,
		argv []string,
		_ func(string),
	) error {
		config := argumentAfter(argv, "--config")
		assertPrivateDirectory(t, config)
		mutex.Lock()
		buildConfigs = append(buildConfigs, config)
		mutex.Unlock()
		return nil
	}
	var wait sync.WaitGroup
	errorsChannel := make(chan error, 2)
	for _, variant := range []string{"default", "jvm"} {
		wait.Add(1)
		go func(variant string) {
			defer wait.Done()
			_, err := Build(context.Background(), BuildRequest{
				Variant: variant, Namespace: "acme", Version: "2.0.0",
				Push: true, DockerHubUser: "user", DockerHubPAT: "token",
			}, nil)
			errorsChannel <- err
		}(variant)
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(loginConfigs) != 2 || len(buildConfigs) != 2 {
		t.Fatalf("login configs=%#v build configs=%#v", loginConfigs, buildConfigs)
	}
	if loginConfigs[0] == loginConfigs[1] {
		t.Fatalf("concurrent builds shared config %q", loginConfigs[0])
	}
	buildSet := map[string]bool{buildConfigs[0]: true, buildConfigs[1]: true}
	for _, config := range loginConfigs {
		if !buildSet[config] {
			t.Fatalf("login/build config mismatch: login=%#v build=%#v", loginConfigs, buildConfigs)
		}
		if _, err := os.Stat(config); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("concurrent ephemeral config survived: %v", err)
		}
	}
}

func argumentAfter(arguments []string, name string) string {
	for index := range arguments {
		if arguments[index] == name && index+1 < len(arguments) {
			return arguments[index+1]
		}
	}
	return ""
}

func assertPrivateDirectory(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat ephemeral config: %v", err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("ephemeral config mode = %v", info.Mode())
	}
}

// TestFixerVariantsResolve verifies the parallel fixer build table: each
// engine variant resolves, carries the wolf-fixer repo base, and points at a
// Dockerfile in the fixer/ context subtree.
func TestFixerVariantsResolve(t *testing.T) {
	wantSuffix := map[string]string{
		"base":   "",
		"claude": "-claude",
		"codex":  "-codex",
		"api":    "-api",
	}
	for name, suffix := range wantSuffix {
		v, ok := FixerVariantByName(name)
		if !ok {
			t.Fatalf("FixerVariantByName(%q) not found", name)
		}
		if v.ImageBase != "wolf-fixer" {
			t.Fatalf("%s ImageBase = %q, want wolf-fixer", name, v.ImageBase)
		}
		if v.ImageSuffix != suffix {
			t.Fatalf("%s ImageSuffix = %q, want %q", name, v.ImageSuffix, suffix)
		}
		if v.ContextSubdir != "fixer" {
			t.Fatalf("%s ContextSubdir = %q, want fixer", name, v.ContextSubdir)
		}
		repo := imageRepo("acme", v)
		if repo != "acme/wolf-fixer"+suffix {
			t.Fatalf("%s imageRepo = %q, want acme/wolf-fixer%s", name, repo, suffix)
		}
	}
	// Scanner variants are still resolvable and unchanged (wolf-scanners base).
	if _, ok := FixerVariantByName("default"); ok {
		t.Fatalf("scanner variant leaked into FixerVariantByName")
	}
	def, _ := VariantByName("default")
	if imageRepo("acme", def) != "acme/wolf-scanners" {
		t.Fatalf("scanner default repo regressed: %q", imageRepo("acme", def))
	}
}

// TestFixerBuildUsesFixerContextAndDockerfile drives Build for a fixer variant
// with a stubbed runner and asserts the argv targets the materialized
// fixer/Dockerfile.api under the repository-shaped context, tagged wolf-fixer-api.
func TestFixerBuildUsesFixerContextAndDockerfile(t *testing.T) {
	t.Setenv("WOLF_SCANNERS_TAG", "dev")

	var argvSeen []string
	restore := stubRunStreaming(func(_ string, argv []string, onLine func(string)) error {
		argvSeen = argv
		return nil
	})
	defer restore()

	res, err := Build(testCtx(), BuildRequest{
		Variant:   "api",
		Namespace: "acme",
		Version:   "2.0.0",
		Push:      false,
	}, nil)
	if err != nil {
		t.Fatalf("Build(api): %v", err)
	}
	// --file must point at a fixer/Dockerfile.api path.
	var dockerfileArg string
	for i, a := range argvSeen {
		if a == "--file" && i+1 < len(argvSeen) {
			dockerfileArg = argvSeen[i+1]
		}
	}
	if !strings.Contains(dockerfileArg, filepath.Join("fixer", "Dockerfile.api")) {
		t.Fatalf("--file = %q, want a fixer/Dockerfile.api path", dockerfileArg)
	}
	// Context dir (last positional) must be the repository-shaped root so the
	// fixer Dockerfile can COPY cmd/, internal/, plugins/, scanners/, and fixer/.
	ctxDir := argvSeen[len(argvSeen)-1]
	if filepath.Base(ctxDir) != "context" {
		t.Fatalf("context dir = %q, want a .../context root", ctxDir)
	}
	if !equalStrings(res.Refs, []string{
		"acme/wolf-fixer-api:2.0.0",
		"acme/wolf-fixer-api:latest",
		"acme/wolf-fixer-api:dev",
	}) {
		t.Fatalf("refs = %v", res.Refs)
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

func TestBumpPatch(t *testing.T) {
	cases := map[string]string{
		"2.0.0":  "2.0.1",
		"2.0.9":  "2.0.10",
		"1.4.0":  "1.4.1",
		"v2.0.0": "v2.0.1",
		"":       "0.0.1",
		"weird":  "weird.1", // non-semver still yields a distinct tag
	}
	for in, want := range cases {
		if got := BumpPatch(in); got != want {
			t.Errorf("BumpPatch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsMultiPlatform(t *testing.T) {
	if !IsMultiPlatform("linux/amd64,linux/arm64") {
		t.Error("comma-separated platforms should be multi")
	}
	if IsMultiPlatform("linux/arm64") {
		t.Error("single platform should not be multi")
	}
	if IsMultiPlatform("") {
		t.Error("empty should not be multi")
	}
}

func TestBuildArgsAddsPlatform(t *testing.T) {
	req := BuildRequest{Version: "2.0.1", Push: true, Platforms: "linux/amd64,linux/arm64"}
	argv := buildArgs(req, "/ctx", "Dockerfile", []string{"2.0.1"}, "acme/wolf-scanners")
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--platform linux/amd64,linux/arm64") {
		t.Errorf("expected --platform in argv, got: %v", argv)
	}
	if !strings.Contains(joined, "--push") {
		t.Errorf("multi-arch build must push, got: %v", argv)
	}
	// A single-arch (no platforms) build must NOT add --platform.
	argv2 := buildArgs(BuildRequest{Version: "2.0.1"}, "/ctx", "Dockerfile", []string{"2.0.1"}, "acme/wolf-scanners")
	if strings.Contains(strings.Join(argv2, " "), "--platform") {
		t.Errorf("single-arch build should not add --platform, got: %v", argv2)
	}
}
