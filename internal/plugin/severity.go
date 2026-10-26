package plugin

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	cvss20 "github.com/pandatix/go-cvss/20"
	cvss30 "github.com/pandatix/go-cvss/30"
	cvss31 "github.com/pandatix/go-cvss/31"
	cvss40 "github.com/pandatix/go-cvss/40"
)

// CVSS preserves the upstream score/vector and its declared version/source.
type CVSS struct {
	Type   string `json:"type"`
	Score  string `json:"score"`
	Source string `json:"source,omitempty"`
}

// ResolveSeverity uses the highest valid CVSS rating, then a tool rating, then
// the documented compatibility fallback. The explanation travels with the
// finding description through storage and exports, including when no score exists.
func ResolveSeverity(toolRating string, vectors []CVSS, fallback models.Severity) (models.Severity, string) {
	best, source := -1.0, ""
	for _, vector := range vectors {
		if score, ok := cvssScore(vector); ok && score > best {
			best = score
			source = fmt.Sprintf("%s %s", vector.Type, vector.Score)
			if vector.Source != "" {
				source += " (" + vector.Source + ")"
			}
		}
	}
	if best >= 0 {
		severity := models.SeverityInfo
		switch {
		case best >= 9:
			severity = models.SeverityCritical
		case best >= 7:
			severity = models.SeverityHigh
		case best >= 4:
			severity = models.SeverityMedium
		case best > 0:
			severity = models.SeverityLow
		}
		return severity, fmt.Sprintf("Severity: %s from %s (score %.1f).", severity, source, best)
	}
	rating := strings.ToLower(strings.TrimSpace(toolRating))
	if rating == "moderate" {
		rating = "medium"
	}
	if rating == "none" {
		rating = "info"
	}
	switch models.Severity(rating) {
	case models.SeverityCritical, models.SeverityHigh, models.SeverityMedium, models.SeverityLow, models.SeverityInfo:
		return models.Severity(rating), fmt.Sprintf("Severity: %s from tool rating %q.", rating, toolRating)
	}
	return fallback, fmt.Sprintf("Severity unavailable: no valid tool rating or CVSS score; Wolf uses its documented %s fallback. This is not an upstream severity assessment.", fallback)
}

func cvssScore(value CVSS) (float64, bool) {
	if value.Type != "CVSS_V2" && value.Type != "CVSS_V3" && value.Type != "CVSS_V4" {
		return 0, false
	}
	vector := strings.TrimSpace(value.Score)
	if len(vector) > 4096 {
		return 0, false
	}
	if n, err := strconv.ParseFloat(vector, 64); err == nil {
		return n, !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= 10
	}
	switch {
	case value.Type == "CVSS_V2":
		v, err := cvss20.ParseVector(vector)
		if err == nil {
			return v.BaseScore(), true
		}
	case value.Type == "CVSS_V3" && strings.HasPrefix(vector, "CVSS:3.0/"):
		v, err := cvss30.ParseVector(vector)
		if err == nil {
			return v.BaseScore(), true
		}
	case value.Type == "CVSS_V3" && strings.HasPrefix(vector, "CVSS:3.1/"):
		v, err := cvss31.ParseVector(vector)
		if err == nil {
			return v.BaseScore(), true
		}
	case value.Type == "CVSS_V4" && strings.HasPrefix(vector, "CVSS:4.0/"):
		v, err := cvss40.ParseVector(vector)
		if err == nil {
			return v.Score(), true
		}
	}
	return 0, false
}
