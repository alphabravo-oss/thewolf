package ospackages

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ulikunitz/xz"
)

const (
	maxReleaseBytes      = int64(8 << 20)
	maxIndexBytes        = int64(64 << 20)
	maxDecompressedBytes = int64(512 << 20)
	refreshTimeout       = 2 * time.Minute
	refreshAttempts      = 4
)

type RefreshOptions struct {
	Snapshot string
	Client   *http.Client
}

type packageRecord struct {
	Name         string
	Version      string
	Architecture string
	Filename     string
	SHA256       string
}

func Refresh(
	ctx context.Context,
	policy *Policy,
	policyData []byte,
	options RefreshOptions,
) (*Lock, error) {
	if policy == nil {
		return nil, errors.New("OS package policy is required")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if !snapshotPattern.MatchString(options.Snapshot) {
		return nil, errors.New("--snapshot must use YYYYMMDDTHHMMSSZ")
	}
	snapshotTime, err := time.Parse("20060102T150405Z", options.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("parse snapshot timestamp: %w", err)
	}
	if snapshotTime.After(time.Now().UTC().Add(24 * time.Hour)) {
		return nil, errors.New("snapshot timestamp is in the future")
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: refreshTimeout}
	}

	architectures := policyArchitectures(*policy)
	repositories := make(map[string]RepositoryLock, len(policy.Repositories))
	records := make(map[string]map[string]map[string]packageRecord, len(policy.Repositories))
	for _, repositoryName := range sortedRepositoryNames(policy.Repositories) {
		repositoryPolicy := policy.Repositories[repositoryName]
		repositoryLock, byArchitecture, err := refreshRepository(
			ctx,
			client,
			repositoryName,
			repositoryPolicy,
			options.Snapshot,
			architectures,
		)
		if err != nil {
			return nil, fmt.Errorf("refresh repository %s: %w", repositoryName, err)
		}
		repositories[repositoryName] = repositoryLock
		records[repositoryName] = byArchitecture
	}

	lock := &Lock{
		SchemaVersion: LockSchemaVersion,
		PolicyDigest:  PolicyDigest(policyData),
		Snapshot:      options.Snapshot,
		Repositories:  repositories,
		Variants:      make(map[string]VariantLock, len(policy.Variants)),
	}
	for _, variantName := range sortedVariantNames(policy.Variants) {
		variantPolicy := policy.Variants[variantName]
		variantLock := VariantLock{
			Dockerfile: variantPolicy.Dockerfile,
			Platforms:  make(map[string]PlatformLock, len(variantPolicy.Platforms)),
		}
		for _, platform := range variantPolicy.Platforms {
			architecture, _ := platformArchitecture(platform)
			var packages []PackageLock
			for _, declaredSource := range sortedPackageSources(variantPolicy.Packages) {
				sourcePolicy := policy.Repositories[declaredSource]
				for _, packageName := range variantPolicy.Packages[declaredSource] {
					record, source, found := selectPackage(
						packageName,
						architecture,
						declaredSource,
						sourcePolicy.Type,
						policy.Repositories,
						records,
					)
					if !found {
						return nil, fmt.Errorf(
							"variant %s platform %s package %s was not present in the refreshed indexes",
							variantName,
							platform,
							packageName,
						)
					}
					locked := PackageLock{
						Name: record.Name, Version: record.Version,
						Architecture: record.Architecture, Source: source,
						Filename: record.Filename, SHA256: "sha256:" + record.SHA256,
					}
					if policy.Repositories[source].Type == RepositoryAPTArtifact {
						locked.URL = strings.TrimSuffix(policy.Repositories[source].URI, "/") +
							"/" + strings.TrimPrefix(record.Filename, "/")
					}
					packages = append(packages, locked)
				}
			}
			sort.Slice(packages, func(i, j int) bool { return packages[i].Name < packages[j].Name })
			variantLock.Platforms[platform] = PlatformLock{Packages: packages}
		}
		lock.Variants[variantName] = variantLock
	}
	if err := lock.SetDigest(); err != nil {
		return nil, err
	}
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	if err := lock.ValidatePolicy(*policy, policyData); err != nil {
		return nil, err
	}
	return lock, nil
}

// FetchBootstrapCACertificates downloads the exact architecture-independent
// ca-certificates package already selected in the lock. The image extracts its
// trust bundle before apt contacts the HTTPS-only immutable snapshots.
func FetchBootstrapCACertificates(
	ctx context.Context,
	lock *Lock,
	client *http.Client,
) ([]byte, error) {
	if lock == nil {
		return nil, errors.New("OS package lock is required")
	}
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	pkg, err := bootstrapCAPackage(*lock)
	if err != nil {
		return nil, err
	}
	repository := lock.Repositories[pkg.Source]
	rawURL := strings.TrimSuffix(repository.URI, "/") + "/" + lock.Snapshot +
		"/" + strings.TrimPrefix(pkg.Filename, "/")
	if client == nil {
		client = &http.Client{Timeout: refreshTimeout}
	}
	data, err := fetchBounded(ctx, client, rawURL, maxReleaseBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch ca-certificates bootstrap: %w", err)
	}
	if actual := sha256Value(data); actual != pkg.SHA256 {
		return nil, fmt.Errorf("ca-certificates bootstrap digest=%s, want %s", actual, pkg.SHA256)
	}
	return data, nil
}

func refreshRepository(
	ctx context.Context,
	client *http.Client,
	name string,
	policy RepositoryPolicy,
	snapshot string,
	architectures []string,
) (RepositoryLock, map[string]map[string]packageRecord, error) {
	lock := RepositoryLock{
		Type: policy.Type, URI: policy.URI, Suite: policy.Suite,
		Component: policy.Component, Indexes: make(map[string]IndexLock, len(architectures)),
	}
	records := make(map[string]map[string]packageRecord, len(architectures))
	var releaseHashes map[string]string
	if policy.Type == RepositoryDebianSnapshot {
		releaseURL := strings.TrimSuffix(policy.URI, "/") + "/" + snapshot +
			"/dists/" + url.PathEscape(policy.Suite) + "/Release"
		release, err := fetchBounded(ctx, client, releaseURL, maxReleaseBytes)
		if err != nil {
			return RepositoryLock{}, nil, err
		}
		lock.ReleaseSHA256 = sha256Value(release)
		releaseHashes, err = parseReleaseSHA256(release)
		if err != nil {
			return RepositoryLock{}, nil, err
		}
	}
	for _, architecture := range architectures {
		indexPath := policy.Component + "/binary-" + architecture + "/Packages.gz"
		if policy.Type == RepositoryDebianSnapshot {
			var ok bool
			indexPath, ok = selectReleaseIndex(releaseHashes, policy.Component, architecture)
			if !ok {
				return RepositoryLock{}, nil, fmt.Errorf(
					"Release metadata has no supported package index for %s",
					architecture,
				)
			}
		}
		indexURL := repositoryIndexURL(policy, snapshot, indexPath)
		compressed, err := fetchBounded(ctx, client, indexURL, maxIndexBytes)
		if err != nil {
			return RepositoryLock{}, nil, fmt.Errorf("fetch %s index: %w", architecture, err)
		}
		indexDigest := sha256Value(compressed)
		if policy.Type == RepositoryDebianSnapshot {
			expected := releaseHashes[indexPath]
			if expected == "" || indexDigest != "sha256:"+expected {
				return RepositoryLock{}, nil, fmt.Errorf(
					"%s index digest %s does not match Release metadata %s",
					architecture,
					indexDigest,
					expected,
				)
			}
		}
		uncompressed, err := decompressIndexBounded(compressed, indexPath, maxDecompressedBytes)
		if err != nil {
			return RepositoryLock{}, nil, fmt.Errorf("decompress %s index: %w", architecture, err)
		}
		parsed, err := parsePackages(uncompressed, architecture)
		if err != nil {
			return RepositoryLock{}, nil, fmt.Errorf("parse %s index: %w", architecture, err)
		}
		lock.Indexes[architecture] = IndexLock{URL: indexURL, SHA256: indexDigest}
		records[architecture] = parsed
	}
	_ = name
	return lock, records, nil
}

func repositoryIndexURL(policy RepositoryPolicy, snapshot, indexPath string) string {
	base := strings.TrimSuffix(policy.URI, "/")
	if policy.Type == RepositoryDebianSnapshot {
		base += "/" + snapshot
	}
	return base + "/dists/" + url.PathEscape(policy.Suite) + "/" + indexPath
}

func fetchBounded(ctx context.Context, client *http.Client, rawURL string, limit int64) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("refuse invalid refresh URL %q", rawURL)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	bounded := *client
	previousRedirect := bounded.CheckRedirect
	bounded.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("OS package metadata redirect limit exceeded")
		}
		if next.URL.Scheme != "https" || !strings.EqualFold(next.URL.Host, parsed.Host) ||
			next.URL.User != nil {
			return errors.New("OS package metadata redirect left the configured HTTPS origin")
		}
		if previousRedirect != nil {
			return previousRedirect(next, via)
		}
		return nil
	}
	var lastErr error
	for attempt := 1; attempt <= refreshAttempts; attempt++ {
		response, doErr := bounded.Do(request.Clone(ctx))
		if doErr == nil {
			data, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
			closeErr := response.Body.Close()
			switch {
			case readErr != nil:
				lastErr = readErr
			case closeErr != nil:
				lastErr = closeErr
			case int64(len(data)) > limit:
				return nil, fmt.Errorf("GET %s exceeded %d bytes", parsed.Redacted(), limit)
			case response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices:
				return data, nil
			case response.StatusCode != http.StatusTooManyRequests && response.StatusCode < 500:
				return nil, fmt.Errorf("GET %s: %s", parsed.Redacted(), response.Status)
			default:
				lastErr = fmt.Errorf("GET %s: %s", parsed.Redacted(), response.Status)
			}
		} else {
			lastErr = doErr
		}
		if attempt < refreshAttempts {
			timer := time.NewTimer(time.Duration(attempt) * 250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, lastErr
}

func decompressIndexBounded(compressed []byte, indexPath string, limit int64) ([]byte, error) {
	var reader io.Reader
	switch {
	case strings.HasSuffix(indexPath, ".gz"):
		gzipReader, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			return nil, err
		}
		defer func() { _ = gzipReader.Close() }()
		reader = gzipReader
	case strings.HasSuffix(indexPath, ".xz"):
		xzReader, err := xz.NewReader(bytes.NewReader(compressed))
		if err != nil {
			return nil, err
		}
		reader = xzReader
	case strings.HasSuffix(indexPath, "/Packages"):
		reader = bytes.NewReader(compressed)
	default:
		return nil, fmt.Errorf("unsupported package index encoding: %s", indexPath)
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("decompressed package index exceeded %d bytes", limit)
	}
	return data, nil
}

func selectReleaseIndex(hashes map[string]string, component, architecture string) (string, bool) {
	base := component + "/binary-" + architecture + "/Packages"
	for _, suffix := range []string{".xz", ".gz", ""} {
		candidate := base + suffix
		if hashes[candidate] != "" {
			return candidate, true
		}
	}
	return "", false
}

func parseReleaseSHA256(data []byte) (map[string]string, error) {
	hashes := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	inSection := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "SHA256:" {
			inSection = true
			continue
		}
		if inSection && len(line) > 0 && line[0] != ' ' {
			break
		}
		if !inSection {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		if len(fields[0]) == 64 {
			hashes[fields[2]] = fields[0]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(hashes) == 0 {
		return nil, errors.New("Release metadata has no SHA256 section")
	}
	return hashes, nil
}

func parsePackages(data []byte, architecture string) (map[string]packageRecord, error) {
	records := map[string]packageRecord{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	fields := map[string]string{}
	flush := func() error {
		if len(fields) == 0 {
			return nil
		}
		record := packageRecord{
			Name: fields["Package"], Version: fields["Version"],
			Architecture: fields["Architecture"], Filename: fields["Filename"],
			SHA256: fields["SHA256"],
		}
		fields = map[string]string{}
		if record.Name == "" || record.Version == "" || record.Filename == "" ||
			len(record.SHA256) != 64 ||
			(record.Architecture != architecture && record.Architecture != "all") {
			return nil
		}
		if current, exists := records[record.Name]; !exists ||
			compareDebianVersions(record.Version, current.Version) > 0 {
			records[record.Name] = record
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if ok {
			fields[key] = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, errors.New("package index contained no usable records")
	}
	return records, nil
}

func selectPackage(
	name, architecture, declaredSource, sourceType string,
	policies map[string]RepositoryPolicy,
	records map[string]map[string]map[string]packageRecord,
) (packageRecord, string, bool) {
	if sourceType == RepositoryAPTArtifact {
		record, ok := records[declaredSource][architecture][name]
		return record, declaredSource, ok
	}
	var selected packageRecord
	selectedSource := ""
	for _, repositoryName := range sortedRepositoryNames(policies) {
		if policies[repositoryName].Type != RepositoryDebianSnapshot {
			continue
		}
		candidate, ok := records[repositoryName][architecture][name]
		if !ok {
			continue
		}
		if selectedSource == "" ||
			compareDebianVersions(candidate.Version, selected.Version) > 0 ||
			(candidate.Version == selected.Version && repositoryName < selectedSource) {
			selected = candidate
			selectedSource = repositoryName
		}
	}
	return selected, selectedSource, selectedSource != ""
}

func compareDebianVersions(left, right string) int {
	leftEpoch, leftUpstream, leftRevision := splitDebianVersion(left)
	rightEpoch, rightUpstream, rightRevision := splitDebianVersion(right)
	if leftEpoch != rightEpoch {
		if leftEpoch < rightEpoch {
			return -1
		}
		return 1
	}
	if compared := compareDebianPart(leftUpstream, rightUpstream); compared != 0 {
		return compared
	}
	return compareDebianPart(leftRevision, rightRevision)
}

func splitDebianVersion(version string) (epoch int64, upstream, revision string) {
	rest := version
	if rawEpoch, after, ok := strings.Cut(version, ":"); ok {
		if parsed, err := strconv.ParseInt(rawEpoch, 10, 64); err == nil {
			epoch = parsed
			rest = after
		}
	}
	upstream, revision = rest, "0"
	if index := strings.LastIndex(rest, "-"); index >= 0 {
		upstream, revision = rest[:index], rest[index+1:]
	}
	return epoch, upstream, revision
}

func compareDebianPart(left, right string) int {
	for left != "" || right != "" {
		for (left != "" && !isDigit(left[0])) || (right != "" && !isDigit(right[0])) {
			var leftOrder, rightOrder int
			if left != "" && !isDigit(left[0]) {
				leftOrder = debianCharOrder(left[0])
				left = left[1:]
			}
			if right != "" && !isDigit(right[0]) {
				rightOrder = debianCharOrder(right[0])
				right = right[1:]
			}
			if leftOrder != rightOrder {
				if leftOrder < rightOrder {
					return -1
				}
				return 1
			}
		}
		leftDigits, leftRest := takeDigits(left)
		rightDigits, rightRest := takeDigits(right)
		left, right = leftRest, rightRest
		leftDigits = strings.TrimLeft(leftDigits, "0")
		rightDigits = strings.TrimLeft(rightDigits, "0")
		if len(leftDigits) != len(rightDigits) {
			if len(leftDigits) < len(rightDigits) {
				return -1
			}
			return 1
		}
		if leftDigits != rightDigits {
			if leftDigits < rightDigits {
				return -1
			}
			return 1
		}
	}
	return 0
}

func debianCharOrder(character byte) int {
	switch {
	case character == '~':
		return -1
	case character == 0:
		return 0
	case (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z'):
		return int(character)
	default:
		return int(character) + 256
	}
}

func isDigit(character byte) bool {
	return character >= '0' && character <= '9'
}

func takeDigits(value string) (string, string) {
	index := 0
	for index < len(value) && isDigit(value[index]) {
		index++
	}
	return value[:index], value[index:]
}

func policyArchitectures(policy Policy) []string {
	set := map[string]struct{}{}
	for _, variant := range policy.Variants {
		for _, platform := range variant.Platforms {
			architecture, _ := platformArchitecture(platform)
			set[architecture] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for architecture := range set {
		out = append(out, architecture)
	}
	sort.Strings(out)
	return out
}

func sortedRepositoryNames(repositories map[string]RepositoryPolicy) []string {
	out := make([]string, 0, len(repositories))
	for name := range repositories {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func sortedVariantNames(variants map[string]VariantPolicy) []string {
	out := make([]string, 0, len(variants))
	for name := range variants {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func sortedPackageSources(packages map[string][]string) []string {
	out := make([]string, 0, len(packages))
	for source := range packages {
		out = append(out, source)
	}
	sort.Strings(out)
	return out
}
