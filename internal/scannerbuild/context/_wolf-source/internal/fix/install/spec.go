package install

import (
	"fmt"
	"runtime"
)

// Pinned to fixer/versions.env. Refresh together when bumping CLIs.
const (
	claudeVersion = "2.1.296"
	codexVersion  = "0.162.1"
	opencodeVer   = "1.19.0"

	claudeLinuxAMD64SRI = "sha512-+m01urgelnI+VJxPaKPEK0wixgq42uZ8PeCaUk5tX/8YoDBvSmC/REVQG2jQfJAr8dxunpjJ600FvLrcj1WWTw=="
	claudeLinuxARM64SRI = "sha512-Q6Qh6qFmwuBG9rQFiDR6bY3XkFnwTN9AAPrnnDgBvr9sbg1Ql5wrf0lfoCDiV369EUHOzxI4i5OdHW9Vf2/oeA=="
	codexLinuxAMD64SRI  = "sha512-cjK2mdHIfEepZ1lQ1MX3aTQC+8ncIsvnacl/6vI2dIIcfP8/MrhTQ2+qRlkXFNDzJSj1d36quIszP+3/4CUJWQ=="
	codexLinuxARM64SRI  = "sha512-Ss7W/SrKtQFNaMtSojNoiykJgX+iDC8daM+YrcvgmJADtQPk0cd3w6Dna4CYH2CYvf9nQranj69fpcTzZ3sQIQ=="

	// GitHub release digests for anomalyco/opencode v1.19.0 musl builds
	// (alpine fixer image).
	opencodeLinuxAMD64MuslSHA = "043193a55bcb67c524426132870ab7f960da664ad20a5327e8e03730ef14a869"
	opencodeLinuxARM64MuslSHA = "c864e28b1504fe4412d9495493e72b8ebf3e205dd1ee14d125b99c14b26623da"
	opencodeLinuxAMD64SHA     = "7bb9e61ad6feacf2e89c10a6a2c1840e3de4220bb50ba1fc7110f31806076aec"
	opencodeLinuxARM64SHA     = "e9841d43d3e1a8f36ed2691592d3b8b4fb097e606c8b559a277a1b845840e26b"
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
