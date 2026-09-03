// Package packaging is the public contract for Enterprise delivery artifacts.
// Community never serves commercial images, Helm overlays, or air-gap bundles.
package packaging

import (
	"fmt"
	"strings"
)

const ChannelAuthenticated = "authenticated"

func errf(format string, args ...any) error { return fmt.Errorf(format, args...) }

// Bundle is the certified air-gap / Enterprise delivery manifest.
// It lists required artifacts; it is not the artifacts themselves.
type Bundle struct {
	Channel                string   `json:"channel"`
	CoreModule             string   `json:"core_module"`
	CoreCommit             string   `json:"core_commit"`
	HelmOverlay            string   `json:"helm_overlay"`
	Artifacts              []string `json:"artifacts"`
	ControlPlaneDockerSock bool     `json:"control_plane_docker_sock"`
	CloudIncluded          bool     `json:"cloud_included"`
	ScannerNetworkClass    string   `json:"scanner_network_class"`
}

type Catalog interface {
	Bundle() Bundle
}

func Valid(b Bundle) error {
	if b.Channel != ChannelAuthenticated {
		return errf("channel must be %s", ChannelAuthenticated)
	}
	if b.ControlPlaneDockerSock {
		return errf("control-plane docker.sock is forbidden")
	}
	if b.CloudIncluded {
		return errf("cloud must not be included in self-hosted packaging")
	}
	if b.CoreCommit == "" || b.HelmOverlay == "" {
		return errf("core commit and helm overlay are required")
	}
	switch strings.ToLower(strings.TrimSpace(b.ScannerNetworkClass)) {
	case "offline", "none":
	default:
		return errf("scanner network must be offline or none")
	}
	return nil
}
