package scannerdiscovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannertools/httpcache"
	"github.com/alphabravocompany/thewolf/internal/scannertools/latest"
)

const (
	defaultToolchainResponseLimit = int64(8 << 20)
	maximumToolchainResponseLimit = int64(16 << 20)
	defaultToolchainTimeout       = 10 * time.Second
	maximumToolchainTimeout       = time.Minute
)

var (
	sha256ValuePattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	semverValuePattern   = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	sha1ValuePattern     = regexp.MustCompile(`^[a-f0-9]{40}$`)
	leadingSemverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+`)
)

// ToolchainResolver resolves release metadata only from its configured
// endpoints. Endpoints is an administrative allowlist, not a set of URLs
// derived from scanner-lock metadata. A nil map enables the production
// defaults; a non-nil map is authoritative and can intentionally omit an
// online source for an air-gapped deployment.
//
// Toolchains without an exact current pin are returned as explicit manual
// review holds. This avoids claiming that a moving distribution suite or major
// release channel is current when the installed package version is unknown.
type ToolchainResolver struct {
	Client           *http.Client
	Endpoints        map[string]string
	MaxResponseBytes int64
	RequestTimeout   time.Duration
	Cache            httpcache.Store
	CacheMaxAge      time.Duration
	Now              func() time.Time
}

func (ToolchainResolver) Name() string {
	return "toolchain-release-metadata"
}

func (ToolchainResolver) Supports(item Item) bool {
	return item.ID.Kind == ComponentToolchain
}

func (r ToolchainResolver) Resolve(ctx context.Context, item Item) (Observation, error) {
	if item.ID.Kind != ComponentToolchain {
		return Observation{}, &ClassifiedError{
			Class: ErrorUnsupported,
			Err:   errors.New("toolchain resolver received a non-toolchain component"),
		}
	}
	switch item.ID.Name {
	case "go":
		return r.resolveGo(ctx, item)
	case "rust":
		return r.resolveRust(ctx, item)
	case "nodejs":
		return manualToolchainReview(
			item,
			"mutable-major-channel",
			"NodeSource setup selects a moving major channel and the scanner lock does not record the exact Node.js package version or artifact digest",
		), nil
	case "jdk", "php", "python", "ruby":
		return manualToolchainReview(
			item,
			"unpinned-debian-suite-package",
			"the Debian suite package is installed without an exact package version or snapshot, so source freshness cannot be compared reproducibly",
		), nil
	default:
		return manualToolchainReview(
			item,
			"unrecognized-toolchain",
			"no exact, reproducible release metadata strategy is registered for this toolchain",
		), nil
	}
}

func (r ToolchainResolver) resolveGo(ctx context.Context, item Item) (Observation, error) {
	endpoint, configured, err := r.endpoint("go")
	if err != nil {
		return Observation{}, err
	}
	if !configured {
		return manualToolchainReview(
			item,
			"exact-go-release-endpoint-unconfigured",
			"the exact Go release metadata endpoint is not configured for this deployment",
		), nil
	}
	body, evidence, err := r.fetch(ctx, endpoint)
	if err != nil {
		return Observation{}, err
	}
	var releases []struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
		Files   []struct {
			Filename string `json:"filename"`
			OS       string `json:"os"`
			Arch     string `json:"arch"`
			Kind     string `json:"kind"`
			SHA256   string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(body, &releases); err != nil {
		return Observation{}, invalidToolchainResponse(evidence, "decode Go release metadata", err)
	}

	current := normalizeGoVersion(item.CurrentValue)
	var latestRelease *struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
		Files   []struct {
			Filename string `json:"filename"`
			OS       string `json:"os"`
			Arch     string `json:"arch"`
			Kind     string `json:"kind"`
			SHA256   string `json:"sha256"`
		} `json:"files"`
	}
	currentFound := false
	for index := range releases {
		release := &releases[index]
		version := normalizeGoVersion(release.Version)
		if release.Stable && version != "" && version == current {
			currentFound = true
		}
		if !release.Stable || version == "" {
			continue
		}
		if latestRelease == nil ||
			latest.CompareVersions(version, normalizeGoVersion(latestRelease.Version)) > 0 {
			latestRelease = release
		}
	}
	if latestRelease == nil {
		return Observation{}, invalidToolchainResponse(
			evidence, "validate Go release metadata", errors.New("no stable release was present"),
		)
	}
	version := normalizeGoVersion(latestRelease.Version)
	checksums, err := goReleaseChecksums(latestRelease.Files)
	if err != nil {
		return Observation{}, invalidToolchainResponse(evidence, "validate Go release checksums", err)
	}
	releaseMetadata := copyMap(checksums)
	releaseMetadata["version"] = version
	metadataDigest := digestStringMap(releaseMetadata)
	evidence.Reference = "go:" + version
	evidence.Attributes = map[string]string{
		"strategy":             "exact-go-release",
		"pin":                  version,
		"linux_amd64_sha256":   checksums["linux/amd64"],
		"linux_arm64_sha256":   checksums["linux/arm64"],
		"release_metadata_pin": metadataDigest,
	}
	observation := Observation{
		AvailableValue:  version,
		AvailableDigest: metadataDigest,
		Evidence:        evidence,
	}
	if current == "" || !currentFound {
		observation.Status = StatusHeld
		observation.Evidence.Detail = "the current exact Go pin is absent from the complete upstream release index; manual review is required"
		observation.Evidence.Attributes["review"] = "manual"
		return observation, nil
	}
	if latest.CompareVersions(version, current) > 0 {
		observation.Status = StatusUpdate
		return observation, nil
	}
	observation.Status = StatusCurrent
	return observation, nil
}

func (r ToolchainResolver) resolveRust(ctx context.Context, item Item) (Observation, error) {
	endpoint, configured, err := r.endpoint("rust")
	if err != nil {
		return Observation{}, err
	}
	if !configured {
		return manualToolchainReview(
			item,
			"exact-rust-channel-endpoint-unconfigured",
			"the exact Rust stable-channel metadata endpoint is not configured for this deployment",
		), nil
	}
	body, evidence, err := r.fetch(ctx, endpoint)
	if err != nil {
		return Observation{}, err
	}
	section, err := tomlSection(body, "pkg.rust")
	if err != nil {
		return Observation{}, invalidToolchainResponse(evidence, "locate Rust release metadata", err)
	}
	versionValue, err := tomlString(section, "version")
	if err != nil {
		return Observation{}, invalidToolchainResponse(evidence, "read Rust release version", err)
	}
	version := leadingSemver(versionValue)
	if !semverValuePattern.MatchString(version) {
		return Observation{}, invalidToolchainResponse(
			evidence, "validate Rust release version", fmt.Errorf("release version %q is not exact", versionValue),
		)
	}
	commit, err := tomlString(section, "git_commit_hash")
	if err != nil || !sha1ValuePattern.MatchString(commit) {
		if err == nil {
			err = errors.New("git_commit_hash is not a full SHA-1 commit")
		}
		return Observation{}, invalidToolchainResponse(evidence, "validate Rust release commit", err)
	}
	releaseDate, err := tomlString(body, "date")
	if err != nil {
		return Observation{}, invalidToolchainResponse(evidence, "read Rust release date", err)
	}
	if _, err := time.Parse(time.DateOnly, releaseDate); err != nil {
		return Observation{}, invalidToolchainResponse(evidence, "validate Rust release date", err)
	}
	checksums := map[string]string{
		"release_commit": commit,
		"release_date":   releaseDate,
		"version":        version,
	}
	for platform, target := range map[string]string{
		"linux/amd64": "x86_64-unknown-linux-gnu",
		"linux/arm64": "aarch64-unknown-linux-gnu",
	} {
		targetSection, sectionErr := tomlSection(body, "pkg.rust.target."+target)
		if sectionErr != nil {
			return Observation{}, invalidToolchainResponse(evidence, "locate Rust target metadata", sectionErr)
		}
		hash, hashErr := tomlString(targetSection, "hash")
		if hashErr != nil || !sha256ValuePattern.MatchString(hash) {
			if hashErr == nil {
				hashErr = fmt.Errorf("%s target hash is not SHA-256", platform)
			}
			return Observation{}, invalidToolchainResponse(evidence, "validate Rust target checksum", hashErr)
		}
		checksums[platform] = hash
	}
	metadataDigest := digestStringMap(checksums)
	evidence.Reference = "rust:stable@" + version
	evidence.Attributes = map[string]string{
		"strategy":             "exact-rust-channel-release",
		"pin":                  version,
		"release_commit":       commit,
		"release_date":         releaseDate,
		"linux_amd64_sha256":   checksums["linux/amd64"],
		"linux_arm64_sha256":   checksums["linux/arm64"],
		"release_metadata_pin": metadataDigest,
	}
	observation := Observation{
		AvailableValue: version, AvailableDigest: metadataDigest, Evidence: evidence,
	}
	current := exactSemver(item.CurrentValue)
	switch comparison := latest.CompareVersions(version, current); {
	case current == "":
		observation.Status = StatusHeld
		observation.Evidence.Detail = "the scanner lock does not contain an exact Rust version pin; manual review is required"
		observation.Evidence.Attributes["review"] = "manual"
	case comparison > 0:
		observation.Status = StatusUpdate
	case comparison == 0:
		observation.Status = StatusCurrent
	default:
		observation.Status = StatusHeld
		observation.Evidence.Detail = "the locked Rust version is newer than the stable-channel release; manual review is required"
		observation.Evidence.Attributes["review"] = "manual"
	}
	return observation, nil
}

func manualToolchainReview(item Item, strategy, reason string) Observation {
	evidence := Evidence{
		Reference: item.Metadata["source"],
		Detail:    reason,
		Attributes: map[string]string{
			"strategy": strategy,
			"review":   "manual",
			"reason":   reason,
		},
	}
	if strings.HasPrefix(item.Source.URL, "https://") || strings.HasPrefix(item.Source.URL, "http://") {
		evidence.SourceURL = item.Source.URL
	}
	return Observation{Status: StatusHeld, Evidence: evidence}
}

func (r ToolchainResolver) endpoint(name string) (string, bool, error) {
	endpoints := r.Endpoints
	if endpoints == nil {
		endpoints = map[string]string{
			"go":   "https://go.dev/dl/?mode=json&include=all",
			"rust": "https://static.rust-lang.org/dist/channel-rust-stable.toml",
		}
	}
	raw, ok := endpoints[name]
	if !ok || strings.TrimSpace(raw) == "" {
		return "", false, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", false, &ClassifiedError{
			Class: ErrorInvalidResponse,
			Err:   fmt.Errorf("configured %s toolchain endpoint is not an absolute HTTP URL", name),
		}
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return "", false, &ClassifiedError{
			Class: ErrorInvalidResponse,
			Err:   fmt.Errorf("configured %s toolchain endpoint contains credentials or a fragment", name),
		}
	}
	for key := range parsed.Query() {
		if sensitiveKey(key) || strings.HasPrefix(strings.ToLower(key), "x-amz-") {
			return "", false, &ClassifiedError{
				Class: ErrorInvalidResponse,
				Err:   fmt.Errorf("configured %s toolchain endpoint contains a credential query parameter", name),
			}
		}
	}
	return parsed.String(), true, nil
}

func (r ToolchainResolver) fetch(ctx context.Context, endpoint string) ([]byte, Evidence, error) {
	parsed, _ := url.Parse(endpoint)
	timeout := r.RequestTimeout
	if timeout <= 0 {
		timeout = defaultToolchainTimeout
	}
	if timeout > maximumToolchainTimeout {
		timeout = maximumToolchainTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, Evidence{}, &ClassifiedError{Class: ErrorInvalidResponse, Err: errors.New("construct toolchain metadata request")}
	}
	request.Header.Set("Accept", "application/json, application/toml, text/plain")

	client := r.Client
	if client == nil {
		client = &http.Client{}
	}
	boundedClient := *client
	previousRedirect := boundedClient.CheckRedirect
	boundedClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 4 {
			return errors.New("toolchain metadata redirect limit exceeded")
		}
		if next.URL.Scheme != parsed.Scheme || !strings.EqualFold(next.URL.Host, parsed.Host) ||
			next.URL.User != nil {
			return errors.New("toolchain metadata redirect left the configured origin")
		}
		for key := range next.URL.Query() {
			if sensitiveKey(key) || strings.HasPrefix(strings.ToLower(key), "x-amz-") {
				return errors.New("toolchain metadata redirect added a credential query parameter")
			}
		}
		if previousRedirect != nil {
			return previousRedirect(next, via)
		}
		return nil
	}
	limit := r.MaxResponseBytes
	if limit <= 0 {
		limit = defaultToolchainResponseLimit
	}
	if limit > maximumToolchainResponseLimit {
		limit = maximumToolchainResponseLimit
	}
	response, err := httpcache.Do(requestCtx, &boundedClient, request, httpcache.Options{
		Store: r.Cache, MaxBodyBytes: limit, MaxAge: r.CacheMaxAge, Now: r.Now,
	})
	if err != nil {
		if errors.Is(err, httpcache.ErrNotModifiedWithoutUsableCache) {
			return nil, Evidence{}, &ClassifiedError{
				Class: ErrorInvalidResponse,
				Err:   fmt.Errorf("request toolchain metadata: %w", err),
			}
		}
		if strings.Contains(err.Error(), "response exceeds") {
			evidence := Evidence{SourceURL: endpoint}
			return nil, evidence, &ClassifiedError{
				Class:    ErrorInvalidResponse,
				Err:      fmt.Errorf("toolchain metadata response exceeds maximum size %d", limit),
				Evidence: evidence,
			}
		}
		decision := (DefaultRetryClassifier{}).Classify(err)
		if decision.Class == ErrorUnknown {
			decision.Class = ErrorUnavailable
			decision.Retry = true
		}
		return nil, Evidence{}, &ClassifiedError{
			Class: decision.Class, RetryAfter: decision.RetryAfter,
			Err: fmt.Errorf("request toolchain metadata: %w", err),
		}
	}
	evidence := Evidence{
		SourceURL:    endpoint,
		ETag:         response.Header.Get("ETag"),
		LastModified: response.Header.Get("Last-Modified"),
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, evidence, toolchainHTTPError(&http.Response{
			StatusCode: response.StatusCode,
			Status:     response.Status,
			Header:     response.Header,
		}, evidence)
	}
	body := response.Body
	sum := sha256.Sum256(body)
	evidence.ResponseDigest = "sha256:" + hex.EncodeToString(sum[:])
	return body, evidence, nil
}

func toolchainHTTPError(response *http.Response, evidence Evidence) error {
	class := ErrorInvalidResponse
	retryAfter := time.Duration(0)
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		class = ErrorAuthentication
	case http.StatusNotFound:
		class = ErrorNotFound
	case http.StatusTooManyRequests:
		class = ErrorRateLimited
		retryAfter = parseRetryAfter(response.Header.Get("Retry-After"))
	default:
		if response.StatusCode >= http.StatusInternalServerError {
			class = ErrorUnavailable
		}
	}
	return &ClassifiedError{
		Class: class, RetryAfter: retryAfter,
		Err:      fmt.Errorf("toolchain metadata source returned %s", response.Status),
		Evidence: evidence,
	}
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if delay := time.Until(at); delay > 0 {
			return delay
		}
	}
	return 0
}

func invalidToolchainResponse(evidence Evidence, action string, err error) error {
	return &ClassifiedError{
		Class:    ErrorInvalidResponse,
		Err:      fmt.Errorf("%s: %w", action, err),
		Evidence: evidence,
	}
}

func goReleaseChecksums(files []struct {
	Filename string `json:"filename"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Kind     string `json:"kind"`
	SHA256   string `json:"sha256"`
}) (map[string]string, error) {
	out := make(map[string]string, 2)
	for _, file := range files {
		if file.OS != "linux" || file.Kind != "archive" {
			continue
		}
		platform := ""
		switch file.Arch {
		case "amd64":
			platform = "linux/amd64"
		case "arm64":
			platform = "linux/arm64"
		}
		if platform == "" {
			continue
		}
		if !sha256ValuePattern.MatchString(file.SHA256) {
			return nil, fmt.Errorf("%s archive checksum is not SHA-256", platform)
		}
		out[platform] = file.SHA256
	}
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		if out[platform] == "" {
			return nil, fmt.Errorf("stable release lacks a checksummed %s archive", platform)
		}
	}
	return out, nil
}

func normalizeGoVersion(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "go")
	return exactSemver(value)
}

func leadingSemver(value string) string {
	value = strings.TrimSpace(value)
	return leadingSemverPattern.FindString(value)
}

func exactSemver(value string) string {
	value = strings.TrimSpace(value)
	if !semverValuePattern.MatchString(value) {
		return ""
	}
	return value
}

func tomlSection(document []byte, name string) ([]byte, error) {
	pattern := regexp.MustCompile(`(?ms)^\[` + regexp.QuoteMeta(name) + `\]\s*(.*?)(?:^\[|\z)`)
	match := pattern.FindSubmatch(document)
	if len(match) != 2 {
		return nil, fmt.Errorf("TOML document has no [%s] section", name)
	}
	return match[1], nil
}

func tomlString(document []byte, key string) (string, error) {
	pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `\s*=\s*"([^"]+)"`)
	match := pattern.FindSubmatch(document)
	if len(match) != 2 {
		return "", fmt.Errorf("TOML document has no %s string", key)
	}
	return string(match[1]), nil
}
