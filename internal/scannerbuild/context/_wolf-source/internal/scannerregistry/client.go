package scannerregistry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerbundle"
)

const defaultMaxManifestBytes = int64(16 << 20)
const defaultMaxInMemoryBlobBytes = int64(512 << 20)

type Endpoint struct {
	// BaseURL is configured by an administrator. Production endpoints should
	// be HTTPS; HTTP is accepted only when explicitly present here, enabling
	// isolated development registries without deriving a URL from user input.
	BaseURL string
}

type CredentialProvider interface {
	Authorization(ctx context.Context, registry string) (string, error)
}

type CredentialProviderFunc func(context.Context, string) (string, error)

func (f CredentialProviderFunc) Authorization(ctx context.Context, registry string) (string, error) {
	return f(ctx, registry)
}

type Client struct {
	HTTP        *http.Client
	Endpoints   map[string]Endpoint
	Credentials CredentialProvider
	// TokenHosts is an explicit per-registry allowlist for Bearer challenge
	// realms. When omitted, only the registry host itself is allowed. This
	// prevents a compromised registry response from forwarding credentials to
	// an arbitrary token service.
	TokenHosts       map[string][]string
	MaxManifestBytes int64
	MaxBlobBytes     int64
	Concurrency      int
}

type Manifest struct {
	Reference   Reference
	MediaType   string
	Digest      string
	Content     []byte
	Descriptors []Descriptor
}

type Descriptor struct {
	MediaType    string            `json:"mediaType"`
	Digest       string            `json:"digest"`
	Size         int64             `json:"size"`
	Platform     Platform          `json:"platform"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

type Platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Variant      string `json:"variant,omitempty"`
}

func (p Platform) String() string {
	value := p.OS + "/" + p.Architecture
	if p.Variant != "" {
		value += "/" + p.Variant
	}
	return value
}

type Verification struct {
	ImageKey  string   `json:"image_key"`
	Ref       string   `json:"reference"`
	Verified  bool     `json:"verified"`
	Error     string   `json:"error,omitempty"`
	Platforms []string `json:"platforms,omitempty"`
}

func (c Client) Check(ctx context.Context, registry string) error {
	endpoint, err := c.endpoint(registry)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/v2/", nil)
	if err != nil {
		return err
	}
	response, err := c.doAuthorized(ctx, request, registry)
	if err != nil {
		return fmt.Errorf("check OCI registry %q: %w", registry, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("check OCI registry %q: %s", registry, response.Status)
	}
	return nil
}

func (c Client) FetchManifest(ctx context.Context, reference Reference) (*Manifest, error) {
	endpoint, err := c.endpoint(reference.Registry)
	if err != nil {
		return nil, err
	}
	if !validSHA256Digest(reference.Digest) {
		return nil, errors.New("manifest reference digest is invalid")
	}
	manifestURL := endpoint + "/v2/" + escapeRepository(reference.Repository) + "/manifests/" + url.PathEscape(reference.Digest)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))
	response, err := c.doAuthorized(ctx, request, reference.Registry)
	if err != nil {
		return nil, fmt.Errorf("fetch OCI manifest %s: %w", reference.String(), err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return nil, fmt.Errorf("fetch OCI manifest %s: %s", reference.String(), response.Status)
	}
	maximum := c.MaxManifestBytes
	if maximum <= 0 {
		maximum = defaultMaxManifestBytes
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("read OCI manifest %s: %w", reference.String(), err)
	}
	if int64(len(content)) > maximum {
		return nil, fmt.Errorf("OCI manifest %s exceeds maximum size %d", reference.String(), maximum)
	}
	digest := digestBytes(content)
	if digest != reference.Digest {
		return nil, fmt.Errorf("OCI manifest content digest %s does not match requested %s", digest, reference.Digest)
	}
	if advertised := strings.TrimSpace(response.Header.Get("Docker-Content-Digest")); advertised != "" &&
		advertised != reference.Digest {
		return nil, fmt.Errorf("OCI registry advertised digest %s for requested %s", advertised, reference.Digest)
	}
	mediaType := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	var parsed struct {
		SchemaVersion int          `json:"schemaVersion"`
		MediaType     string       `json:"mediaType"`
		Manifests     []Descriptor `json:"manifests"`
	}
	if err := json.Unmarshal(content, &parsed); err != nil {
		// Image manifests contain many fields that the index shape does not.
		// They are still content-addressed and valid; descriptor parsing is
		// required only when the document declares a multi-platform index.
		parsed = struct {
			SchemaVersion int          `json:"schemaVersion"`
			MediaType     string       `json:"mediaType"`
			Manifests     []Descriptor `json:"manifests"`
		}{}
		var header struct {
			MediaType string `json:"mediaType"`
		}
		if headerErr := json.Unmarshal(content, &header); headerErr != nil {
			return nil, fmt.Errorf("decode OCI manifest %s: %w", reference.String(), headerErr)
		}
		parsed.MediaType = header.MediaType
	}
	if parsed.MediaType != "" {
		mediaType = parsed.MediaType
	}
	return &Manifest{
		Reference: reference, MediaType: mediaType, Digest: digest,
		Content: content, Descriptors: parsed.Manifests,
	}, nil
}

// FetchReferrers returns the immutable OCI descriptors that attach signatures,
// provenance, and SBOM evidence to a subject digest. Callers compare descriptor
// digests with release inventory; media-type labels are advisory and never
// substitute for an exact digest match.
func (c Client) FetchReferrers(ctx context.Context, reference Reference) ([]Descriptor, error) {
	endpoint, err := c.endpoint(reference.Registry)
	if err != nil {
		return nil, err
	}
	if !validSHA256Digest(reference.Digest) {
		return nil, errors.New("referrers subject digest is invalid")
	}
	referrersURL := endpoint + "/v2/" + escapeRepository(reference.Repository) +
		"/referrers/" + url.PathEscape(reference.Digest)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, referrersURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.oci.image.index.v1+json")
	response, err := c.doAuthorized(ctx, request, reference.Registry)
	if err != nil {
		return nil, fmt.Errorf("fetch OCI referrers %s: %w", reference.String(), err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return nil, fmt.Errorf("fetch OCI referrers %s: %s", reference.String(), response.Status)
	}
	maximum := c.MaxManifestBytes
	if maximum <= 0 {
		maximum = defaultMaxManifestBytes
	}
	var index struct {
		Manifests []Descriptor `json:"manifests"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maximum+1))
	if err := decoder.Decode(&index); err != nil {
		return nil, fmt.Errorf("decode OCI referrers %s: %w", reference.String(), err)
	}
	for _, descriptor := range index.Manifests {
		if !validSHA256Digest(descriptor.Digest) {
			return nil, errors.New("OCI registry returned an invalid referrer digest")
		}
	}
	return index.Manifests, nil
}

// ReadEvidence performs exact digest readback across the subject's OCI
// referrer closure. Evidence digests may identify either a referrer manifest
// or a config/layer payload (for example raw signature bytes). Empty expected
// digests are ignored. A returned false value is drift, not success.
func (c Client) ReadEvidence(
	ctx context.Context,
	reference Reference,
	expected map[string]string,
) (map[string]bool, error) {
	referrers, err := c.FetchReferrers(ctx, reference)
	if err != nil {
		return nil, err
	}
	available := make(map[string]struct{}, len(referrers))
	visited := make(map[string]struct{}, len(referrers))
	for _, descriptor := range referrers {
		if err := c.collectManifestDescriptorDigests(
			ctx, Reference{
				Registry: reference.Registry, Repository: reference.Repository,
				Digest: descriptor.Digest,
			}, available, visited,
		); err != nil {
			return nil, err
		}
	}
	result := make(map[string]bool, len(expected))
	for kind, digest := range expected {
		if digest == "" {
			continue
		}
		_, result[kind] = available[digest]
	}
	return result, nil
}

func (c Client) collectManifestDescriptorDigests(
	ctx context.Context,
	reference Reference,
	available, visited map[string]struct{},
) error {
	if _, ok := visited[reference.Digest]; ok {
		return nil
	}
	visited[reference.Digest] = struct{}{}
	available[reference.Digest] = struct{}{}
	manifest, err := c.FetchManifest(ctx, reference)
	if err != nil {
		return err
	}
	var graph struct {
		Config    *Descriptor  `json:"config"`
		Layers    []Descriptor `json:"layers"`
		Manifests []Descriptor `json:"manifests"`
		Blobs     []Descriptor `json:"blobs"`
	}
	if err := json.Unmarshal(manifest.Content, &graph); err != nil {
		return fmt.Errorf("decode OCI evidence manifest %s: %w", reference.String(), err)
	}
	if graph.Config != nil && graph.Config.Digest != "" {
		if !validSHA256Digest(graph.Config.Digest) {
			return errors.New("OCI evidence manifest contains an invalid config digest")
		}
		available[graph.Config.Digest] = struct{}{}
	}
	for _, descriptor := range append(graph.Layers, graph.Blobs...) {
		if !validSHA256Digest(descriptor.Digest) {
			return errors.New("OCI evidence manifest contains an invalid blob digest")
		}
		available[descriptor.Digest] = struct{}{}
	}
	for _, descriptor := range graph.Manifests {
		if !validSHA256Digest(descriptor.Digest) {
			return errors.New("OCI evidence index contains an invalid manifest digest")
		}
		if err := c.collectManifestDescriptorDigests(ctx, Reference{
			Registry: reference.Registry, Repository: reference.Repository,
			Digest: descriptor.Digest,
		}, available, visited); err != nil {
			return err
		}
	}
	return nil
}

// CopyManifestGraph copies an immutable manifest/index and every referenced
// manifest/config/layer to another configured registry. Each write is followed
// by exact digest readback. The operation is safe to repeat after interruption:
// content-addressed blobs and manifests already present are verified and
// skipped.
func (c Client) CopyManifestGraph(
	ctx context.Context,
	source, destination Reference,
) error {
	if source.Digest != destination.Digest {
		return errors.New("source and destination manifest digests must match")
	}
	visited := make(map[string]struct{})
	return c.copyManifestGraph(ctx, source, destination, visited)
}

func (c Client) copyManifestGraph(
	ctx context.Context,
	source, destination Reference,
	visited map[string]struct{},
) error {
	key := source.Repository + "@" + source.Digest + "->" +
		destination.Repository + "@" + destination.Digest
	if _, exists := visited[key]; exists {
		return nil
	}
	visited[key] = struct{}{}
	present, err := c.ManifestPresent(ctx, destination)
	if err != nil {
		return err
	}
	manifest, err := c.FetchManifest(ctx, source)
	if err != nil {
		return err
	}
	var graph struct {
		Config    *Descriptor  `json:"config"`
		Layers    []Descriptor `json:"layers"`
		Manifests []Descriptor `json:"manifests"`
		Blobs     []Descriptor `json:"blobs"`
	}
	if err := json.Unmarshal(manifest.Content, &graph); err != nil {
		return fmt.Errorf("decode OCI manifest graph %s: %w", source.String(), err)
	}
	for _, descriptor := range graph.Manifests {
		if !validSHA256Digest(descriptor.Digest) {
			return errors.New("OCI manifest graph contains an invalid child digest")
		}
		childSource := Reference{
			Registry: source.Registry, Repository: source.Repository, Digest: descriptor.Digest,
		}
		childDestination := Reference{
			Registry: destination.Registry, Repository: destination.Repository, Digest: descriptor.Digest,
		}
		if err := c.copyManifestGraph(ctx, childSource, childDestination, visited); err != nil {
			return err
		}
	}
	blobs := append([]Descriptor(nil), graph.Layers...)
	blobs = append(blobs, graph.Blobs...)
	if graph.Config != nil && graph.Config.Digest != "" {
		blobs = append(blobs, *graph.Config)
	}
	for _, descriptor := range blobs {
		if !validSHA256Digest(descriptor.Digest) {
			return errors.New("OCI manifest graph contains an invalid blob digest")
		}
		if err := c.copyBlob(ctx, source, destination, descriptor); err != nil {
			return err
		}
	}
	if !present {
		if err := c.PutManifest(ctx, destination, manifest.MediaType, manifest.Content); err != nil {
			return err
		}
	}
	readback, err := c.FetchManifest(ctx, destination)
	if err != nil {
		return err
	}
	if readback.Digest != source.Digest {
		return fmt.Errorf("destination readback digest %s does not match source %s", readback.Digest, source.Digest)
	}
	referrers, err := c.fetchReferrers(ctx, source, source.Digest)
	if err != nil {
		return err
	}
	expectedReferrers := make(map[string]struct{}, len(referrers))
	for _, descriptor := range referrers {
		if !validSHA256Digest(descriptor.Digest) {
			return errors.New("OCI referrers index contains an invalid manifest digest")
		}
		expectedReferrers[descriptor.Digest] = struct{}{}
		if err := c.copyManifestGraph(ctx, Reference{
			Registry: source.Registry, Repository: source.Repository,
			Digest: descriptor.Digest,
		}, Reference{
			Registry: destination.Registry, Repository: destination.Repository,
			Digest: descriptor.Digest,
		}, visited); err != nil {
			return fmt.Errorf("copy OCI referrer %s: %w", descriptor.Digest, err)
		}
	}
	if len(expectedReferrers) != 0 {
		mirrored, err := c.fetchReferrers(ctx, destination, destination.Digest)
		if err != nil {
			return err
		}
		for _, descriptor := range mirrored {
			delete(expectedReferrers, descriptor.Digest)
		}
		if len(expectedReferrers) != 0 {
			return errors.New("destination OCI referrer readback is incomplete")
		}
	}
	return nil
}

func (c Client) copyBlob(
	ctx context.Context,
	source, destination Reference,
	descriptor Descriptor,
) error {
	present, err := c.BlobPresent(ctx, destination, descriptor.Digest)
	if err != nil {
		return err
	}
	if present {
		return nil
	}
	content, err := c.FetchBlob(ctx, source, descriptor.Digest)
	if err != nil {
		return err
	}
	if descriptor.Size > 0 && int64(len(content)) != descriptor.Size {
		return fmt.Errorf("OCI blob %s size %d does not match descriptor %d", descriptor.Digest, len(content), descriptor.Size)
	}
	if err := c.PutBlob(ctx, destination, descriptor.Digest, content); err != nil {
		return err
	}
	present, err = c.BlobPresent(ctx, destination, descriptor.Digest)
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("destination OCI blob %s was not present after upload", descriptor.Digest)
	}
	return nil
}

func (c Client) ManifestPresent(ctx context.Context, reference Reference) (bool, error) {
	endpoint, err := c.endpoint(reference.Registry)
	if err != nil {
		return false, err
	}
	if !validSHA256Digest(reference.Digest) {
		return false, errors.New("manifest reference digest is invalid")
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodHead,
		endpoint+"/v2/"+escapeRepository(reference.Repository)+"/manifests/"+url.PathEscape(reference.Digest),
		nil,
	)
	if err != nil {
		return false, err
	}
	response, err := c.doAuthorized(ctx, request, reference.Registry)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, fmt.Errorf("inspect OCI manifest %s: %s", reference.String(), response.Status)
	}
	if advertised := response.Header.Get("Docker-Content-Digest"); advertised != "" &&
		advertised != reference.Digest {
		return false, fmt.Errorf("OCI registry advertised digest %s for requested %s", advertised, reference.Digest)
	}
	return true, nil
}

func (c Client) BlobPresent(ctx context.Context, reference Reference, digest string) (bool, error) {
	endpoint, err := c.endpoint(reference.Registry)
	if err != nil {
		return false, err
	}
	if !validSHA256Digest(digest) {
		return false, errors.New("blob digest is invalid")
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodHead,
		endpoint+"/v2/"+escapeRepository(reference.Repository)+"/blobs/"+url.PathEscape(digest),
		nil,
	)
	if err != nil {
		return false, err
	}
	response, err := c.doAuthorized(ctx, request, reference.Registry)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, fmt.Errorf("inspect OCI blob %s: %s", digest, response.Status)
	}
	return true, nil
}

func (c Client) FetchBlob(ctx context.Context, reference Reference, digest string) ([]byte, error) {
	endpoint, err := c.endpoint(reference.Registry)
	if err != nil {
		return nil, err
	}
	if !validSHA256Digest(digest) {
		return nil, errors.New("blob digest is invalid")
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet,
		endpoint+"/v2/"+escapeRepository(reference.Repository)+"/blobs/"+url.PathEscape(digest),
		nil,
	)
	if err != nil {
		return nil, err
	}
	response, err := c.doAuthorized(ctx, request, reference.Registry)
	if err != nil {
		return nil, fmt.Errorf("fetch OCI blob %s: %w", digest, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return nil, fmt.Errorf("fetch OCI blob %s: %s", digest, response.Status)
	}
	maximum := c.MaxBlobBytes
	if maximum <= 0 {
		maximum = defaultMaxInMemoryBlobBytes
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("read OCI blob %s: %w", digest, err)
	}
	if int64(len(content)) > maximum {
		return nil, fmt.Errorf("OCI blob %s exceeds maximum size %d", digest, maximum)
	}
	if digestBytes(content) != digest {
		return nil, fmt.Errorf("OCI blob content does not match requested digest %s", digest)
	}
	return content, nil
}

func (c Client) PutBlob(
	ctx context.Context,
	reference Reference,
	digest string,
	content []byte,
) error {
	endpoint, err := c.endpoint(reference.Registry)
	if err != nil {
		return err
	}
	if !validSHA256Digest(digest) || digestBytes(content) != digest {
		return errors.New("uploaded OCI blob content does not match digest")
	}
	base := endpoint + "/v2/" + escapeRepository(reference.Repository)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/blobs/uploads/", nil)
	if err != nil {
		return err
	}
	response, err := c.doAuthorized(ctx, request, reference.Registry)
	if err != nil {
		return fmt.Errorf("start OCI blob upload %s: %w", digest, err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("start OCI blob upload %s: %s", digest, response.Status)
	}
	location, err := c.resolveUploadLocation(reference.Registry, response.Header.Get("Location"))
	if err != nil {
		return err
	}
	parsed, _ := url.Parse(location)
	query := parsed.Query()
	query.Set("digest", digest)
	parsed.RawQuery = query.Encode()
	request, err = http.NewRequestWithContext(
		ctx, http.MethodPut, parsed.String(), bytes.NewReader(content),
	)
	if err != nil {
		return err
	}
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(content)), nil
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	response, err = c.doAuthorized(ctx, request, reference.Registry)
	if err != nil {
		return fmt.Errorf("complete OCI blob upload %s: %w", digest, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("complete OCI blob upload %s: %s", digest, response.Status)
	}
	return nil
}

func (c Client) PutManifest(
	ctx context.Context,
	reference Reference,
	mediaType string,
	content []byte,
) error {
	endpoint, err := c.endpoint(reference.Registry)
	if err != nil {
		return err
	}
	if !validSHA256Digest(reference.Digest) || digestBytes(content) != reference.Digest {
		return errors.New("uploaded OCI manifest content does not match digest")
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPut,
		endpoint+"/v2/"+escapeRepository(reference.Repository)+"/manifests/"+url.PathEscape(reference.Digest),
		bytes.NewReader(content),
	)
	if err != nil {
		return err
	}
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(content)), nil
	}
	if mediaType == "" {
		mediaType = "application/vnd.oci.image.manifest.v1+json"
	}
	request.Header.Set("Content-Type", mediaType)
	response, err := c.doAuthorized(ctx, request, reference.Registry)
	if err != nil {
		return fmt.Errorf("put OCI manifest %s: %w", reference.String(), err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("put OCI manifest %s: %s", reference.String(), response.Status)
	}
	if advertised := response.Header.Get("Docker-Content-Digest"); advertised != "" &&
		advertised != reference.Digest {
		return fmt.Errorf("OCI registry stored unexpected digest %s for %s", advertised, reference.Digest)
	}
	return nil
}

// EnsureManifestAlias creates a unique operation-scoped alias for an already
// uploaded immutable manifest.  The alias is never accepted as artifact
// identity; it is a sink-side idempotency receipt. Replays first resolve the
// alias and succeed only when it still names the exact expected digest.
func (c Client) EnsureManifestAlias(
	ctx context.Context,
	reference Reference,
	alias string,
) error {
	if !validOperationAlias(alias) {
		return errors.New("OCI operation alias is invalid")
	}
	manifest, err := c.FetchManifest(ctx, reference)
	if err != nil {
		return err
	}
	observed, found, err := c.resolveManifestAlias(ctx, reference, alias)
	if err != nil {
		return err
	}
	if found {
		if observed != reference.Digest {
			return fmt.Errorf(
				"OCI operation alias %q already names %s instead of %s",
				alias, observed, reference.Digest,
			)
		}
		return nil
	}
	endpoint, err := c.endpoint(reference.Registry)
	if err != nil {
		return err
	}
	target := endpoint + "/v2/" + escapeRepository(reference.Repository) +
		"/manifests/" + url.PathEscape(alias)
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPut, target, bytes.NewReader(manifest.Content),
	)
	if err != nil {
		return err
	}
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(manifest.Content)), nil
	}
	request.Header.Set("Content-Type", manifest.MediaType)
	response, err := c.doAuthorized(ctx, request, reference.Registry)
	if err != nil {
		return fmt.Errorf("put OCI operation alias %q: %w", alias, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("put OCI operation alias %q: %s", alias, response.Status)
	}
	observed, found, err = c.resolveManifestAlias(ctx, reference, alias)
	if err != nil {
		return err
	}
	if !found || observed != reference.Digest {
		return errors.New("OCI operation alias readback did not match the immutable manifest")
	}
	return nil
}

func (c Client) resolveManifestAlias(
	ctx context.Context,
	reference Reference,
	alias string,
) (string, bool, error) {
	endpoint, err := c.endpoint(reference.Registry)
	if err != nil {
		return "", false, err
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet,
		endpoint+"/v2/"+escapeRepository(reference.Repository)+
			"/manifests/"+url.PathEscape(alias), nil,
	)
	if err != nil {
		return "", false, err
	}
	request.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))
	response, err := c.doAuthorized(ctx, request, reference.Registry)
	if err != nil {
		return "", false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", false, fmt.Errorf("resolve OCI operation alias %q: %s", alias, response.Status)
	}
	maximum := c.MaxManifestBytes
	if maximum <= 0 {
		maximum = defaultMaxManifestBytes
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return "", false, err
	}
	if int64(len(content)) > maximum {
		return "", false, errors.New("OCI operation alias manifest exceeds size limit")
	}
	digest := digestBytes(content)
	if advertised := strings.TrimSpace(response.Header.Get("Docker-Content-Digest")); advertised != "" && advertised != digest {
		return "", false, errors.New("OCI operation alias advertised a mismatched digest")
	}
	return digest, true, nil
}

func validOperationAlias(value string) bool {
	const prefix = "wolf-operation-"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	for _, character := range value[len(prefix):] {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func (c Client) DeleteManifest(ctx context.Context, reference Reference) (bool, error) {
	endpoint, err := c.endpoint(reference.Registry)
	if err != nil {
		return false, err
	}
	if !validSHA256Digest(reference.Digest) {
		return false, errors.New("manifest reference digest is invalid")
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodDelete,
		endpoint+"/v2/"+escapeRepository(reference.Repository)+"/manifests/"+url.PathEscape(reference.Digest),
		nil,
	)
	if err != nil {
		return false, err
	}
	response, err := c.doAuthorized(ctx, request, reference.Registry)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode == http.StatusNotFound {
		return true, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, fmt.Errorf("delete OCI manifest %s: %s", reference.String(), response.Status)
	}
	present, err := c.ManifestPresent(ctx, reference)
	if err != nil {
		return false, err
	}
	return !present, nil
}

func (c Client) resolveUploadLocation(registry, location string) (string, error) {
	endpoint, err := c.endpoint(registry)
	if err != nil {
		return "", err
	}
	base, _ := url.Parse(endpoint)
	locationURL, err := url.Parse(strings.TrimSpace(location))
	if err != nil || strings.TrimSpace(location) == "" {
		return "", errors.New("OCI registry blob upload Location is invalid")
	}
	resolved := base.ResolveReference(locationURL)
	if resolved.Scheme != base.Scheme || resolved.Host != base.Host ||
		resolved.User != nil || resolved.Fragment != "" {
		return "", errors.New("OCI registry blob upload Location escaped configured origin")
	}
	return resolved.String(), nil
}

func (c Client) VerifyRelease(ctx context.Context, release scannerbundle.ReleaseManifest) ([]Verification, error) {
	if err := release.Validate(); err != nil {
		return nil, fmt.Errorf("validate scanner release: %w", err)
	}
	concurrency := c.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	results := make([]Verification, len(release.Images))
	semaphore := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for index, image := range release.Images {
		index, image := index, image
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[index] = Verification{
					ImageKey: image.Key, Ref: image.Reference, Error: ctx.Err().Error(),
				}
				return
			}
			results[index] = c.verifyImage(ctx, image)
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return results, err
	}
	return results, nil
}

func (c Client) verifyImage(ctx context.Context, image scannerbundle.ReleaseImage) Verification {
	result := Verification{ImageKey: image.Key, Ref: image.Reference}
	reference, err := ParseReference(image.Reference)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if reference.Digest != image.Digest {
		result.Error = "release image reference and digest differ"
		return result
	}
	manifest, err := c.FetchManifest(ctx, reference)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	descriptors := make(map[string]string, len(manifest.Descriptors))
	for _, descriptor := range manifest.Descriptors {
		platform := descriptor.Platform.String()
		if platform == "/" {
			continue
		}
		if !validSHA256Digest(descriptor.Digest) {
			result.Error = "registry returned an invalid platform digest"
			return result
		}
		if _, duplicate := descriptors[platform]; duplicate {
			result.Error = "registry returned a duplicate platform descriptor"
			return result
		}
		descriptors[platform] = descriptor.Digest
	}
	for platform, expected := range image.Platforms {
		actual, exists := descriptors[platform]
		if len(manifest.Descriptors) == 0 && len(image.Platforms) == 1 && expected == image.Digest {
			actual, exists = image.Digest, true
		}
		if !exists || actual != expected {
			result.Error = fmt.Sprintf("platform %s digest mismatch", platform)
			return result
		}
		result.Platforms = append(result.Platforms, platform)
	}
	sort.Strings(result.Platforms)
	result.Verified = true
	return result
}

func (c Client) endpoint(registry string) (string, error) {
	config, ok := c.Endpoints[registry]
	if !ok {
		return "", fmt.Errorf("OCI registry %q is not configured", registry)
	}
	base := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("OCI registry %q has invalid configured endpoint", registry)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("OCI registry %q endpoint must not contain a path", registry)
	}
	if parsed.Host != registry {
		return "", fmt.Errorf("OCI registry endpoint host %q does not match configured registry %q", parsed.Host, registry)
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func (c Client) authorize(ctx context.Context, request *http.Request, registry string) error {
	if c.Credentials == nil {
		return nil
	}
	value, err := c.Credentials.Authorization(ctx, registry)
	if err != nil {
		return fmt.Errorf("resolve OCI registry authorization: %w", err)
	}
	if value != "" {
		if strings.ContainsAny(value, "\r\n") {
			return errors.New("OCI registry authorization contains a newline")
		}
		request.Header.Set("Authorization", value)
	}
	return nil
}

func (c Client) doAuthorized(
	ctx context.Context,
	request *http.Request,
	registry string,
) (*http.Response, error) {
	if err := c.authorize(ctx, request, registry); err != nil {
		return nil, err
	}
	response, err := c.httpClient().Do(request)
	if err != nil || response.StatusCode != http.StatusUnauthorized {
		return response, err
	}
	challenge := strings.TrimSpace(response.Header.Get("WWW-Authenticate"))
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return response, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	_ = response.Body.Close()
	token, err := c.exchangeBearerChallenge(ctx, registry, request.Header.Get("Authorization"), challenge)
	if err != nil {
		return nil, err
	}
	retry := request.Clone(ctx)
	retry.Header = request.Header.Clone()
	if request.GetBody != nil {
		body, bodyErr := request.GetBody()
		if bodyErr != nil {
			return nil, bodyErr
		}
		retry.Body = body
	}
	retry.Header.Set("Authorization", "Bearer "+token)
	return c.httpClient().Do(retry)
}

func (c Client) exchangeBearerChallenge(
	ctx context.Context,
	registry, authorization, challenge string,
) (string, error) {
	parameters, err := parseBearerChallenge(challenge)
	if err != nil {
		return "", err
	}
	realm, err := url.Parse(parameters["realm"])
	if err != nil || realm.Host == "" || realm.User != nil || realm.Fragment != "" {
		return "", errors.New("OCI registry Bearer challenge realm is invalid")
	}
	endpoint, err := c.endpoint(registry)
	if err != nil {
		return "", err
	}
	registryURL, _ := url.Parse(endpoint)
	if realm.Scheme != "https" &&
		!(registryURL.Scheme == "http" && realm.Scheme == "http" && realm.Host == registryURL.Host) {
		return "", errors.New("OCI registry Bearer challenge realm must use HTTPS")
	}
	if !c.tokenHostAllowed(registry, realm.Host) {
		return "", fmt.Errorf("OCI registry Bearer token host %q is not allowed", realm.Host)
	}
	query := realm.Query()
	for _, name := range []string{"service", "scope"} {
		if value := strings.TrimSpace(parameters[name]); value != "" {
			query.Set(name, value)
		}
	}
	realm.RawQuery = query.Encode()
	tokenRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return "", err
	}
	tokenRequest.Header.Set("Accept", "application/json")
	if authorization != "" {
		tokenRequest.Header.Set("Authorization", authorization)
	}
	tokenClient := *c.httpClient()
	tokenClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("OCI Bearer token service redirects are not allowed")
	}
	response, err := tokenClient.Do(tokenRequest)
	if err != nil {
		return "", fmt.Errorf("exchange OCI registry Bearer challenge: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return "", fmt.Errorf("exchange OCI registry Bearer challenge: %s", response.Status)
	}
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&payload); err != nil {
		return "", fmt.Errorf("decode OCI registry Bearer token: %w", err)
	}
	token := strings.TrimSpace(payload.Token)
	if token == "" {
		token = strings.TrimSpace(payload.AccessToken)
	}
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return "", errors.New("OCI registry Bearer token response is invalid")
	}
	return token, nil
}

func (c Client) tokenHostAllowed(registry, host string) bool {
	host = strings.ToLower(host)
	if host == strings.ToLower(registry) {
		return true
	}
	for _, allowed := range c.TokenHosts[registry] {
		if strings.EqualFold(strings.TrimSpace(allowed), host) {
			return true
		}
	}
	return false
}

func parseBearerChallenge(value string) (map[string]string, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(strings.ToLower(value), "bearer ") {
		return nil, errors.New("OCI registry authentication challenge is not Bearer")
	}
	raw := strings.TrimSpace(value[len("Bearer "):])
	parameters := make(map[string]string)
	for len(raw) > 0 {
		raw = strings.TrimLeft(raw, " \t,")
		if raw == "" {
			break
		}
		equal := strings.IndexByte(raw, '=')
		if equal <= 0 {
			return nil, errors.New("OCI registry Bearer challenge is malformed")
		}
		name := strings.ToLower(strings.TrimSpace(raw[:equal]))
		raw = strings.TrimSpace(raw[equal+1:])
		if raw == "" || raw[0] != '"' {
			return nil, errors.New("OCI registry Bearer challenge values must be quoted")
		}
		raw = raw[1:]
		var value strings.Builder
		escaped := false
		closed := false
		index := 0
		for ; index < len(raw); index++ {
			character := raw[index]
			if escaped {
				value.WriteByte(character)
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == '"' {
				closed = true
				index++
				break
			}
			value.WriteByte(character)
		}
		if !closed || name == "" || value.Len() == 0 {
			return nil, errors.New("OCI registry Bearer challenge is malformed")
		}
		if _, duplicate := parameters[name]; duplicate {
			return nil, fmt.Errorf("OCI registry Bearer challenge repeats %q", name)
		}
		parameters[name] = value.String()
		raw = raw[index:]
	}
	if strings.TrimSpace(parameters["realm"]) == "" {
		return nil, errors.New("OCI registry Bearer challenge has no realm")
	}
	return parameters, nil
}

func (c Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func escapeRepository(repository string) string {
	parts := strings.Split(repository, "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return strings.Join(parts, "/")
}

func validSHA256Digest(value string) bool {
	algorithm, encoded, ok := strings.Cut(value, ":")
	if !ok || algorithm != "sha256" || len(encoded) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
