package verification

import (
	"context"
	"testing"
)

func TestDenyEngineProductionDefault(t *testing.T) {
	res, err := (DenyEngine{}).Verify(context.Background(), Request{VulnerabilityID: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusDenied || res.Reason != "production-deny default" || res.Exploitable != nil {
		t.Fatalf("%+v", res)
	}
}

func TestDenyEngineKillSwitch(t *testing.T) {
	t.Setenv("WOLF_VERIFY_KILL_SWITCH", "1")
	res, err := (DenyEngine{}).Verify(context.Background(), Request{
		VulnerabilityID: "v1", Environment: EnvSandbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusDenied || res.Reason != "verification kill switch is on" {
		t.Fatalf("%+v", res)
	}
}

func TestDenyEngineSandboxDoesNotRun(t *testing.T) {
	t.Setenv("WOLF_VERIFY_KILL_SWITCH", "")
	res, err := (DenyEngine{}).Verify(context.Background(), Request{
		VulnerabilityID: "v1", Environment: EnvSandbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusDenied || res.Exploitable != nil {
		t.Fatalf("%+v", res)
	}
}
