package container

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryBytes(t *testing.T) {
	for input, want := range map[string]int64{"2g": 2 << 30, "1.5GB": 3 << 29, "512m": 512 << 20, "1024": 1024} {
		got, err := MemoryBytes(input)
		if err != nil || got != want {
			t.Fatalf("%s: %d, %v", input, got, err)
		}
	}
	for _, input := range []string{"", "0", "-1g", "NaN", "Inf", "9e99", "wat"} {
		if _, err := MemoryBytes(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}

func TestDetectResourcesUsesConfiguredDaemon(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docker")
	script := "#!/bin/sh\n[ \"$DOCKER_HOST\" = tcp://scanner-engine:2375 ] || exit 3\n[ \"$1\" = info ] || exit 4\nprintf '%s' '{\"ID\":\"engine\",\"NCPU\":16,\"MemTotal\":32212254720}'\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	host, err := DetectHostResources(t.Context(), &Config{DockerPath: path, DockerEnvironment: []string{"DOCKER_HOST=tcp://scanner-engine:2375"}})
	if err != nil || host.ID != "engine" || host.NCPU != 16 {
		t.Fatalf("host=%+v err=%v", host, err)
	}
}

func TestScheduledMemoryMatchesDockerCap(t *testing.T) {
	cfg := newTestCfg()
	for _, tool := range []string{"bearer", "gosec", "grype", "semgrep"} {
		_, args := mustBuildDockerArgs(t, cfg, Options{RepoDir: "/repos/repo"}, tool)
		if !strings.Contains(strings.Join(args, " "), "--memory "+MemoryLimitFor(cfg, tool)) {
			t.Fatalf("memory cap drift for %s", tool)
		}
	}
}

func TestGoEnvCacheLocation(t *testing.T) {
	for _, tc := range []struct {
		cfg  *Config
		root string
	}{{nil, "/tmp/wolf-go"}, {&Config{DBVolume: "cache"}, "/var/lib/wolf-db/go"}} {
		env := GoEnv(tc.cfg)
		if env["GOCACHE"] != tc.root+"/build" || env["GOMODCACHE"] != tc.root+"/mod" {
			t.Fatalf("wrong cache: %v", env)
		}
	}
}
