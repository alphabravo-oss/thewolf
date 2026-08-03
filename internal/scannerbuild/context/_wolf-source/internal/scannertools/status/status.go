package status

import (
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

type ToolStatus struct {
	Name              string `json:"name"`
	DisplayName       string `json:"display_name"`
	Category          string `json:"category"`
	IntegrationTier   string `json:"integration_tier"`
	Bucket            string `json:"bucket,omitempty"`
	PinnedVersion     string `json:"pinned_version,omitempty"`
	VersionVariable   string `json:"version_variable,omitempty"`
	UpdateSourceType  string `json:"update_source_type,omitempty"`
	CanonicalImage    string `json:"canonical_image,omitempty"`
	ConfiguredImage   string `json:"configured_image,omitempty"`
	ImagePresent      *bool  `json:"image_present,omitempty"`
	Entrypoint        string `json:"entrypoint,omitempty"`
	Overridden        bool   `json:"overridden"`
	UsesLatestTag     bool   `json:"uses_latest_tag"`
	LatestVersion     string `json:"latest_version,omitempty"`
	LatestReference   string `json:"latest_reference,omitempty"`
	FreshnessStatus   string `json:"freshness_status,omitempty"`
	VersionCheckError string `json:"version_check_error,omitempty"`
	VersionCheckedAt  string `json:"version_checked_at,omitempty"`
}

func Build(m *manifest.Manifest, cfg *container.Config) []ToolStatus {
	return BuildWithChecks(m, cfg, nil)
}

func BuildWithChecks(m *manifest.Manifest, cfg *container.Config, checks map[string]models.ScannerVersionCheck) []ToolStatus {
	return BuildWithChecksAndImages(m, cfg, checks, nil)
}

func BuildWithChecksAndImages(m *manifest.Manifest, cfg *container.Config, checks map[string]models.ScannerVersionCheck, imagePresent map[string]bool) []ToolStatus {
	if m == nil {
		return nil
	}
	out := make([]ToolStatus, 0, len(m.Tools))
	for _, name := range m.Names() {
		tool := m.Tools[name]
		row := ToolStatus{
			Name:             name,
			DisplayName:      tool.DisplayName,
			Category:         tool.Category,
			IntegrationTier:  tool.IntegrationTier,
			Bucket:           tool.Bucket,
			PinnedVersion:    tool.PinnedVersion,
			VersionVariable:  tool.VersionVariable,
			UpdateSourceType: tool.UpdateSource.Type,
			CanonicalImage:   tool.Image.PinnedReference,
			Entrypoint:       tool.Image.Entrypoint,
		}
		if cfg != nil {
			row.ConfiguredImage = cfg.ImageFor(name)
			if spec, ok := cfg.UpstreamSpec(name); ok {
				row.Entrypoint = spec.Entrypoint
			}
			row.Overridden = isOverridden(tool, cfg, name, row.ConfiguredImage)
			row.UsesLatestTag = UsesLatestTag(row.ConfiguredImage)
			if imagePresent != nil && row.ConfiguredImage != "" {
				present := imagePresent[row.ConfiguredImage]
				row.ImagePresent = &present
			}
		}
		if check, ok := checks[name]; ok {
			row.LatestVersion = check.LatestVersion
			row.LatestReference = check.LatestReference
			row.FreshnessStatus = string(check.Status)
			row.VersionCheckError = check.Error
			if !check.CheckedAt.IsZero() {
				row.VersionCheckedAt = check.CheckedAt.UTC().Format(time.RFC3339)
			}
		}
		out = append(out, row)
	}
	return out
}

func Find(rows []ToolStatus, name string) (ToolStatus, bool) {
	i := sort.Search(len(rows), func(i int) bool { return rows[i].Name >= name })
	if i < len(rows) && rows[i].Name == name {
		return rows[i], true
	}
	return ToolStatus{}, false
}

func UsesLatestTag(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	if strings.Contains(ref, "@sha256:") {
		return false
	}
	i := strings.LastIndex(ref, ":")
	if i < 0 || i < strings.LastIndex(ref, "/") {
		return false
	}
	return ref[i+1:] == "latest"
}

func isOverridden(tool manifest.Tool, cfg *container.Config, name, configured string) bool {
	if cfg == nil || configured == "" {
		return false
	}
	switch tool.IntegrationTier {
	case manifest.TierUpstream:
		if tool.Image.PinnedReference == "" {
			return false
		}
		return configured != tool.Image.PinnedReference
	case manifest.TierBucket:
		_, dedicated := cfg.ImageOverrides[name]
		return !dedicated
	case manifest.TierDefault:
		_, dedicated := cfg.ImageOverrides[name]
		if dedicated {
			return true
		}
		_, upstream := cfg.UpstreamTools[name]
		return upstream
	default:
		return false
	}
}
