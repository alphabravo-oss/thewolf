// Command wolf is the Wolf CLI and server. The command tree lives in
// internal/wolfcli so the Enterprise overlay can embed it (pkg/host.Execute).
package main

import "github.com/alphabravocompany/thewolf/internal/wolfcli"

// Version metadata. Overridden at link time via:
//
//	-ldflags "-X main.version=X.Y.Z -X main.commit=abc -X main.buildDate=..."
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	wolfcli.Execute(wolfcli.Options{Version: version, Commit: commit, BuildDate: buildDate})
}
