package scannerdiscovery

import (
	"sort"
	"strconv"
	"strings"
)

type Risk string

const (
	RiskNone     Risk = "none"
	RiskLow      Risk = "low"
	RiskMedium   Risk = "medium"
	RiskHigh     Risk = "high"
	RiskCritical Risk = "critical"
)

type RiskResult struct {
	Level   Risk     `json:"level"`
	Reasons []string `json:"reasons,omitempty"`
}

// ClassifyRisk is deterministic and explainable. Definition policy is a floor:
// it may raise the computed level but never lower a hard safety classification.
func ClassifyRisk(item Item, observation Observation) RiskResult {
	if observation.Status == StatusHeld {
		return RiskResult{
			Level:   maxRisk(item.DefinitionRisk, RiskMedium),
			Reasons: []string{"update resolution requires manual review"},
		}
	}
	if observation.Status != StatusUpdate && observation.Status != StatusYanked {
		return RiskResult{Level: RiskNone}
	}
	level := RiskLow
	var reasons []string

	if observation.Status == StatusYanked {
		level = RiskHigh
		reasons = append(reasons, "selected source release is yanked or removed")
	}
	if observation.Facts.ActivelyExploited {
		level = RiskCritical
		reasons = append(reasons, "update addresses an actively exploited vulnerability")
	}
	if observation.Facts.ArtifactRevoked {
		level = RiskCritical
		reasons = append(reasons, "artifact or signing identity is revoked")
	}
	if observation.Facts.PlatformLost {
		level = maxRisk(level, RiskHigh)
		reasons = append(reasons, "supported platform would be lost")
	}
	if observation.Facts.SignatureChanged || observation.Facts.SourceChanged {
		level = maxRisk(level, RiskHigh)
		reasons = append(reasons, "source or signing identity changed")
	}
	if observation.Facts.PrivilegeIncreased {
		level = maxRisk(level, RiskHigh)
		reasons = append(reasons, "scanner requires additional privilege")
	}
	if observation.Facts.ParserChanged || observation.Facts.RulesChanged || observation.Facts.LicenseChanged {
		level = maxRisk(level, RiskMedium)
		if observation.Facts.ParserChanged {
			reasons = append(reasons, "parser or output contract changed")
		}
		if observation.Facts.RulesChanged {
			reasons = append(reasons, "scanner ruleset changed")
		}
		if observation.Facts.LicenseChanged {
			reasons = append(reasons, "license evidence changed")
		}
	}

	if observation.Status == StatusUpdate && !observation.Facts.RebuildOnly {
		switch versionChange(item.CurrentValue, observation.AvailableValue) {
		case "major":
			level = maxRisk(level, RiskHigh)
			reasons = append(reasons, "major version update")
		case "minor":
			level = maxRisk(level, RiskMedium)
			reasons = append(reasons, "minor version update")
		case "patch":
			reasons = append(reasons, "patch version update")
		case "digest":
			reasons = append(reasons, "digest-only rebuild")
		default:
			level = maxRisk(level, RiskHigh)
			reasons = append(reasons, "version change is not semantically comparable")
		}
	} else if observation.Facts.RebuildOnly {
		reasons = append(reasons, "rebuild-only change")
	}

	if item.DefinitionRisk != "" && item.DefinitionRisk != RiskNone && riskRank(item.DefinitionRisk) > riskRank(level) {
		level = item.DefinitionRisk
		reasons = append(reasons, "definition policy raises risk to "+string(item.DefinitionRisk))
	}
	reasons = sortedUniqueStrings(reasons)
	return RiskResult{Level: level, Reasons: reasons}
}

func versionChange(current, available string) string {
	if current == available && current != "" {
		return "digest"
	}
	currentParts, currentOK := numericVersion(current)
	availableParts, availableOK := numericVersion(available)
	if !currentOK || !availableOK {
		if strings.HasPrefix(current, "sha256:") && strings.HasPrefix(available, "sha256:") {
			return "digest"
		}
		return "unknown"
	}
	if part(currentParts, 0) != part(availableParts, 0) {
		return "major"
	}
	if part(currentParts, 1) != part(availableParts, 1) {
		return "minor"
	}
	if part(currentParts, 2) != part(availableParts, 2) || current != available {
		return "patch"
	}
	return "digest"
}

func numericVersion(value string) ([]int, bool) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "refs/tags/")
	value = strings.TrimPrefix(value, "v")
	var parts []int
	for _, raw := range strings.Split(value, ".") {
		var digits strings.Builder
		for _, char := range raw {
			if char < '0' || char > '9' {
				break
			}
			digits.WriteRune(char)
		}
		if digits.Len() == 0 {
			break
		}
		number, err := strconv.Atoi(digits.String())
		if err != nil {
			return nil, false
		}
		parts = append(parts, number)
	}
	return parts, len(parts) > 0
}

func part(parts []int, index int) int {
	if index >= len(parts) {
		return 0
	}
	return parts[index]
}

func maxRisk(left, right Risk) Risk {
	if riskRank(right) > riskRank(left) {
		return right
	}
	return left
}

func riskRank(value Risk) int {
	switch value {
	case RiskLow:
		return 1
	case RiskMedium:
		return 2
	case RiskHigh:
		return 3
	case RiskCritical:
		return 4
	default:
		return 0
	}
}

func sortedUniqueStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
