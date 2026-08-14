package install

import (
	"fmt"
	"runtime"
)

// Pinned to fixer/versions.env. Refresh together when bumping CLIs.
const (
	claudeVersion = "2.1.220"
	codexVersion  = "0.146.0"
	opencodeVer   = "1.18.16"

	claudeLinuxAMD64SRI = "sha512-3CGFCnI0gpgsqNeJruFALBDGJaKXOuok3alQEg56ty2yOPpIrOx/r2Y0+T4uhJl7kP5Hzw4IFkxo4DZKWvzQ7Q=="
	claudeLinuxARM64SRI = "sha512-VHFI8mKruIntKn7eq81sbyS19/KWmQcmJQsS/C+j9M/E+w0s4UytgsL7DADPjBE/GByNiKoRtLYDMntCjRlOdA=="
	codexLinuxAMD64SRI  = "sha512-fswvyGprAPCMiOEue/7MKMk7pCjh9kZIJfJX5i9atmfnmGYbYCcUhZsEH9LEP0+0t5xyPqDbfNXY7NSxIVuXxA=="
	codexLinuxARM64SRI  = "sha512-qiYDxkkEFnXG7joadJW6Q+XcgyDXCpGdpa9nk/c+i0gEomur1j7bHvx12NfWWCF/y8Tqri6ay+FLuC2MjdehtA=="

	// GitHub release digests for anomalyco/opencode v1.18.16 musl builds
	// (alpine fixer image).
	opencodeLinuxAMD64MuslSHA = "a3a72753c6f9dc97626c81b406ac6af7ab4acab17bf275775205a8927e814bcd"
	opencodeLinuxARM64MuslSHA = "34b3a487f1535866f7b1eebbfc1e4c36ad1fdbcb2ab1fb17e675f657df302f45"
	opencodeLinuxAMD64SHA     = "286e07355df06738c1905955be15b7fbc10a7b12d931de9394a6f7597246750b"
	opencodeLinuxARM64SHA     = "4fdce5f9bc877d977304d71c0c90ad6e83efa381fe0edf0a61e6142a625e1c41"
)

type cliSpec struct {
	Command   string
	Version   string
	URL       string
	SHA256    string
	SHA512SRI string
}

func specFor(name string) (cliSpec, error) {
	cmd := commandName(name)
	arch := goarch()
	if runtime.GOOS != "linux" || (arch != "x64" && arch != "arm64") {
		return cliSpec{}, fmt.Errorf("cannot install %s on %s/%s", name, runtime.GOOS, runtime.GOARCH)
	}
	switch cmd {
	case "claude":
		pkg := "claude-code-linux-" + arch
		sri := claudeLinuxAMD64SRI
		if arch == "arm64" {
			sri = claudeLinuxARM64SRI
		}
		return cliSpec{
			Command:   "claude",
			Version:   claudeVersion,
			URL:       fmt.Sprintf("https://registry.npmjs.org/@anthropic-ai/%s/-/%s-%s.tgz", pkg, pkg, claudeVersion),
			SHA512SRI: sri,
		}, nil
	case "codex":
		ver := codexVersion + "-linux-" + arch
		sri := codexLinuxAMD64SRI
		if arch == "arm64" {
			sri = codexLinuxARM64SRI
		}
		return cliSpec{
			Command:   "codex",
			Version:   codexVersion,
			URL:       fmt.Sprintf("https://registry.npmjs.org/@openai/codex/-/codex-%s.tgz", ver),
			SHA512SRI: sri,
		}, nil
	case "opencode":
		suffix := "linux-" + arch
		sum := opencodeLinuxAMD64SHA
		if linuxMusl() {
			suffix += "-musl"
			sum = opencodeLinuxAMD64MuslSHA
			if arch == "arm64" {
				sum = opencodeLinuxARM64MuslSHA
			}
		} else if arch == "arm64" {
			sum = opencodeLinuxARM64SHA
		}
		return cliSpec{
			Command: "opencode",
			Version: opencodeVer,
			URL:     fmt.Sprintf("https://github.com/anomalyco/opencode/releases/download/v%s/opencode-%s.tar.gz", opencodeVer, suffix),
			SHA256:  sum,
		}, nil
	default:
		return cliSpec{}, fmt.Errorf("unknown CLI %q (install claude, codex, or opencode)", name)
	}
}
