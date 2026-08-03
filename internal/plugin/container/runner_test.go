package container

import (
	"strings"
	"testing"
)

// argList wraps []string so tests can read it as one string for assertions.
type argList []string

func (a argList) String() string { return strings.Join([]string(a), " ") }

func newTestCfg() *Config {
	return &Config{
		Image:                "wolf-scanners:test",
		PullPolicy:           PullIfNotPresent,
		Network:              "bridge",
		UID:                  1000,
		GID:                  1000,
		HostReposRoot:        "/host/projects",
		InContainerReposRoot: "/repos",
		Memory:               "2g",
		CPUs:                 "1.5",
	}
}

func mustBuildDockerArgs(t *testing.T, cfg *Config, opts Options, tool string, args ...string) (string, []string) {
	t.Helper()
	name, dockerArgs, err := BuildDockerArgs(cfg, opts, tool, args...)
	if err != nil {
		t.Fatalf("BuildDockerArgs: %v", err)
	}
	return name, dockerArgs
}

func TestBuildDockerArgs_Bandit(t *testing.T) {
	cfg := newTestCfg()
	_, args := mustBuildDockerArgs(t, cfg, Options{RepoDir: "/repos/myrepo"},
		"bandit", "-r", "/scan", "-f", "json", "--exit-zero")

	joined := argList(args).String()

	for _, want := range []string{
		"run", "--rm", "--name", "wolf-scan-bandit-",
		"--pull never",
		"--user 1000:1000",
		"--read-only",
		"--tmpfs /tmp:rw,size=4g,mode=1777",
		"-v /host/projects/myrepo:/scan:ro",
		"--workdir /scan",
		"--network bridge",
		"--memory 2g",
		"--cpus 1.5",
		"wolf-scanners:test bandit -r /scan -f json --exit-zero",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("docker args missing %q\nfull args: %s", want, joined)
		}
	}
}

func TestScannerFailureCommandIncludesMessage(t *testing.T) {
	cmd := scannerFailureCommand(t.Context(), "scanner image for tool \"semgrep\" is not present locally: semgrep/semgrep:1.2.3")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected failure command to exit non-zero")
	}
	if !strings.Contains(string(out), "semgrep/semgrep:1.2.3") {
		t.Fatalf("failure output missing image: %s", out)
	}
}

func TestBuildDockerArgs_WorkDirOverride(t *testing.T) {
	cfg := newTestCfg()
	_, args := mustBuildDockerArgs(t, cfg,
		Options{RepoDir: "/repos/myrepo", WorkDir: "/scan/cmd/foo"},
		"gosec", "-fmt", "json", "./...")
	joined := argList(args).String()
	if !strings.Contains(joined, "--workdir /scan/cmd/foo") {
		t.Errorf("expected --workdir /scan/cmd/foo, got: %s", joined)
	}
}

func TestBuildDockerArgs_NoRepoMount(t *testing.T) {
	cfg := newTestCfg()
	_, args := mustBuildDockerArgs(t, cfg,
		Options{NoRepoMount: true},
		"nuclei", "-target", "https://example.com")
	joined := argList(args).String()
	if strings.Contains(joined, "/scan") {
		t.Errorf("NoRepoMount should not produce a /scan mount, got: %s", joined)
	}
	if !strings.Contains(joined, "--workdir /tmp") {
		t.Errorf("NoRepoMount should default workdir to /tmp, got: %s", joined)
	}
}

func TestBuildDockerArgs_ReadWrite(t *testing.T) {
	cfg := newTestCfg()
	_, args := mustBuildDockerArgs(t, cfg,
		Options{RepoDir: "/repos/x", ReadWrite: true},
		"codeql", "database", "create")
	joined := argList(args).String()
	if !strings.Contains(joined, "-v /host/projects/x:/scan ") &&
		!strings.HasSuffix(joined, "-v /host/projects/x:/scan") {
		// Must NOT have the :ro suffix.
		if strings.Contains(joined, "/scan:ro") {
			t.Errorf("ReadWrite should not produce :ro suffix, got: %s", joined)
		}
	}
}

func TestBuildDockerArgs_ExtraEnvSortedAndIncluded(t *testing.T) {
	cfg := newTestCfg()
	cfg.ExtraEnv = map[string]string{"GLOBAL_KEY": "g"}
	opts := Options{
		RepoDir:  "/repos/x",
		ExtraEnv: map[string]string{"LOCAL_KEY": "l", "AAA": "1"},
	}
	_, args := mustBuildDockerArgs(t, cfg, opts, "bandit", "/scan")
	joined := argList(args).String()
	// All three vars present.
	for _, key := range []string{"GLOBAL_KEY=g", "LOCAL_KEY=l", "AAA=1"} {
		if !strings.Contains(joined, key) {
			t.Errorf("missing env %q in args: %s", key, joined)
		}
	}
	// Sorted: AAA < GLOBAL_KEY < LOCAL_KEY. Check the relative order.
	idxAAA := strings.Index(joined, "AAA=")
	idxG := strings.Index(joined, "GLOBAL_KEY=")
	idxL := strings.Index(joined, "LOCAL_KEY=")
	if !(idxAAA < idxG && idxG < idxL) {
		t.Errorf("env vars not sorted alphabetically; got positions AAA=%d GLOBAL=%d LOCAL=%d", idxAAA, idxG, idxL)
	}
}

func TestBuildDockerArgsNeverForwardsReleaseEngineOrRegistryCredentials(t *testing.T) {
	for _, name := range []string{
		"DOCKER_HOST", "DOCKER_CONFIG", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY",
		"WOLF_SCANNER_RELEASE_REGISTRY_CREDENTIAL_DIR", "WOLF_SCANNER_RELEASE_ENGINE_CREDENTIAL_DIR",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := newTestCfg()
			cfg.DockerEnvironment = []string{"DOCKER_HOST=tcp://engine.example:2376", "DOCKER_CERT_PATH=/run/engine"}
			cfg.ExtraEnv = map[string]string{name: "credential-canary"}
			if _, _, err := BuildDockerArgs(cfg, Options{RepoDir: "/repos/x"}, "bandit", "/scan"); err == nil {
				t.Fatalf("credential environment %q was accepted for a scanner container", name)
			}
		})
	}

	cfg := newTestCfg()
	cfg.DockerEnvironment = []string{
		"DOCKER_HOST=tcp://engine.example:2376",
		"DOCKER_CONFIG=/run/registry",
		"DOCKER_CERT_PATH=/run/engine",
		"DOCKER_TLS_VERIFY=1",
	}
	_, args := mustBuildDockerArgs(t, cfg, Options{RepoDir: "/repos/x"}, "bandit", "/scan")
	joined := strings.Join(args, " ")
	for _, secret := range cfg.DockerEnvironment {
		if strings.Contains(joined, secret) {
			t.Fatalf("Docker client credential environment leaked into scanner argv: %s", joined)
		}
	}
}

func TestBuildDockerArgs_ExtraMounts(t *testing.T) {
	cfg := newTestCfg()
	_, args := mustBuildDockerArgs(t, cfg,
		Options{RepoDir: "/repos/x", ExtraMounts: []string{"wolf-semgrep-cache:/cache"}},
		"semgrep", "/scan")
	joined := argList(args).String()
	if !strings.Contains(joined, "-v wolf-semgrep-cache:/cache") {
		t.Errorf("ExtraMounts not propagated; args: %s", joined)
	}
}

func TestBuildDockerArgs_RejectsDockerSocketExtraMount(t *testing.T) {
	cfg := newTestCfg()
	if _, _, err := BuildDockerArgs(cfg,
		Options{RepoDir: "/repos/x", ExtraMounts: []string{"/var/run/docker.sock:/var/run/docker.sock"}},
		"semgrep", "/scan"); err == nil {
		t.Fatal("expected docker socket extra mount to be rejected")
	}
}

func TestBuildDockerArgs_DBVolume(t *testing.T) {
	cfg := newTestCfg()
	cfg.DBVolume = "wolf-scanners-db"
	_, args := mustBuildDockerArgs(t, cfg, Options{RepoDir: "/repos/x"},
		"trivy", "fs", "/scan")
	joined := argList(args).String()
	if !strings.Contains(joined, "-v wolf-scanners-db:/var/lib/wolf-db") {
		t.Errorf("DBVolume not mounted; args: %s", joined)
	}
}

func TestBuildDockerArgs_RepoVolumeAvoidsDaemonHostBindPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Image = "registry.example/wolf/scanners@sha256:" + strings.Repeat("a", 64)
	cfg.RepoVolume = "wolf-quality-corpus-operation"
	_, args, err := BuildDockerArgs(cfg, Options{RepoDir: "/worker-only/corpus"}, "bandit")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "wolf-quality-corpus-operation:/scan:ro") {
		t.Fatalf("repository volume mount is absent: %s", joined)
	}
	if strings.Contains(joined, "/worker-only/corpus") {
		t.Fatalf("worker-local bind path leaked to remote daemon: %s", joined)
	}
}

func TestBuildDockerArgs_NoMemoryNoCPU(t *testing.T) {
	cfg := newTestCfg()
	cfg.Memory = ""
	cfg.CPUs = ""
	_, args := mustBuildDockerArgs(t, cfg, Options{RepoDir: "/repos/x"},
		"bandit", "/scan")
	joined := argList(args).String()
	if strings.Contains(joined, "--memory") {
		t.Errorf("empty Memory should not produce --memory flag; args: %s", joined)
	}
	if strings.Contains(joined, "--cpus") {
		t.Errorf("empty CPUs should not produce --cpus flag; args: %s", joined)
	}
}

func TestBuildDockerArgs_UniqueNames(t *testing.T) {
	cfg := newTestCfg()
	name1, _ := mustBuildDockerArgs(t, cfg, Options{RepoDir: "/repos/x"}, "bandit", "/scan")
	name2, _ := mustBuildDockerArgs(t, cfg, Options{RepoDir: "/repos/x"}, "bandit", "/scan")
	if name1 == name2 {
		t.Errorf("expected unique names from sequential calls, both were %q", name1)
	}
}

// TestBuildDockerArgs_EntrypointOverrideSkipsToolName locks in the fix for
// the "sh: 0: cannot open sh: No such file" bug. Before the fix, plugins
// that set EntrypointOverride="sh" produced docker invocations like
//
//	docker run --entrypoint sh <image> sh -c "..."
//
// causing sh to interpret its own name as the script. The runner now skips
// the tool-name dispatcher when an EntrypointOverride is set on a
// non-upstream image, so the args go straight to the entrypoint.
func TestBuildDockerArgs_EntrypointOverrideSkipsToolName(t *testing.T) {
	cfg := newTestCfg()
	_, args := mustBuildDockerArgs(t, cfg,
		Options{RepoDir: "/repos/myrepo", EntrypointOverride: "sh"},
		"markdownlint", "-c", "markdownlint /scan/**/*.md")

	joined := argList(args).String()

	// Entrypoint flag present.
	if !strings.Contains(joined, "--entrypoint sh") {
		t.Errorf("missing --entrypoint sh in args: %s", joined)
	}
	// Image present.
	if !strings.Contains(joined, "wolf-scanners:test") {
		t.Errorf("missing image in args: %s", joined)
	}
	// Args present.
	if !strings.Contains(joined, "-c markdownlint /scan/**/*.md") {
		t.Errorf("missing script args: %s", joined)
	}
	// The tool name "markdownlint" should NOT appear immediately after
	// the image — otherwise sh treats it as a script-name. We assert
	// the image is followed directly by "-c".
	if !strings.Contains(joined, "wolf-scanners:test -c ") {
		t.Errorf("expected image followed by -c, got: %s", joined)
	}
	if strings.Contains(joined, "wolf-scanners:test markdownlint ") {
		t.Errorf("entrypoint override should have stripped the tool-name dispatcher; got: %s", joined)
	}
}

func TestSanitizeName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"bandit", "bandit"},
		{"pip-audit", "pip-audit"},
		{"npm-audit", "npm-audit"},
		{"semgrep", "semgrep"},
		{"a/b\\c", "a_b_c"},
		{"", "tool"},
		{".dotted", "x.dotted"},
	}
	for _, c := range cases {
		got := sanitizeName(c.in)
		if got != c.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestImageReady_NoCacheEntry(t *testing.T) {
	ResetImageReadyCache()
	cfg := &Config{Image: "missing:tag"}
	if ImageReady(cfg) {
		t.Error("ImageReady should be false when cache is empty")
	}
}

func TestImageReady_PostEnsure(t *testing.T) {
	ResetImageReadyCache()
	cfg := &Config{Image: "x:y"}
	// Directly poke the cache (we can't call EnsureImage in unit tests
	// because it shells out to docker).
	globalImageState.set(cfg.Image, true)
	if !ImageReady(cfg) {
		t.Error("ImageReady should be true after cache populated")
	}
	globalImageState.set(cfg.Image, false)
	if ImageReady(cfg) {
		t.Error("ImageReady should be false after cache negated")
	}
}

func TestImageReady_Disabled(t *testing.T) {
	cfg := &Config{Disabled: true, Image: "x:y"}
	globalImageState.set(cfg.Image, true)
	defer ResetImageReadyCache()
	if ImageReady(cfg) {
		t.Error("ImageReady should be false when Disabled=true")
	}
}
