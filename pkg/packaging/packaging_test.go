package packaging

import "testing"

func TestValidRejectsPublicChannelAndDockerSock(t *testing.T) {
	b := Bundle{Channel: "public", CoreCommit: "abc", HelmOverlay: "x", ScannerNetworkClass: "offline"}
	if err := Valid(b); err == nil {
		t.Fatal("public channel")
	}
	b.Channel = ChannelAuthenticated
	b.ControlPlaneDockerSock = true
	if err := Valid(b); err == nil {
		t.Fatal("docker.sock")
	}
	b.ControlPlaneDockerSock = false
	if err := Valid(b); err != nil {
		t.Fatal(err)
	}
	b.CloudIncluded = true
	if err := Valid(b); err == nil {
		t.Fatal("cloud included")
	}
	b.CloudIncluded = false
	b.ScannerNetworkClass = "bridge"
	if err := Valid(b); err == nil {
		t.Fatal("open scanner network")
	}
}
