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

func TestBuildDockerArgs_Bandit(t *testing.T) {
	cfg := newTestCfg()
	_, args := BuildDockerArgs(cfg, Options{RepoDir: "/repos/myrepo"},
		"bandit", "-r", "/scan", "-f", "json", "--exit-zero")

	joined := argList(args).String()

	for _, want := range []string{
		"run", "--rm", "--name", "wolf-scan-bandit-",
		"--user 1000:1000",
		"--read-only",
		"--tmpfs /tmp:rw,size=512m,mode=1777",
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

func TestBuildDockerArgs_WorkDirOverride(t *testing.T) {
	cfg := newTestCfg()
	_, args := BuildDockerArgs(cfg,
		Options{RepoDir: "/repos/myrepo", WorkDir: "/scan/cmd/foo"},
		"gosec", "-fmt", "json", "./...")
	joined := argList(args).String()
	if !strings.Contains(joined, "--workdir /scan/cmd/foo") {
		t.Errorf("expected --workdir /scan/cmd/foo, got: %s", joined)
	}
}

func TestBuildDockerArgs_NoRepoMount(t *testing.T) {
	cfg := newTestCfg()
	_, args := BuildDockerArgs(cfg,
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
	_, args := BuildDockerArgs(cfg,
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
	_, args := BuildDockerArgs(cfg, opts, "bandit", "/scan")
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

func TestBuildDockerArgs_ExtraMounts(t *testing.T) {
	cfg := newTestCfg()
	_, args := BuildDockerArgs(cfg,
		Options{RepoDir: "/repos/x", ExtraMounts: []string{"wolf-semgrep-cache:/cache"}},
		"semgrep", "/scan")
	joined := argList(args).String()
	if !strings.Contains(joined, "-v wolf-semgrep-cache:/cache") {
		t.Errorf("ExtraMounts not propagated; args: %s", joined)
	}
}

func TestBuildDockerArgs_DBVolume(t *testing.T) {
	cfg := newTestCfg()
	cfg.DBVolume = "wolf-scanners-db"
	_, args := BuildDockerArgs(cfg, Options{RepoDir: "/repos/x"},
		"trivy", "fs", "/scan")
	joined := argList(args).String()
	if !strings.Contains(joined, "-v wolf-scanners-db:/var/lib/wolf-db") {
		t.Errorf("DBVolume not mounted; args: %s", joined)
	}
}

func TestBuildDockerArgs_NoMemoryNoCPU(t *testing.T) {
	cfg := newTestCfg()
	cfg.Memory = ""
	cfg.CPUs = ""
	_, args := BuildDockerArgs(cfg, Options{RepoDir: "/repos/x"},
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
	name1, _ := BuildDockerArgs(cfg, Options{RepoDir: "/repos/x"}, "bandit", "/scan")
	name2, _ := BuildDockerArgs(cfg, Options{RepoDir: "/repos/x"}, "bandit", "/scan")
	if name1 == name2 {
		t.Errorf("expected unique names from sequential calls, both were %q", name1)
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
