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
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/scannerbundle"
)

const defaultMaxBlobBytes = int64(16 << 30)

type TransferBlob struct {
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	MediaType string `json:"media_type"`
	Kind      string `json:"kind"`
	Path      string `json:"path"`
}

type TransferClosure struct {
	SourceReference string            `json:"source_reference"`
	SourceDigest    string            `json:"source_digest"`
	RootDigest      string            `json:"root_digest"`
	Platforms       map[string]string `json:"platforms"`
	Blobs           []TransferBlob    `json:"blobs"`
}

// FetchTransferClosure downloads a complete, selected-platform OCI closure to
// bounded local staging. It also includes all referrer manifests and their
// blobs when the registry implements the OCI referrers API.
func (c Client) FetchTransferClosure(
	ctx context.Context,
	reference Reference,
	selected []string,
	directory string,
) (TransferClosure, error) {
	if !filepath.IsAbs(directory) {
		return TransferClosure{}, errors.New("OCI transfer directory must be absolute")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return TransferClosure{}, err
	}
	root, err := c.FetchManifest(ctx, reference)
	if err != nil {
		return TransferClosure{}, err
	}
	closure := TransferClosure{
		SourceReference: reference.String(), SourceDigest: reference.Digest,
		RootDigest: reference.Digest, Platforms: map[string]string{},
	}
	blobs := make(map[string]TransferBlob)
	rootContent := root.Content
	children := root.Descriptors
	if len(children) != 0 {
		filtered, platforms, filterErr := filterPlatforms(children, selected)
		if filterErr != nil {
			return TransferClosure{}, filterErr
		}
		closure.Platforms = platforms
		if len(filtered) != len(children) {
			var document map[string]any
			if err := json.Unmarshal(root.Content, &document); err != nil {
				return TransferClosure{}, fmt.Errorf("decode OCI index for platform selection: %w", err)
			}
			document["manifests"] = filtered
			rootContent, err = json.Marshal(document)
			if err != nil {
				return TransferClosure{}, err
			}
			closure.RootDigest = digestBytes(rootContent)
			if err := writeTransferBytes(
				directory, reference.Digest, root.Content, root.MediaType,
				"oci-source-index", blobs,
			); err != nil {
				return TransferClosure{}, err
			}
		}
		children = filtered
	} else {
		if len(selected) > 1 {
			return TransferClosure{}, errors.New("single-platform OCI manifest cannot satisfy multiple selected platforms")
		}
		if len(selected) == 1 {
			closure.Platforms[selected[0]] = closure.RootDigest
		}
	}
	rootKind := "oci-image-index"
	if len(children) == 0 {
		rootKind = "oci-image-manifest"
	}
	if err := writeTransferBytes(
		directory, closure.RootDigest, rootContent, root.MediaType,
		rootKind, blobs,
	); err != nil {
		return TransferClosure{}, err
	}
	for _, child := range children {
		if err := c.fetchManifestClosure(
			ctx, reference, child, directory, "oci-image-manifest", blobs,
		); err != nil {
			return TransferClosure{}, err
		}
	}
	if len(children) == 0 {
		if err := c.fetchDocumentBlobs(
			ctx, reference, rootContent, directory, blobs,
		); err != nil {
			return TransferClosure{}, err
		}
	}
	subjects := []string{reference.Digest}
	for _, child := range children {
		subjects = append(subjects, child.Digest)
	}
	for _, subject := range subjects {
		referrers, err := c.fetchReferrers(ctx, reference, subject)
		if err != nil {
			return TransferClosure{}, err
		}
		for _, descriptor := range referrers {
			if err := c.fetchManifestClosure(
				ctx, reference, descriptor, directory,
				"oci-trust-manifest", blobs,
			); err != nil {
				return TransferClosure{}, err
			}
		}
	}
	closure.Blobs = make([]TransferBlob, 0, len(blobs))
	for _, blob := range blobs {
		closure.Blobs = append(closure.Blobs, blob)
	}
	sort.Slice(closure.Blobs, func(i, j int) bool {
		return closure.Blobs[i].Digest < closure.Blobs[j].Digest
	})
	return closure, nil
}

func filterPlatforms(
	descriptors []Descriptor,
	selected []string,
) ([]Descriptor, map[string]string, error) {
	available := make(map[string]Descriptor)
	for _, descriptor := range descriptors {
		platform := descriptor.Platform.String()
		if platform == "/" || !validSHA256Digest(descriptor.Digest) {
			return nil, nil, errors.New("OCI index contains an invalid platform descriptor")
		}
		if _, duplicate := available[platform]; duplicate {
			return nil, nil, fmt.Errorf("OCI index repeats platform %s", platform)
		}
		available[platform] = descriptor
	}
	if len(selected) == 0 {
		selected = make([]string, 0, len(available))
		for platform := range available {
			selected = append(selected, platform)
		}
		sort.Strings(selected)
	}
	result := make([]Descriptor, 0, len(selected))
	platforms := make(map[string]string, len(selected))
	seen := make(map[string]struct{}, len(selected))
	for _, platform := range selected {
		if _, duplicate := seen[platform]; duplicate {
			return nil, nil, fmt.Errorf("selected platform %q is duplicated", platform)
		}
		seen[platform] = struct{}{}
		descriptor, exists := available[platform]
		if !exists {
			return nil, nil, fmt.Errorf("selected platform %q is unavailable", platform)
		}
		result = append(result, descriptor)
		platforms[platform] = descriptor.Digest
	}
	return result, platforms, nil
}

func (c Client) fetchManifestClosure(
	ctx context.Context,
	parent Reference,
	descriptor Descriptor,
	directory, kind string,
	blobs map[string]TransferBlob,
) error {
	if !validSHA256Digest(descriptor.Digest) {
		return errors.New("OCI manifest descriptor digest is invalid")
	}
	if _, exists := blobs[descriptor.Digest]; exists {
		return nil
	}
	manifest, err := c.FetchManifest(ctx, Reference{
		Registry: parent.Registry, Repository: parent.Repository,
		Digest: descriptor.Digest,
	})
	if err != nil {
		return err
	}
	mediaType := descriptor.MediaType
	if mediaType == "" {
		mediaType = manifest.MediaType
	}
	if err := writeTransferBytes(
		directory, descriptor.Digest, manifest.Content, mediaType, kind, blobs,
	); err != nil {
		return err
	}
	return c.fetchDocumentBlobs(ctx, parent, manifest.Content, directory, blobs)
}

func (c Client) fetchDocumentBlobs(
	ctx context.Context,
	parent Reference,
	content []byte,
	directory string,
	blobs map[string]TransferBlob,
) error {
	var document struct {
		Config Descriptor   `json:"config"`
		Layers []Descriptor `json:"layers"`
	}
	if err := json.Unmarshal(content, &document); err != nil {
		return fmt.Errorf("decode OCI manifest descriptors: %w", err)
	}
	descriptors := append([]Descriptor(nil), document.Layers...)
	if document.Config.Digest != "" {
		descriptors = append(descriptors, document.Config)
	}
	for _, descriptor := range descriptors {
		if _, exists := blobs[descriptor.Digest]; exists {
			continue
		}
		path := filepath.Join(directory, strings.TrimPrefix(descriptor.Digest, "sha256:"))
		size, err := c.fetchBlobToFile(
			ctx, parent, descriptor.Digest, descriptor.Size, path,
		)
		if err != nil {
			return err
		}
		blobs[descriptor.Digest] = TransferBlob{
			Digest: descriptor.Digest, Size: size,
			MediaType: descriptor.MediaType, Kind: "oci-image-blob", Path: path,
		}
	}
	return nil
}

func (c Client) fetchReferrers(
	ctx context.Context,
	parent Reference,
	subject string,
) ([]Descriptor, error) {
	endpoint, err := c.endpoint(parent.Registry)
	if err != nil {
		return nil, err
	}
	value := endpoint + "/v2/" + escapeRepository(parent.Repository) +
		"/referrers/" + url.PathEscape(subject)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, value, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.oci.image.index.v1+json")
	response, err := c.doAuthorized(ctx, request, parent.Registry)
	if err != nil {
		return nil, fmt.Errorf("fetch OCI referrers for %s: %w", subject, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound ||
		response.StatusCode == http.StatusMethodNotAllowed {
		return nil, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch OCI referrers for %s: %s", subject, response.Status)
	}
	var document struct {
		Manifests []Descriptor `json:"manifests"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, defaultMaxManifestBytes+1))
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode OCI referrers for %s: %w", subject, err)
	}
	return document.Manifests, nil
}

func (c Client) fetchBlobToFile(
	ctx context.Context,
	parent Reference,
	digest string,
	expectedSize int64,
	path string,
) (int64, error) {
	if !validSHA256Digest(digest) {
		return 0, errors.New("OCI blob digest is invalid")
	}
	endpoint, err := c.endpoint(parent.Registry)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet,
		endpoint+"/v2/"+escapeRepository(parent.Repository)+"/blobs/"+url.PathEscape(digest),
		nil,
	)
	if err != nil {
		return 0, err
	}
	response, err := c.doAuthorized(ctx, request, parent.Registry)
	if err != nil {
		return 0, fmt.Errorf("fetch OCI blob %s: %w", digest, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, fmt.Errorf("fetch OCI blob %s: %s", digest, response.Status)
	}
	maximum := c.MaxBlobBytes
	if maximum <= 0 {
		maximum = defaultMaxBlobBytes
	}
	if expectedSize > maximum {
		return 0, fmt.Errorf("OCI blob %s exceeds maximum size %d", digest, maximum)
	}
	if response.ContentLength > maximum {
		return 0, fmt.Errorf("OCI blob %s exceeds maximum size %d", digest, maximum)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(
		io.MultiWriter(file, hash),
		io.LimitReader(response.Body, maximum+1),
	)
	closeErr := file.Close()
	if copyErr != nil {
		return 0, copyErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if written > maximum || (expectedSize > 0 && written != expectedSize) {
		return 0, fmt.Errorf("OCI blob %s size mismatch", digest)
	}
	actual := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if actual != digest {
		return 0, fmt.Errorf("OCI blob %s digest mismatch: got %s", digest, actual)
	}
	return written, nil
}

func writeTransferBytes(
	directory, digest string,
	content []byte,
	mediaType, kind string,
	blobs map[string]TransferBlob,
) error {
	if digestBytes(content) != digest {
		return fmt.Errorf("OCI document digest mismatch for %s", digest)
	}
	path := filepath.Join(directory, strings.TrimPrefix(digest, "sha256:"))
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return err
	}
	blobs[digest] = TransferBlob{
		Digest: digest, Size: int64(len(content)),
		MediaType: mediaType, Kind: kind, Path: path,
	}
	return nil
}

type PushResult struct {
	Reference string `json:"reference"`
	Digest    string `json:"digest"`
	ReadBack  bool   `json:"read_back_verified"`
}

// PushBundleImage performs digest-idempotent OCI uploads, then re-reads the
// destination root and platform mapping before reporting success.
func (c Client) PushBundleImage(
	ctx context.Context,
	registry, repository string,
	image scannerbundle.ReleaseImage,
	records map[string]scannerbundle.OCIRecord,
	root string,
) (PushResult, error) {
	reference := Reference{Registry: registry, Repository: repository, Digest: image.Digest}
	var manifests []scannerbundle.OCIRecord
	for _, digest := range image.BlobDigests {
		artifact, ok := records[digest]
		if !ok {
			return PushResult{}, fmt.Errorf("OCI artifact %s is missing", digest)
		}
		if strings.Contains(artifact.Kind, "manifest") ||
			strings.Contains(artifact.Kind, "index") {
			manifests = append(manifests, artifact)
			continue
		}
		if err := c.pushBlob(
			ctx, reference, artifact,
			filepath.Join(root, filepath.FromSlash(artifact.BundlePath)),
		); err != nil {
			return PushResult{}, err
		}
	}
	sort.SliceStable(manifests, func(i, j int) bool {
		left, right := manifestUploadPriority(manifests[i], image.Digest),
			manifestUploadPriority(manifests[j], image.Digest)
		if left != right {
			return left < right
		}
		return manifests[i].Digest < manifests[j].Digest
	})
	for _, artifact := range manifests {
		present, err := c.ManifestPresent(ctx, Reference{
			Registry: registry, Repository: repository, Digest: artifact.Digest,
		})
		if err != nil {
			return PushResult{}, fmt.Errorf("inspect destination OCI manifest %s: %w", artifact.Digest, err)
		}
		if !present {
			if err := c.putManifest(
				ctx, reference, artifact,
				filepath.Join(root, filepath.FromSlash(artifact.BundlePath)),
			); err != nil {
				return PushResult{}, err
			}
		}
	}
	// Re-read every destination manifest, including signature/provenance/SBOM
	// referrers, only after the complete graph has been uploaded.
	for _, artifact := range manifests {
		readBack, err := c.FetchManifest(ctx, Reference{
			Registry: registry, Repository: repository, Digest: artifact.Digest,
		})
		if err != nil {
			return PushResult{}, fmt.Errorf("read back imported OCI manifest %s: %w", artifact.Digest, err)
		}
		if readBack.Digest != artifact.Digest {
			return PushResult{}, fmt.Errorf(
				"destination OCI manifest changed from %s to %s",
				artifact.Digest, readBack.Digest,
			)
		}
	}
	manifest, err := c.FetchManifest(ctx, reference)
	if err != nil {
		return PushResult{}, fmt.Errorf("read back imported OCI image: %w", err)
	}
	if manifest.Digest != image.Digest {
		return PushResult{}, errors.New("destination OCI root digest changed after upload")
	}
	verification := c.verifyImage(ctx, scannerbundle.ReleaseImage{
		Reference: reference.String(), Digest: image.Digest,
		Platforms: image.Platforms,
	})
	if !verification.Verified {
		return PushResult{}, fmt.Errorf("destination OCI platform verification failed: %s", verification.Error)
	}
	return PushResult{
		Reference: reference.String(), Digest: image.Digest, ReadBack: true,
	}, nil
}

// PushBundleArtifact uploads and re-reads a non-image OCI evidence graph. The
// artifact carries both its storage root and payload closure; unlike an image
// it has no platform mapping to verify.
func (c Client) PushBundleArtifact(
	ctx context.Context,
	registry, repository string,
	artifact scannerbundle.ReleaseArtifact,
	records map[string]scannerbundle.OCIRecord,
	root string,
) (PushResult, error) {
	if artifact.StorageDigest == "" || len(artifact.OCIClosure) == 0 {
		return PushResult{}, errors.New("bundle artifact has no OCI storage closure")
	}
	reference := Reference{Registry: registry, Repository: repository, Digest: artifact.StorageDigest}
	var manifests []scannerbundle.OCIRecord
	for _, digest := range artifact.OCIClosure {
		record, ok := records[digest]
		if !ok {
			return PushResult{}, fmt.Errorf("OCI artifact %s is missing", digest)
		}
		path := filepath.Join(root, filepath.FromSlash(record.BundlePath))
		if strings.Contains(record.Kind, "manifest") || strings.Contains(record.Kind, "index") {
			manifests = append(manifests, record)
			continue
		}
		if err := c.pushBlob(ctx, reference, record, path); err != nil {
			return PushResult{}, err
		}
	}
	sort.SliceStable(manifests, func(i, j int) bool {
		left := artifactManifestUploadPriority(manifests[i], artifact.StorageDigest)
		right := artifactManifestUploadPriority(manifests[j], artifact.StorageDigest)
		if left != right {
			return left < right
		}
		return manifests[i].Digest < manifests[j].Digest
	})
	for _, record := range manifests {
		item := Reference{Registry: registry, Repository: repository, Digest: record.Digest}
		present, err := c.ManifestPresent(ctx, item)
		if err != nil {
			return PushResult{}, err
		}
		if !present {
			if err := c.putManifest(
				ctx, reference, record,
				filepath.Join(root, filepath.FromSlash(record.BundlePath)),
			); err != nil {
				return PushResult{}, err
			}
		}
	}
	for _, record := range manifests {
		readBack, err := c.FetchManifest(ctx, Reference{
			Registry: registry, Repository: repository, Digest: record.Digest,
		})
		if err != nil || readBack.Digest != record.Digest {
			return PushResult{}, fmt.Errorf("read back imported OCI artifact manifest %s", record.Digest)
		}
	}
	rootManifest, err := c.FetchManifest(ctx, reference)
	if err != nil || rootManifest.Digest != artifact.StorageDigest {
		return PushResult{}, errors.New("destination OCI artifact root changed after upload")
	}
	return PushResult{Reference: reference.String(), Digest: artifact.StorageDigest, ReadBack: true}, nil
}

func artifactManifestUploadPriority(record scannerbundle.OCIRecord, rootDigest string) int {
	if record.Digest == rootDigest {
		return 2
	}
	return 1
}

func manifestUploadPriority(record scannerbundle.OCIRecord, rootDigest string) int {
	switch {
	case strings.Contains(record.Kind, "trust"):
		return 2
	case record.Digest == rootDigest:
		return 1
	default:
		return 0
	}
}

func (c Client) pushBlob(
	ctx context.Context,
	reference Reference,
	artifact scannerbundle.OCIRecord,
	path string,
) error {
	endpoint, err := c.endpoint(reference.Registry)
	if err != nil {
		return err
	}
	base := endpoint + "/v2/" + escapeRepository(reference.Repository)
	head, _ := http.NewRequestWithContext(
		ctx, http.MethodHead, base+"/blobs/"+url.PathEscape(artifact.Digest), nil,
	)
	response, err := c.doAuthorized(ctx, head, reference.Registry)
	if err != nil {
		return fmt.Errorf("inspect destination OCI blob %s: %w", artifact.Digest, err)
	}
	if response == nil {
		return errors.New("OCI registry returned no blob inspection response")
	}
	_ = response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	if response.StatusCode != http.StatusNotFound {
		return fmt.Errorf("inspect destination OCI blob %s: %s", artifact.Digest, response.Status)
	}
	start, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/blobs/uploads/", nil)
	response, err = c.doAuthorized(ctx, start, reference.Registry)
	if err != nil {
		return err
	}
	if response == nil {
		return errors.New("OCI registry returned no upload response")
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("start OCI blob upload: %s", response.Status)
	}
	location := strings.TrimSpace(response.Header.Get("Location"))
	uploadURL, err := resolveUploadLocation(endpoint, location)
	if err != nil {
		return err
	}
	parsed, _ := url.Parse(uploadURL)
	query := parsed.Query()
	query.Set("digest", artifact.Digest)
	parsed.RawQuery = query.Encode()
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPut, parsed.String(), file)
	request.ContentLength = artifact.Size
	request.Header.Set("Content-Type", "application/octet-stream")
	response, err = c.doAuthorized(ctx, request, reference.Registry)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return fmt.Errorf("complete OCI blob upload: %s", response.Status)
	}
	return nil
}

func (c Client) putManifest(
	ctx context.Context,
	reference Reference,
	artifact scannerbundle.OCIRecord,
	path string,
) error {
	value, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	endpoint, err := c.endpoint(reference.Registry)
	if err != nil {
		return err
	}
	target := endpoint + "/v2/" + escapeRepository(reference.Repository) +
		"/manifests/" + url.PathEscape(artifact.Digest)
	request, _ := http.NewRequestWithContext(ctx, http.MethodPut, target, bytes.NewReader(value))
	request.Header.Set("Content-Type", artifact.MediaType)
	request.ContentLength = int64(len(value))
	response, err := c.doAuthorized(ctx, request, reference.Registry)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated &&
		response.StatusCode != http.StatusAccepted &&
		response.StatusCode != http.StatusOK {
		return fmt.Errorf("put OCI manifest %s: %s", artifact.Digest, response.Status)
	}
	if advertised := response.Header.Get("Docker-Content-Digest"); advertised != "" &&
		advertised != artifact.Digest {
		return fmt.Errorf("destination rewrote OCI manifest %s as %s", artifact.Digest, advertised)
	}
	return nil
}

func resolveUploadLocation(endpoint, location string) (string, error) {
	base, _ := url.Parse(endpoint)
	target, err := url.Parse(location)
	if err != nil {
		return "", errors.New("OCI upload location is invalid")
	}
	target = base.ResolveReference(target)
	if target.Scheme != base.Scheme || target.Host != base.Host ||
		target.User != nil || target.Fragment != "" {
		return "", errors.New("OCI upload location leaves configured registry")
	}
	return target.String(), nil
}
