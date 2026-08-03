package scannerdiscovery

import (
	"context"
	"fmt"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scannertools/httpcache"
	"github.com/alphabravocompany/thewolf/internal/scannertools/latest"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
)

// LatestToolResolver adapts the existing manifest update checker to the durable
// discovery contract without changing the legacy API cache model.
type LatestToolResolver struct {
	Checker latest.Checker
}

func (LatestToolResolver) Name() string {
	return "manifest-latest"
}

func (LatestToolResolver) Supports(item Item) bool {
	return item.ID.Kind == ComponentTool && item.ToolDefinition != nil
}

func (r LatestToolResolver) Resolve(ctx context.Context, item Item) (Observation, error) {
	check := r.Checker.Check(ctx, item.ID.Name, *item.ToolDefinition)
	evidence := Evidence{
		SourceURL: check.SourceURL, Reference: check.LatestReference,
		Attributes: map[string]string{"source_type": check.SourceType},
	}
	switch check.Status {
	case models.ScannerVersionCurrent:
		return Observation{
			Status: StatusCurrent, AvailableValue: check.LatestVersion,
			Evidence: evidence,
		}, nil
	case models.ScannerVersionUpdateAvailable:
		return Observation{
			Status: StatusUpdate, AvailableValue: check.LatestVersion,
			Evidence: evidence,
		}, nil
	case models.ScannerVersionUnknown:
		return Observation{
			Status: StatusUnknown, AvailableValue: check.LatestVersion,
			Evidence: evidence,
		}, nil
	case models.ScannerVersionCheckFailed:
		classification := (DefaultRetryClassifier{}).Classify(fmt.Errorf("%s", check.Error))
		return Observation{}, &ClassifiedError{
			Class: classification.Class, RetryAfter: classification.RetryAfter,
			Err: fmt.Errorf("%s", check.Error), Evidence: evidence,
		}
	default:
		return Observation{}, &ClassifiedError{
			Class:    ErrorInvalidResponse,
			Err:      fmt.Errorf("legacy checker returned unsupported status %q", check.Status),
			Evidence: evidence,
		}
	}
}

// ImageDigestResolver detects upstream and base tag digest changes. A source
// tag resolving to a new upstream digest is marked as a source change; callers
// therefore receive a high-risk item rather than a quiet rebuild.
type ImageDigestResolver struct {
	Resolver *scannerlock.ImageResolver
}

func (ImageDigestResolver) Name() string {
	return "oci-registry-digest"
}

func (ImageDigestResolver) Supports(item Item) bool {
	return (item.ID.Kind == ComponentUpstreamImage || item.ID.Kind == ComponentBaseImage) &&
		item.Source.Reference != ""
}

func (r ImageDigestResolver) Resolve(ctx context.Context, item Item) (Observation, error) {
	resolver := r.Resolver
	if resolver == nil {
		resolver = &scannerlock.ImageResolver{}
	}
	resolved, err := resolver.Resolve(ctx, item.Source.Reference)
	if err != nil {
		return Observation{}, err
	}
	status := StatusCurrent
	facts := ChangeFacts{}
	currentDigest := item.CurrentDigest
	if currentDigest == "" {
		currentDigest = item.CurrentValue
	}
	if resolved.Digest != currentDigest {
		status = StatusUpdate
		facts.RebuildOnly = item.ID.Kind == ComponentBaseImage
		facts.SourceChanged = item.ID.Kind == ComponentUpstreamImage
	}
	return Observation{
		Status: status, AvailableValue: resolved.Digest, AvailableDigest: resolved.Digest, Facts: facts,
		Evidence: Evidence{
			Reference: resolvedReference(item.Source.Reference, resolved.Digest),
			Attributes: map[string]string{
				"registry_host":  item.Source.Host,
				"component_kind": string(item.ID.Kind),
			},
		},
	}, nil
}

func resolvedReference(reference, digest string) string {
	base := strings.Split(reference, "@")[0]
	slash := strings.LastIndex(base, "/")
	colon := strings.LastIndex(base, ":")
	if colon > slash {
		base = base[:colon]
	}
	return base + "@" + digest
}

// DefaultResolvers is the standard production resolver set. The toolchain
// resolver uses fixed upstream metadata endpoints by default and returns an
// explicit manual-review hold for toolchains whose lock metadata is not an
// exact, reproducible pin.
func DefaultResolvers(checker latest.Checker, images *scannerlock.ImageResolver) []Resolver {
	cache := checker.Cache
	if cache == nil && images != nil {
		cache = images.Cache
	}
	if cache == nil {
		cache = httpcache.NewMemoryStore()
	}
	if checker.Cache == nil {
		checker.Cache = cache
	}
	if images == nil {
		images = &scannerlock.ImageResolver{Cache: cache}
	} else if images.Cache == nil {
		images.Cache = cache
	}
	return []Resolver{
		LatestToolResolver{Checker: checker},
		ImageDigestResolver{Resolver: images},
		ToolchainResolver{Cache: cache},
	}
}
