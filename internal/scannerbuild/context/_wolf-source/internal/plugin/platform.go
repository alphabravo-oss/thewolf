package plugin

import "runtime"

// IsArm64Host reports whether wolf is running on an arm64 host. Some upstream
// scanner images (bearer) are published amd64-only; under Docker
// Desktop / Rancher Desktop they run via qemu emulation and crash with Go
// runtime panics ("lfstack.push invalid packing") or fail to find the
// entrypoint. Plugins whose images are arm64-incompatible can use this to
// skip cleanly with an explanatory message.
//
// We check runtime.GOARCH rather than uname to avoid exec'ing into the
// container; wolf itself runs as the host architecture.
func IsArm64Host() bool {
	return runtime.GOARCH == "arm64"
}
