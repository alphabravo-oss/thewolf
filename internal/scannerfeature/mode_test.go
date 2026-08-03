package scannerfeature

import "testing"

func TestParseDefaultsToFailClosedReadOnly(t *testing.T) {
	t.Parallel()
	mode, err := Parse("")
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeReadOnly || !mode.Allows(CapabilityRead) ||
		mode.Allows(CapabilityCandidate) || mode.Allows(CapabilityStable) {
		t.Fatalf("default mode = %q", mode)
	}
}

func TestModesOnlyAllowTheirEnablementStage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value                           string
		mode                            Mode
		read, candidate, canary, stable bool
	}{
		{"disabled", ModeDisabled, false, false, false, false},
		{"observe-only", ModeReadOnly, true, false, false, false},
		{"candidate", ModeCandidate, true, true, false, false},
		{"canary", ModeCanary, true, true, true, false},
		{"stable-control", ModeStableControl, true, true, true, true},
	}
	for _, test := range cases {
		test := test
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			mode, err := Parse(test.value)
			if err != nil {
				t.Fatal(err)
			}
			got := mode.Capabilities()
			if mode != test.mode || got.Read != test.read ||
				got.Candidates != test.candidate || got.Canary != test.canary ||
				got.StableControl != test.stable {
				t.Fatalf("capabilities = %#v", got)
			}
		})
	}
}

func TestParseRejectsUnknownMode(t *testing.T) {
	t.Parallel()
	if _, err := Parse("production-ish"); err == nil {
		t.Fatal("unknown scanner release mode accepted")
	}
}
