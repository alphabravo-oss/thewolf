package scannerdiscovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

type Config struct {
	MaxConcurrency     int
	PerHostConcurrency int
	PerItemTimeout     time.Duration
	MaxAttempts        int
}

func (c Config) withDefaults() Config {
	if c.MaxConcurrency == 0 {
		c.MaxConcurrency = 8
	}
	if c.PerHostConcurrency == 0 {
		c.PerHostConcurrency = min(2, c.MaxConcurrency)
	}
	if c.PerItemTimeout == 0 {
		c.PerItemTimeout = 30 * time.Second
	}
	if c.MaxAttempts == 0 {
		c.MaxAttempts = 3
	}
	return c
}

func (c Config) validate() error {
	switch {
	case c.MaxConcurrency < 1 || c.MaxConcurrency > 64:
		return fmt.Errorf("max concurrency must be from 1 through 64")
	case c.PerHostConcurrency < 1 || c.PerHostConcurrency > c.MaxConcurrency:
		return fmt.Errorf("per-host concurrency must be from 1 through max concurrency")
	case c.PerItemTimeout < time.Millisecond || c.PerItemTimeout > 10*time.Minute:
		return fmt.Errorf("per-item timeout must be from 1ms through 10m")
	case c.MaxAttempts < 1 || c.MaxAttempts > 10:
		return fmt.Errorf("max attempts must be from 1 through 10")
	default:
		return nil
	}
}

type Engine struct {
	Manifest        *manifest.Manifest
	Lock            *scannerlock.Lock
	Resolvers       []Resolver
	ResultSink      ResultSink
	HoldPolicy      HoldPolicy
	Config          Config
	RetryClassifier RetryClassifier
	Backoff         Backoff
	Sleeper         Sleeper
	Now             func() time.Time
}

// Discover runs every selected item and returns partial source failures in-band.
// Errors are reserved for invalid definitions, configuration, or selection.
func (e Engine) Discover(ctx context.Context, scope Scope) (Run, error) {
	config := e.Config.withDefaults()
	if err := config.validate(); err != nil {
		return Run{}, err
	}
	if e.Manifest == nil || e.Lock == nil {
		return Run{}, fmt.Errorf("manifest and scanner lock are required")
	}
	if err := e.Manifest.Validate(); err != nil {
		return Run{}, err
	}
	if err := e.Lock.Validate(); err != nil {
		return Run{}, err
	}
	if err := e.Lock.ValidateManifestCoverage(e.Manifest); err != nil {
		return Run{}, err
	}
	items, normalizedScope, err := buildItems(e.Manifest, e.Lock, scope)
	if err != nil {
		return Run{}, err
	}
	now := e.Now
	if now == nil {
		now = time.Now
	}
	run := Run{
		SchemaVersion: SchemaVersion, DefinitionDigest: e.Lock.Definition.Digest,
		LockDigest: e.Lock.LockDigest, Scope: normalizedScope,
		StartedAt: now().UTC(),
	}
	if len(items) == 0 {
		run.CompletedAt = now().UTC()
		run.finalize(ctx.Err())
		return run, nil
	}

	classifier := e.RetryClassifier
	if classifier == nil {
		classifier = DefaultRetryClassifier{}
	}
	backoff := e.Backoff
	if backoff == nil {
		backoff = ExponentialBackoff{Base: 250 * time.Millisecond, Maximum: 5 * time.Second}
	}
	sleeper := e.Sleeper
	if sleeper == nil {
		sleeper = timerSleeper{}
	}
	hostLimits := make(map[string]chan struct{})
	for _, item := range items {
		host := concurrencyHost(item)
		if _, ok := hostLimits[host]; !ok {
			hostLimits[host] = make(chan struct{}, config.PerHostConcurrency)
		}
	}

	jobs := make(chan Item, len(items))
	results := make(chan ItemResult, len(items))
	for _, item := range items {
		jobs <- item
	}
	close(jobs)

	workerCount := min(config.MaxConcurrency, len(items))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for item := range jobs {
				results <- e.discoverItem(ctx, item, config, hostLimits[concurrencyHost(item)], classifier, backoff, sleeper, now)
			}
		}()
	}
	go func() {
		workers.Wait()
		close(results)
	}()
	var sinkErr error
	sinkContext := context.WithoutCancel(ctx)
	for result := range results {
		if e.ResultSink != nil {
			if err := e.ResultSink.StoreDiscoveryResult(sinkContext, result); err != nil && sinkErr == nil {
				sinkErr = fmt.Errorf("store discovery result %s: %w", result.Item.ID.String(), err)
			}
		}
		run.Items = append(run.Items, result)
	}
	run.CompletedAt = now().UTC()
	run.finalize(ctx.Err())
	return run, sinkErr
}

func (e Engine) discoverItem(
	ctx context.Context,
	item Item,
	config Config,
	hostLimit chan struct{},
	classifier RetryClassifier,
	backoff Backoff,
	sleeper Sleeper,
	now func() time.Time,
) ItemResult {
	result := ItemResult{Item: RedactItem(item), Risk: RiskResult{Level: RiskNone}, CheckedAt: now().UTC()}
	if ctx.Err() != nil {
		result.Status = StatusUnreachable
		result.ErrorClass = ErrorCancelled
		result.Error = RedactText(ctx.Err().Error())
		return result
	}
	if decision := manifestManualHold(item); decision.Held {
		return heldResult(result, decision)
	}
	if e.HoldPolicy != nil {
		if decision := e.HoldPolicy.Evaluate(ctx, item); decision.Held {
			return heldResult(result, decision)
		}
	}
	resolver := findResolver(e.Resolvers, item)
	if resolver == nil {
		result.Status = StatusUnsupported
		result.ErrorClass = ErrorUnsupported
		result.Error = "no resolver registered for " + item.Source.Type
		return result
	}
	result.Resolver = resolver.Name()

	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		result.Attempts = attempt
		if err := acquire(ctx, hostLimit); err != nil {
			result.Status = StatusUnreachable
			result.ErrorClass = ErrorCancelled
			result.Error = RedactText(err.Error())
			return result
		}
		attemptCtx, cancel := context.WithTimeout(ctx, config.PerItemTimeout)
		observation, err := resolver.Resolve(attemptCtx, item)
		cancel()
		<-hostLimit
		if err == nil {
			if statusErr := validateStatus(observation.Status); statusErr != nil {
				err = &ClassifiedError{Class: ErrorInvalidResponse, Err: statusErr, Evidence: observation.Evidence}
			} else {
				result.Status = observation.Status
				result.AvailableValue = observation.AvailableValue
				result.AvailableDigest = observation.AvailableDigest
				result.Evidence = RedactEvidence(observation.Evidence)
				result.Risk = ClassifyRisk(item, observation)
				return result
			}
		}

		decision := classifier.Classify(err)
		result.ErrorClass = decision.Class
		result.Error = RedactText(err.Error())
		var classified *ClassifiedError
		if errors.As(err, &classified) {
			result.Evidence = RedactEvidence(classified.Evidence)
		}
		result.Status = errorStatus(decision.Class)
		if !decision.Retry || attempt == config.MaxAttempts {
			if decision.Retry {
				delay := backoff.Delay(attempt, decision)
				retryAt := now().UTC().Add(delay)
				result.RetryAt = &retryAt
			}
			return result
		}
		delay := backoff.Delay(attempt, decision)
		if err := sleeper.Sleep(ctx, delay); err != nil {
			result.Status = StatusUnreachable
			result.ErrorClass = ErrorCancelled
			result.Error = RedactText(err.Error())
			return result
		}
	}
	return result
}

func heldResult(result ItemResult, decision HoldDecision) ItemResult {
	result.Status = StatusHeld
	result.Error = RedactText(decision.Reason)
	result.Evidence = Evidence{Attributes: map[string]string{"hold_reason": RedactText(decision.Reason)}}
	if !decision.ReviewAfter.IsZero() {
		result.Evidence.Attributes["review_after"] = decision.ReviewAfter.UTC().Format(time.RFC3339)
	}
	result.Risk = RiskResult{Level: maxRisk(result.Item.DefinitionRisk, RiskMedium), Reasons: []string{"update is policy-held"}}
	return result
}

func manifestManualHold(item Item) HoldDecision {
	if item.ToolDefinition == nil || item.ToolDefinition.ManualUpdate == (manifest.ManualUpdate{}) {
		return HoldDecision{}
	}
	review, _ := time.Parse("2006-01-02", item.ToolDefinition.ManualUpdate.ReviewAfter)
	return HoldDecision{
		Held: true, Reason: item.ToolDefinition.ManualUpdate.Reason,
		ReviewAfter: review,
	}
}

func findResolver(resolvers []Resolver, item Item) Resolver {
	for _, resolver := range resolvers {
		if resolver != nil && resolver.Supports(item) {
			return resolver
		}
	}
	return nil
}

func acquire(ctx context.Context, semaphore chan struct{}) error {
	select {
	case semaphore <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func concurrencyHost(item Item) string {
	if item.Source.Host != "" {
		return strings.ToLower(item.Source.Host)
	}
	if item.Source.Type != "" {
		return "source:" + item.Source.Type
	}
	return "source:unknown"
}

func errorStatus(class ErrorClass) Status {
	switch class {
	case ErrorUnsupported:
		return StatusUnsupported
	case ErrorUnknown:
		return StatusUnknown
	default:
		return StatusUnreachable
	}
}

func buildItems(m *manifest.Manifest, lock *scannerlock.Lock, scope Scope) ([]Item, Scope, error) {
	all := make(map[string]Item)
	for _, name := range m.Names() {
		def := m.Tools[name]
		locked := lock.Tools[name]
		defCopy := def
		item := Item{
			ID:             ComponentID{Kind: ComponentTool, Name: name},
			CurrentValue:   locked.PinnedVersion,
			CurrentDigest:  normalizeDigest(locked.SourceIntegrity.SHA256),
			Source:         sourceForTool(def.UpdateSource),
			Platforms:      append([]string(nil), locked.Platforms...),
			DefinitionRisk: parseDefinitionRisk(locked.Risk.Classification),
			ToolDefinition: &defCopy,
			Metadata: map[string]string{
				"category": def.Category, "integration_tier": def.IntegrationTier,
			},
		}
		all[item.ID.String()] = item
	}
	for name, image := range lock.UpstreamImages {
		item := Item{
			ID:             ComponentID{Kind: ComponentUpstreamImage, Name: name},
			CurrentValue:   image.Digest,
			CurrentDigest:  image.Digest,
			Source:         sourceForImage(image.DeclaredReference),
			Platforms:      append([]string(nil), image.Platforms...),
			DefinitionRisk: parseDefinitionRisk(lock.Tools[name].Risk.Classification),
			Metadata: map[string]string{
				"resolution_status": image.ResolutionStatus,
				"mutable_source":    strconv.FormatBool(image.MutableSource),
			},
		}
		all[item.ID.String()] = item
	}
	for name, image := range lock.BaseImages {
		item := Item{
			ID:             ComponentID{Kind: ComponentBaseImage, Name: name},
			CurrentValue:   image.Digest,
			CurrentDigest:  image.Digest,
			Source:         sourceForImage(strings.Split(image.Reference, "@")[0]),
			DefinitionRisk: RiskLow,
			Metadata:       map[string]string{"locked_reference": image.Reference},
		}
		if variant, ok := lock.ReleaseInputs.Variants[name]; ok {
			item.Platforms = append([]string(nil), variant.Platforms...)
		}
		all[item.ID.String()] = item
	}
	for name, toolchain := range lock.Toolchains {
		current := toolchainCurrentValue(toolchain, lock.Tools)
		sourceURL := toolchain.Values["source"]
		item := Item{
			ID:             ComponentID{Kind: ComponentToolchain, Name: name},
			CurrentValue:   current,
			CurrentDigest:  digestStringMap(toolchain.Values),
			Source:         Source{Type: "toolchain", URL: sourceURL, Host: hostFromURL(sourceURL)},
			DefinitionRisk: RiskHigh,
			Metadata:       copyMap(toolchain.Values),
		}
		all[item.ID.String()] = item
	}

	normalized, selected, err := selectItems(all, m, lock, scope)
	if err != nil {
		return nil, Scope{}, err
	}
	out := make([]Item, 0, len(selected))
	for _, item := range selected {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return out, normalized, nil
}

func toolchainCurrentValue(toolchain scannerlock.Toolchain, tools map[string]scannerlock.Tool) string {
	if variable := toolchain.Values["version_variable"]; variable != "" {
		var matching []string
		for _, tool := range tools {
			if tool.VersionVariable == variable && tool.PinnedVersion != "" {
				matching = append(matching, tool.PinnedVersion)
			}
		}
		sort.Strings(matching)
		if len(matching) == 1 ||
			(len(matching) > 1 && matching[0] == matching[len(matching)-1]) {
			return matching[0]
		}
		// Divergent pins for a shared variable cannot safely describe the
		// toolchain's current version. Leave it empty so exact resolvers hold
		// the item for review instead of reporting false freshness.
		if len(matching) > 1 {
			return ""
		}
	}
	for _, key := range []string{"version", "major", "package"} {
		if value := toolchain.Values[key]; value != "" {
			return value
		}
	}
	return ""
}

func selectItems(
	all map[string]Item,
	m *manifest.Manifest,
	lock *scannerlock.Lock,
	scope Scope,
) (Scope, map[string]Item, error) {
	if scope.Mode == "" {
		scope.Mode = ScopeComplete
	}
	switch scope.Mode {
	case ScopeComplete:
		if len(scope.Tools) > 0 || len(scope.Components) > 0 {
			return Scope{}, nil, fmt.Errorf("complete scope must not include selections")
		}
		return Scope{Mode: ScopeComplete}, all, nil
	case ScopeSelected:
	default:
		return Scope{}, nil, fmt.Errorf("invalid discovery scope mode %q", scope.Mode)
	}
	if len(scope.Tools) == 0 && len(scope.Components) == 0 {
		return Scope{}, nil, fmt.Errorf("selected scope requires tools or components")
	}
	selected := make(map[string]Item)
	toolSet := make(map[string]struct{})
	for _, name := range scope.Tools {
		name = strings.TrimSpace(name)
		if _, ok := m.Tools[name]; !ok {
			return Scope{}, nil, fmt.Errorf("selected scanner tool %q does not exist", name)
		}
		toolSet[name] = struct{}{}
		selected[(ComponentID{Kind: ComponentTool, Name: name}).String()] = all[(ComponentID{Kind: ComponentTool, Name: name}).String()]
		if _, ok := lock.UpstreamImages[name]; ok {
			id := ComponentID{Kind: ComponentUpstreamImage, Name: name}
			selected[id.String()] = all[id.String()]
		}
	}
	componentSet := make(map[string]ComponentID)
	for _, id := range scope.Components {
		id.Name = strings.TrimSpace(id.Name)
		item, ok := all[id.String()]
		if !ok {
			return Scope{}, nil, fmt.Errorf("selected discovery component %q does not exist", id.String())
		}
		componentSet[id.String()] = id
		selected[id.String()] = item
	}
	normalized := Scope{Mode: ScopeSelected}
	for name := range toolSet {
		normalized.Tools = append(normalized.Tools, name)
	}
	sort.Strings(normalized.Tools)
	for _, id := range componentSet {
		normalized.Components = append(normalized.Components, id)
	}
	sort.Slice(normalized.Components, func(i, j int) bool {
		return normalized.Components[i].String() < normalized.Components[j].String()
	})
	return normalized, selected, nil
}

func sourceForTool(source manifest.UpdateSource) Source {
	out := Source{Type: source.Type}
	switch source.Type {
	case "pypi":
		out.URL = "https://pypi.org/pypi/" + source.Package + "/json"
	case "npm":
		out.URL = "https://registry.npmjs.org/" + source.Package
	case "github_releases":
		out.URL = "https://api.github.com/repos/" + source.Owner + "/" + source.Repo + "/releases/latest"
	case "docker_registry":
		out.Reference = source.Repository
		out.Host = imageHost(source.Repository)
	case "rubygems":
		out.URL = "https://rubygems.org/api/v1/gems/" + source.Package + ".json"
	case "go_module":
		out.URL = "https://proxy.golang.org/" + source.Module + "/@v/list"
	case "packagist":
		out.URL = "https://repo.packagist.org/p2/" + source.Package + ".json"
	case "rust_channel":
		out.URL = "https://static.rust-lang.org/dist/channel-rust-" + source.Channel + ".toml"
	case "debian_package":
		out.URL = "https://sources.debian.org/api/src/" + source.Package + "/"
	case "toolchain":
		if source.Package == "npm" {
			out.URL = "https://registry.npmjs.org/npm"
		}
	}
	if out.Host == "" {
		out.Host = hostFromURL(out.URL)
	}
	return out
}

func sourceForImage(reference string) Source {
	return Source{
		Type: "oci_registry", Reference: reference,
		Host: imageHost(reference),
	}
}

func imageHost(reference string) string {
	reference = strings.Split(reference, "@")[0]
	parts := strings.Split(reference, "/")
	if len(parts) > 1 && (strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") || parts[0] == "localhost") {
		return strings.ToLower(parts[0])
	}
	return "registry-1.docker.io"
}

func hostFromURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func parseDefinitionRisk(value string) Risk {
	switch Risk(value) {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
		return Risk(value)
	default:
		return RiskHigh
	}
}

func copyMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func digestStringMap(values map[string]string) string {
	data, _ := json.Marshal(values)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizeDigest(value string) string {
	if value == "" || strings.HasPrefix(value, "sha256:") {
		return value
	}
	return "sha256:" + value
}

type timerSleeper struct{}

func (timerSleeper) Sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type DefaultRetryClassifier struct{}

func (DefaultRetryClassifier) Classify(err error) RetryDecision {
	if err == nil {
		return RetryDecision{}
	}
	var classified *ClassifiedError
	if errors.As(err, &classified) {
		retry := classified.Class == ErrorTransientNetwork ||
			classified.Class == ErrorRateLimited ||
			classified.Class == ErrorUnavailable
		return RetryDecision{Class: classified.Class, Retry: retry, RetryAfter: classified.RetryAfter}
	}
	if errors.Is(err, context.Canceled) {
		return RetryDecision{Class: ErrorCancelled}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return RetryDecision{Class: ErrorTransientNetwork, Retry: true}
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return RetryDecision{Class: ErrorTransientNetwork, Retry: true}
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "unsupported"):
		return RetryDecision{Class: ErrorUnsupported}
	case strings.Contains(message, "429"), strings.Contains(message, "rate limit"), strings.Contains(message, "too many requests"):
		return RetryDecision{Class: ErrorRateLimited, Retry: true}
	case strings.Contains(message, "401"), strings.Contains(message, "403"), strings.Contains(message, "unauthorized"), strings.Contains(message, "forbidden"):
		return RetryDecision{Class: ErrorAuthentication}
	case strings.Contains(message, "404"), strings.Contains(message, "not found"):
		return RetryDecision{Class: ErrorNotFound}
	case strings.Contains(message, "304") && strings.Contains(message, "cache"):
		return RetryDecision{Class: ErrorInvalidResponse}
	case strings.Contains(message, "500"), strings.Contains(message, "502"), strings.Contains(message, "503"), strings.Contains(message, "504"), strings.Contains(message, "temporar"):
		return RetryDecision{Class: ErrorUnavailable, Retry: true}
	case strings.Contains(message, "deadline exceeded"), strings.Contains(message, "timed out"), strings.Contains(message, "timeout"),
		strings.Contains(message, "connection reset"), strings.Contains(message, "connection refused"), strings.Contains(message, "no such host"):
		return RetryDecision{Class: ErrorTransientNetwork, Retry: true}
	default:
		return RetryDecision{Class: ErrorUnknown}
	}
}

type ExponentialBackoff struct {
	Base    time.Duration
	Maximum time.Duration
}

func (b ExponentialBackoff) Delay(attempt int, decision RetryDecision) time.Duration {
	if decision.RetryAfter > 0 {
		return decision.RetryAfter
	}
	base := b.Base
	if base <= 0 {
		base = 250 * time.Millisecond
	}
	maximum := b.Maximum
	if maximum <= 0 {
		maximum = 30 * time.Second
	}
	delay := base
	for index := 1; index < attempt && delay < maximum; index++ {
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

// StaticHoldPolicy is convenient for API/database adapters that have already
// materialized policy decisions for a run.
type StaticHoldPolicy map[string]HoldDecision

func (p StaticHoldPolicy) Evaluate(_ context.Context, item Item) HoldDecision {
	return p[item.ID.String()]
}
