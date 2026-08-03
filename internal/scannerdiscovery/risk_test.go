package scannerdiscovery

import (
	"strings"
	"testing"
)

func TestRiskClassificationTable(t *testing.T) {
	tests := []struct {
		name        string
		item        Item
		observation Observation
		level       Risk
		reason      string
	}{
		{
			name:        "patch",
			item:        Item{CurrentValue: "1.2.3", DefinitionRisk: RiskLow},
			observation: Observation{Status: StatusUpdate, AvailableValue: "1.2.4"},
			level:       RiskLow, reason: "patch version",
		},
		{
			name:        "minor",
			item:        Item{CurrentValue: "1.2.3", DefinitionRisk: RiskLow},
			observation: Observation{Status: StatusUpdate, AvailableValue: "1.3.0"},
			level:       RiskMedium, reason: "minor version",
		},
		{
			name:        "major",
			item:        Item{CurrentValue: "1.2.3", DefinitionRisk: RiskLow},
			observation: Observation{Status: StatusUpdate, AvailableValue: "2.0.0"},
			level:       RiskHigh, reason: "major version",
		},
		{
			name: "base rebuild",
			item: Item{
				ID:             ComponentID{Kind: ComponentBaseImage, Name: "default"},
				CurrentValue:   "sha256:" + strings.Repeat("a", 64),
				DefinitionRisk: RiskLow,
			},
			observation: Observation{
				Status: StatusUpdate, AvailableValue: "sha256:" + strings.Repeat("b", 64),
				Facts: ChangeFacts{RebuildOnly: true},
			},
			level: RiskLow, reason: "rebuild-only",
		},
		{
			name: "parser",
			item: Item{CurrentValue: "1.2.3", DefinitionRisk: RiskLow},
			observation: Observation{
				Status: StatusUpdate, AvailableValue: "1.2.4",
				Facts: ChangeFacts{ParserChanged: true},
			},
			level: RiskMedium, reason: "parser",
		},
		{
			name: "platform loss",
			item: Item{CurrentValue: "1.2.3", DefinitionRisk: RiskLow},
			observation: Observation{
				Status: StatusUpdate, AvailableValue: "1.2.4",
				Facts: ChangeFacts{PlatformLost: true},
			},
			level: RiskHigh, reason: "platform",
		},
		{
			name: "revoked",
			item: Item{CurrentValue: "1.2.3", DefinitionRisk: RiskLow},
			observation: Observation{
				Status: StatusYanked, AvailableValue: "1.2.3",
				Facts: ChangeFacts{ArtifactRevoked: true},
			},
			level: RiskCritical, reason: "revoked",
		},
		{
			name:        "policy floor",
			item:        Item{CurrentValue: "1.2.3", DefinitionRisk: RiskHigh},
			observation: Observation{Status: StatusUpdate, AvailableValue: "1.2.4"},
			level:       RiskHigh, reason: "definition policy",
		},
		{
			name:        "not a change",
			item:        Item{CurrentValue: "1.2.3", DefinitionRisk: RiskCritical},
			observation: Observation{Status: StatusCurrent, AvailableValue: "1.2.3"},
			level:       RiskNone,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ClassifyRisk(test.item, test.observation)
			if got.Level != test.level {
				t.Fatalf("level = %q, want %q (%v)", got.Level, test.level, got.Reasons)
			}
			if test.reason != "" && !containsReason(got.Reasons, test.reason) {
				t.Fatalf("reasons %v do not contain %q", got.Reasons, test.reason)
			}
			for index := 1; index < len(got.Reasons); index++ {
				if got.Reasons[index-1] >= got.Reasons[index] {
					t.Fatalf("reasons are not sorted/unique: %v", got.Reasons)
				}
			}
		})
	}
}

func TestNonSemanticVersionIsConservativelyHighRisk(t *testing.T) {
	got := ClassifyRisk(
		Item{CurrentValue: "stable-2026-01", DefinitionRisk: RiskLow},
		Observation{Status: StatusUpdate, AvailableValue: "stable-2026-02"},
	)
	if got.Level != RiskHigh || !containsReason(got.Reasons, "not semantically") {
		t.Fatalf("risk = %+v", got)
	}
}

func containsReason(reasons []string, fragment string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, fragment) {
			return true
		}
	}
	return false
}
