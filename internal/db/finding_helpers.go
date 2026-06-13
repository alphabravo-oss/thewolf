package db

import (
	"encoding/json"

	"github.com/alphabravocompany/thewolf/internal/finding/identity"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func prepareFindingForWrite(f *models.Finding) {
	if f.StableFingerprint == "" || f.LocationFingerprint == "" || f.SemanticFingerprint == "" || f.IdentityVersion == 0 {
		identity.Apply(f)
	}
	if f.Fingerprint == "" {
		f.Fingerprint = f.StableFingerprint
	}
	if f.CorroboratedByJSON == "" {
		tools := f.CorroboratedBy
		if len(tools) == 0 && f.ToolName != "" {
			tools = []string{f.ToolName}
		}
		if data, err := json.Marshal(tools); err == nil {
			f.CorroboratedByJSON = string(data)
		} else {
			f.CorroboratedByJSON = "[]"
		}
	}
}

func hydrateFindingAfterRead(f *models.Finding) {
	if f.StableFingerprint == "" {
		f.StableFingerprint = f.Fingerprint
	}
	if f.CorroboratedByJSON != "" {
		_ = json.Unmarshal([]byte(f.CorroboratedByJSON), &f.CorroboratedBy)
	}
}

func hydrateFindingsAfterRead(findings []models.Finding) {
	for i := range findings {
		hydrateFindingAfterRead(&findings[i])
	}
}
