package scannerbundle

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

const (
	defaultMaxFiles      = 100_000
	defaultMaxFileBytes  = int64(32 << 30)
	defaultMaxTotalBytes = int64(128 << 30)
	minDecoderMemory     = 64 << 20
	maxDecoderMemory     = 512 << 20
)

// Source is a content-addressed file to add to a release bundle. Open must
// return a new reader positioned at byte zero.
type Source struct {
	Path   string
	Size   int64
	Digest string
	Open   func() (io.ReadCloser, error)
}

type WriteOptions struct {
	Manifest        ReleaseManifest
	Sources         []Source
	Signer          ManifestSigner
	SourceDateEpoch time.Time
	SchemaVersion   string
}

type FileRecord struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type Index struct {
	SchemaVersion  string       `json:"schema_version"`
	ReleaseID      string       `json:"release_id"`
	ManifestDigest string       `json:"manifest_digest"`
	CreatedAt      time.Time    `json:"created_at"`
	Files          []FileRecord `json:"files"`
}

type ReadOptions struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
	AllowUnsigned bool
	Verifier      ManifestVerifier
}

type ImportedBundle struct {
	Root           string
	Manifest       ReleaseManifest
	ManifestDigest string
	Signature      *Signature
	Files          map[string]FileRecord
	SchemaVersion  string
}

// Write creates a deterministic tar.zst scanner release bundle.
func Write(ctx context.Context, output io.Writer, opts WriteOptions) (err error) {
	if output == nil {
		return errors.New("bundle output is required")
	}
	canonical, err := opts.Manifest.CanonicalJSON()
	if err != nil {
		return fmt.Errorf("validate release manifest: %w", err)
	}
	manifestDigest := digestBytes(canonical)

	epoch := opts.SourceDateEpoch
	if epoch.IsZero() {
		epoch = opts.Manifest.GeneratedAt
	}
	if epoch.IsZero() {
		return errors.New("source date epoch is required")
	}
	epoch = epoch.UTC().Truncate(time.Second)

	sources := append([]Source(nil), opts.Sources...)
	sort.Slice(sources, func(i, j int) bool { return sources[i].Path < sources[j].Path })
	seen := map[string]struct{}{
		ManifestPath: {},
		IndexPath:    {},
	}
	type expectedSource struct {
		size   int64
		digest string
	}
	expectedArtifacts := make(map[string]expectedSource)
	for _, artifact := range opts.Manifest.Artifacts {
		if artifact.BundlePath != "" {
			expectedArtifacts[artifact.BundlePath] = expectedSource{
				size: artifact.Size, digest: artifact.Digest,
			}
		}
	}
	for _, record := range opts.Manifest.OCIRecords {
		expectedArtifacts[record.BundlePath] = expectedSource{
			size: record.Size, digest: record.Digest,
		}
	}
	for i := range sources {
		clean, cleanErr := cleanBundlePath(sources[i].Path)
		if cleanErr != nil {
			return fmt.Errorf("source[%d].path: %w", i, cleanErr)
		}
		if clean != sources[i].Path {
			return fmt.Errorf("source[%d].path must be canonical", i)
		}
		if isReservedPath(clean) {
			return fmt.Errorf("source path %q is reserved", clean)
		}
		if _, exists := seen[clean]; exists {
			return fmt.Errorf("duplicate source path %q", clean)
		}
		seen[clean] = struct{}{}
		if sources[i].Size < 0 {
			return fmt.Errorf("source %q has negative size", clean)
		}
		if err := validateDigest("source "+clean+" digest", sources[i].Digest); err != nil {
			return err
		}
		if sources[i].Open == nil {
			return fmt.Errorf("source %q has no opener", clean)
		}
		artifact, declared := expectedArtifacts[clean]
		if !declared {
			return fmt.Errorf("source %q is not declared by the release manifest", clean)
		}
		if artifact.size != sources[i].Size || artifact.digest != sources[i].Digest {
			return fmt.Errorf("source %q does not match release artifact size/digest", clean)
		}
		delete(expectedArtifacts, clean)
	}
	if len(expectedArtifacts) != 0 {
		missing := make([]string, 0, len(expectedArtifacts))
		for name := range expectedArtifacts {
			missing = append(missing, name)
		}
		sort.Strings(missing)
		return fmt.Errorf("bundle sources missing declared artifacts: %s", strings.Join(missing, ", "))
	}

	var signatureBytes []byte
	if opts.Signer != nil {
		signature, signErr := opts.Signer.SignManifest(ctx, canonical)
		if signErr != nil {
			return fmt.Errorf("sign release manifest: %w", signErr)
		}
		if signature.ManifestDigest != manifestDigest {
			return errors.New("signer returned a mismatched manifest digest")
		}
		signatureBytes, err = json.Marshal(signature)
		if err != nil {
			return fmt.Errorf("encode release signature: %w", err)
		}
		seen[SignaturePath] = struct{}{}
	}

	encoder, err := zstd.NewWriter(
		output,
		zstd.WithEncoderConcurrency(1),
		zstd.WithEncoderLevel(zstd.SpeedBetterCompression),
		zstd.WithZeroFrames(false),
	)
	if err != nil {
		return fmt.Errorf("create zstd encoder: %w", err)
	}
	tw := tar.NewWriter(encoder)
	defer func() {
		if closeErr := tw.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close tar writer: %w", closeErr)
		}
		if closeErr := encoder.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close zstd encoder: %w", closeErr)
		}
	}()

	records := make([]FileRecord, 0, len(sources)+2)
	if err = writeBytes(tw, ManifestPath, canonical, epoch); err != nil {
		return err
	}
	records = append(records, FileRecord{
		Path: ManifestPath, Size: int64(len(canonical)), Digest: manifestDigest,
	})
	if signatureBytes != nil {
		if err = writeBytes(tw, SignaturePath, signatureBytes, epoch); err != nil {
			return err
		}
		records = append(records, FileRecord{
			Path: SignaturePath, Size: int64(len(signatureBytes)), Digest: digestBytes(signatureBytes),
		})
	}
	for _, source := range sources {
		if err = writeSource(ctx, tw, source, epoch); err != nil {
			return err
		}
		records = append(records, FileRecord{
			Path: source.Path, Size: source.Size, Digest: source.Digest,
		})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
	schemaVersion := opts.SchemaVersion
	if schemaVersion == "" {
		schemaVersion = BundleSchema
	}
	if schemaVersion != BundleSchema && schemaVersion != BundleSchemaV2 {
		return fmt.Errorf("unsupported bundle schema %q", schemaVersion)
	}
	index := Index{
		SchemaVersion:  schemaVersion,
		ReleaseID:      opts.Manifest.ReleaseID,
		ManifestDigest: manifestDigest,
		CreatedAt:      epoch,
		Files:          records,
	}
	indexBytes, err := json.Marshal(index)
	if err != nil {
		return fmt.Errorf("encode bundle index: %w", err)
	}
	if err = writeBytes(tw, IndexPath, indexBytes, epoch); err != nil {
		return err
	}
	return nil
}

func writeBytes(tw *tar.Writer, name string, value []byte, epoch time.Time) error {
	header := deterministicHeader(name, int64(len(value)), epoch)
	if err := tw.WriteHeader(&header); err != nil {
		return fmt.Errorf("write %s header: %w", name, err)
	}
	if _, err := tw.Write(value); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

func writeSource(ctx context.Context, tw *tar.Writer, source Source, epoch time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	reader, err := source.Open()
	if err != nil {
		return fmt.Errorf("open bundle source %q: %w", source.Path, err)
	}
	defer func() { _ = reader.Close() }()
	header := deterministicHeader(source.Path, source.Size, epoch)
	if err := tw.WriteHeader(&header); err != nil {
		return fmt.Errorf("write %s header: %w", source.Path, err)
	}
	hash := sha256.New()
	written, err := io.CopyN(io.MultiWriter(tw, hash), &contextReader{ctx: ctx, reader: reader}, source.Size)
	if err != nil {
		return fmt.Errorf("write bundle source %q after %d bytes: %w", source.Path, written, err)
	}
	var extra [1]byte
	n, extraErr := io.ReadAtLeast(reader, extra[:], 1)
	if n != 0 || (extraErr != nil && !errors.Is(extraErr, io.EOF)) {
		return fmt.Errorf("bundle source %q contains more than declared size %d", source.Path, source.Size)
	}
	actual := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if actual != source.Digest {
		return fmt.Errorf("bundle source %q digest mismatch: got %s, want %s", source.Path, actual, source.Digest)
	}
	return nil
}

func deterministicHeader(name string, size int64, epoch time.Time) tar.Header {
	return tar.Header{
		Typeflag:   tar.TypeReg,
		Name:       name,
		Mode:       0o644,
		Size:       size,
		ModTime:    epoch,
		AccessTime: time.Time{},
		ChangeTime: time.Time{},
		Uid:        0,
		Gid:        0,
		Uname:      "",
		Gname:      "",
		Format:     tar.FormatPAX,
	}
}

// Read verifies and extracts a tar.zst bundle into an empty destination.
// Callers must not import any extracted OCI content until this function
// returns successfully.
func Read(ctx context.Context, input io.Reader, destination string, opts ReadOptions) (*ImportedBundle, error) {
	if input == nil {
		return nil, errors.New("bundle input is required")
	}
	root, err := prepareDestination(destination)
	if err != nil {
		return nil, err
	}
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = defaultMaxFiles
	}
	if opts.MaxFileBytes <= 0 {
		opts.MaxFileBytes = defaultMaxFileBytes
	}
	if opts.MaxTotalBytes <= 0 {
		opts.MaxTotalBytes = defaultMaxTotalBytes
	}

	decoder, err := zstd.NewReader(
		input,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(decoderMemoryLimit(opts.MaxFileBytes)),
	)
	if err != nil {
		return nil, fmt.Errorf("create zstd decoder: %w", err)
	}
	defer decoder.Close()
	tr := tar.NewReader(decoder)
	actual := make(map[string]FileRecord)
	total := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nil, fmt.Errorf("read bundle archive: %w", nextErr)
		}
		if header.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("bundle entry %q has forbidden type %d", header.Name, header.Typeflag)
		}
		clean, cleanErr := cleanBundlePath(header.Name)
		if cleanErr != nil || clean != header.Name {
			if cleanErr == nil {
				cleanErr = errors.New("path is not canonical")
			}
			return nil, fmt.Errorf("unsafe bundle entry %q: %w", header.Name, cleanErr)
		}
		if _, exists := actual[clean]; exists {
			return nil, fmt.Errorf("duplicate bundle entry %q", clean)
		}
		if len(actual)+1 > opts.MaxFiles {
			return nil, fmt.Errorf("bundle exceeds maximum file count %d", opts.MaxFiles)
		}
		if header.Size < 0 || header.Size > opts.MaxFileBytes {
			return nil, fmt.Errorf("bundle entry %q exceeds maximum size %d", clean, opts.MaxFileBytes)
		}
		if header.Size > opts.MaxTotalBytes-total {
			return nil, fmt.Errorf("bundle exceeds maximum total size %d", opts.MaxTotalBytes)
		}
		total += header.Size
		fullPath, joinErr := secureCreatePath(root, clean)
		if joinErr != nil {
			return nil, fmt.Errorf("prepare bundle entry %q: %w", clean, joinErr)
		}
		file, openErr := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if openErr != nil {
			return nil, fmt.Errorf("create bundle entry %q: %w", clean, openErr)
		}
		hash := sha256.New()
		written, copyErr := io.CopyN(io.MultiWriter(file, hash), &contextReader{ctx: ctx, reader: tr}, header.Size)
		closeErr := file.Close()
		if copyErr != nil {
			return nil, fmt.Errorf("extract bundle entry %q after %d bytes: %w", clean, written, copyErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close bundle entry %q: %w", clean, closeErr)
		}
		actual[clean] = FileRecord{
			Path: clean, Size: header.Size, Digest: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
		}
	}

	result, err := validateExtracted(ctx, root, actual, opts)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func decoderMemoryLimit(maxFileBytes int64) uint64 {
	if maxFileBytes < minDecoderMemory {
		return minDecoderMemory
	}
	if maxFileBytes > maxDecoderMemory {
		return maxDecoderMemory
	}
	// #nosec G115 -- this branch is clamped to positive decoder limit constants.
	return uint64(maxFileBytes)
}

func validateExtracted(ctx context.Context, root string, actual map[string]FileRecord, opts ReadOptions) (*ImportedBundle, error) {
	_, ok := actual[IndexPath]
	if !ok {
		return nil, errors.New("bundle index is missing")
	}
	indexBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(IndexPath)))
	if err != nil {
		return nil, fmt.Errorf("read bundle index: %w", err)
	}
	var index Index
	if err := decodeStrict(indexBytes, &index); err != nil {
		return nil, fmt.Errorf("decode bundle index: %w", err)
	}
	if index.SchemaVersion != BundleSchema && index.SchemaVersion != BundleSchemaV2 {
		return nil, fmt.Errorf("unsupported bundle schema %q", index.SchemaVersion)
	}
	if index.CreatedAt.IsZero() || index.CreatedAt.Location() != time.UTC {
		return nil, errors.New("bundle index created_at must be UTC")
	}
	if len(index.Files)+1 != len(actual) {
		return nil, errors.New("bundle index does not describe every archive entry")
	}
	indexed := make(map[string]FileRecord, len(index.Files))
	for _, record := range index.Files {
		clean, cleanErr := cleanBundlePath(record.Path)
		if cleanErr != nil || clean != record.Path || record.Path == IndexPath {
			return nil, fmt.Errorf("bundle index contains unsafe path %q", record.Path)
		}
		if _, duplicate := indexed[record.Path]; duplicate {
			return nil, fmt.Errorf("bundle index contains duplicate path %q", record.Path)
		}
		if err := validateDigest("bundle index digest", record.Digest); err != nil {
			return nil, err
		}
		got, exists := actual[record.Path]
		if !exists || got.Size != record.Size || got.Digest != record.Digest {
			return nil, fmt.Errorf("bundle entry %q does not match its index record", record.Path)
		}
		indexed[record.Path] = record
	}
	delete(actual, IndexPath)

	manifestRecord, ok := indexed[ManifestPath]
	if !ok {
		return nil, errors.New("release manifest is missing")
	}
	manifestBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ManifestPath)))
	if err != nil {
		return nil, fmt.Errorf("read release manifest: %w", err)
	}
	var manifest ReleaseManifest
	if err := decodeStrict(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("decode release manifest: %w", err)
	}
	canonical, err := manifest.CanonicalJSON()
	if err != nil {
		return nil, fmt.Errorf("validate release manifest: %w", err)
	}
	if !bytes.Equal(canonical, manifestBytes) {
		return nil, errors.New("release manifest is not in canonical form")
	}
	manifestDigest := digestBytes(canonical)
	if manifestDigest != manifestRecord.Digest || manifestDigest != index.ManifestDigest {
		return nil, errors.New("release manifest digest does not match bundle index")
	}
	if index.ReleaseID != manifest.ReleaseID {
		return nil, errors.New("bundle index release ID does not match release manifest")
	}
	for _, artifact := range manifest.Artifacts {
		if artifact.BundlePath == "" {
			continue
		}
		record, exists := indexed[artifact.BundlePath]
		if !exists || record.Size != artifact.Size || record.Digest != artifact.Digest {
			return nil, fmt.Errorf("release artifact %q is missing or does not match the manifest", artifact.Key)
		}
	}
	for _, record := range manifest.OCIRecords {
		indexRecord, exists := indexed[record.BundlePath]
		if !exists || indexRecord.Size != record.Size ||
			indexRecord.Digest != record.Digest {
			return nil, fmt.Errorf("OCI record %s is missing or does not match the manifest", record.Digest)
		}
	}
	if index.SchemaVersion == BundleSchemaV2 {
		if err := validateOCIClosure(root, manifest, indexed); err != nil {
			return nil, err
		}
	}

	var signature *Signature
	if record, exists := indexed[SignaturePath]; exists {
		signatureBytes, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(SignaturePath)))
		if readErr != nil {
			return nil, fmt.Errorf("read release signature: %w", readErr)
		}
		if digestBytes(signatureBytes) != record.Digest {
			return nil, errors.New("release signature digest does not match bundle index")
		}
		var decoded Signature
		if err := decodeStrict(signatureBytes, &decoded); err != nil {
			return nil, fmt.Errorf("decode release signature: %w", err)
		}
		signature = &decoded
	}
	if !opts.AllowUnsigned {
		if signature == nil {
			return nil, errors.New("signed release bundle is required")
		}
		if opts.Verifier == nil {
			return nil, errors.New("release signature verifier is required")
		}
		if err := opts.Verifier.VerifyManifest(ctx, canonical, *signature); err != nil {
			return nil, fmt.Errorf("verify release signature: %w", err)
		}
	}

	return &ImportedBundle{
		Root:           root,
		Manifest:       manifest,
		ManifestDigest: manifestDigest,
		Signature:      signature,
		Files:          indexed,
		SchemaVersion:  index.SchemaVersion,
	}, nil
}

func validateOCIClosure(
	root string,
	manifest ReleaseManifest,
	indexed map[string]FileRecord,
) error {
	records := make(map[string]OCIRecord, len(manifest.OCIRecords))
	for _, record := range manifest.OCIRecords {
		records[record.Digest] = record
	}
	for _, image := range manifest.Images {
		if len(image.BlobDigests) == 0 {
			return fmt.Errorf("v2 image %q has no OCI blob closure", image.Key)
		}
		allowed := make(map[string]struct{}, len(image.BlobDigests))
		for _, value := range image.BlobDigests {
			allowed[value] = struct{}{}
			record, exists := records[value]
			if !exists || record.BundlePath != OCIPath(value) {
				return fmt.Errorf("v2 image %q OCI blob %s is missing", image.Key, value)
			}
			indexRecord, exists := indexed[record.BundlePath]
			if !exists || indexRecord.Digest != value || indexRecord.Size != record.Size {
				return fmt.Errorf("v2 OCI blob %s does not match its signed record", value)
			}
		}
		if _, exists := allowed[image.Digest]; !exists {
			return fmt.Errorf("v2 image %q root digest is outside its OCI closure", image.Key)
		}
		visited := make(map[string]struct{})
		if err := walkOCIDescriptors(root, image.Digest, nil, allowed, records, visited); err != nil {
			return fmt.Errorf("v2 image %q closure: %w", image.Key, err)
		}
		if err := validateOCIPlatformMapping(root, image, records); err != nil {
			return fmt.Errorf("v2 image %q platforms: %w", image.Key, err)
		}
		validSubjects := map[string]struct{}{image.Digest: {}}
		if image.SourceDigest != "" {
			validSubjects[image.SourceDigest] = struct{}{}
		}
		for _, digest := range image.Platforms {
			validSubjects[digest] = struct{}{}
		}
		for _, value := range image.BlobDigests {
			record := records[value]
			if strings.Contains(record.Kind, "manifest") ||
				strings.Contains(record.Kind, "index") {
				if err := walkOCIDescriptors(
					root, value, nil, allowed, records, visited,
				); err != nil {
					return fmt.Errorf("v2 image %q trust closure: %w", image.Key, err)
				}
				if record.Kind == "oci-trust-manifest" {
					subject, err := ociManifestSubject(root, record)
					if err != nil {
						return fmt.Errorf("v2 image %q trust subject: %w", image.Key, err)
					}
					if _, ok := validSubjects[subject]; !ok {
						return fmt.Errorf("v2 image %q trust manifest %s has unrelated subject %s", image.Key, value, subject)
					}
				}
			}
		}
		for platform, digest := range image.Platforms {
			if _, exists := visited[digest]; !exists {
				return fmt.Errorf("v2 image %q platform %s is outside the reachable closure", image.Key, platform)
			}
		}
		for _, value := range image.BlobDigests {
			if _, exists := visited[value]; !exists {
				return fmt.Errorf("v2 image %q OCI blob %s is not reachable from an image or trust root", image.Key, value)
			}
		}
	}
	for _, artifact := range manifest.Artifacts {
		lowerType := strings.ToLower(artifact.Type)
		if (strings.Contains(lowerType, "signature") ||
			strings.Contains(lowerType, "provenance") ||
			strings.Contains(lowerType, "sbom") ||
			strings.Contains(lowerType, "trust")) &&
			artifact.BundlePath == "" {
			return fmt.Errorf("v2 trust artifact %q is not embedded", artifact.Key)
		}
		if artifact.StorageDigest == "" {
			continue
		}
		allowed := make(map[string]struct{}, len(artifact.OCIClosure))
		for _, digest := range artifact.OCIClosure {
			allowed[digest] = struct{}{}
			record, exists := records[digest]
			if !exists {
				return fmt.Errorf("v2 artifact %q OCI record %s is missing", artifact.Key, digest)
			}
			indexRecord, exists := indexed[record.BundlePath]
			if !exists || indexRecord.Digest != digest || indexRecord.Size != record.Size {
				return fmt.Errorf("v2 artifact %q OCI record %s does not match the bundle index", artifact.Key, digest)
			}
		}
		storage := records[artifact.StorageDigest]
		if storage.Size != artifact.StorageSize || storage.MediaType != artifact.StorageMediaType {
			return fmt.Errorf("v2 artifact %q storage descriptor does not match its signed record", artifact.Key)
		}
		payload := records[artifact.Digest]
		if payload.Size != artifact.Size || payload.MediaType != artifact.MediaType ||
			artifact.BundlePath != payload.BundlePath {
			return fmt.Errorf("v2 artifact %q payload descriptor does not match its signed record", artifact.Key)
		}
		visited := make(map[string]struct{})
		if err := walkOCIDescriptors(root, artifact.StorageDigest, nil, allowed, records, visited); err != nil {
			return fmt.Errorf("v2 artifact %q closure: %w", artifact.Key, err)
		}
		if _, exists := visited[artifact.Digest]; !exists {
			return fmt.Errorf("v2 artifact %q payload is unreachable from its storage root", artifact.Key)
		}
		for _, digest := range artifact.OCIClosure {
			if _, exists := visited[digest]; !exists {
				return fmt.Errorf("v2 artifact %q contains unreachable OCI record %s", artifact.Key, digest)
			}
		}
	}
	return nil
}

func walkOCIDescriptors(
	root, current string,
	expected *ociDescriptor,
	allowed map[string]struct{},
	records map[string]OCIRecord,
	visited map[string]struct{},
) error {
	if _, ok := visited[current]; ok {
		return nil
	}
	if _, ok := allowed[current]; !ok {
		return fmt.Errorf("descriptor %s is outside the declared closure", current)
	}
	record, exists := records[current]
	if !exists {
		return fmt.Errorf("descriptor %s has no signed OCI record", current)
	}
	if expected != nil && (record.Size != expected.Size || record.MediaType != expected.MediaType) {
		return fmt.Errorf("descriptor %s size or media type does not match its signed OCI record", current)
	}
	visited[current] = struct{}{}
	if !strings.Contains(record.Kind, "manifest") &&
		!strings.Contains(record.Kind, "index") {
		return nil
	}
	value, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(record.BundlePath)))
	if err != nil {
		return err
	}
	var document ociDocument
	if err := json.Unmarshal(value, &document); err != nil {
		return fmt.Errorf("decode OCI JSON %s: %w", current, err)
	}
	if document.SchemaVersion != 2 || document.MediaType != record.MediaType {
		return fmt.Errorf("OCI JSON %s schema or media type does not match its signed OCI record", current)
	}
	// A platform-selective bundle preserves the original source index as
	// signed identity evidence, but intentionally does not include manifests
	// for unselected platforms reachable only from that original index.
	if record.Kind == "oci-source-index" {
		return nil
	}
	children := make([]ociDescriptor, 0, len(document.Manifests)+len(document.Layers)+2)
	for _, descriptor := range document.Manifests {
		children = append(children, descriptor)
	}
	if document.Config.Digest != "" {
		children = append(children, document.Config)
	}
	for _, descriptor := range document.Layers {
		children = append(children, descriptor)
	}
	if document.Subject != nil && document.Subject.Digest != "" {
		// OCI subjects are references, not owned content. Validate the exact
		// signed descriptor when the subject is embedded elsewhere, but do not
		// require it to belong to this artifact's local transfer closure.
		if err := validateOCIDescriptor(*document.Subject); err != nil {
			return err
		}
		if subject, exists := records[document.Subject.Digest]; exists &&
			(subject.Size != document.Subject.Size || subject.MediaType != document.Subject.MediaType) {
			return fmt.Errorf("OCI subject %s does not match its signed OCI record", document.Subject.Digest)
		}
	}
	for _, child := range children {
		if err := validateOCIDescriptor(child); err != nil {
			return err
		}
		if err := walkOCIDescriptors(root, child.Digest, &child, allowed, records, visited); err != nil {
			return err
		}
	}
	return nil
}

type ociDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Platform  *struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
		Variant      string `json:"variant,omitempty"`
	} `json:"platform,omitempty"`
}

type ociDocument struct {
	SchemaVersion int             `json:"schemaVersion"`
	MediaType     string          `json:"mediaType"`
	Manifests     []ociDescriptor `json:"manifests"`
	Config        ociDescriptor   `json:"config"`
	Layers        []ociDescriptor `json:"layers"`
	Subject       *ociDescriptor  `json:"subject,omitempty"`
}

func validateOCIDescriptor(descriptor ociDescriptor) error {
	if err := validateDigest("OCI descriptor digest", descriptor.Digest); err != nil {
		return err
	}
	if descriptor.Size < 0 || strings.TrimSpace(descriptor.MediaType) == "" {
		return errors.New("OCI descriptor has an invalid size or media type")
	}
	return nil
}

func validateOCIPlatformMapping(root string, image ReleaseImage, records map[string]OCIRecord) error {
	record := records[image.Digest]
	value, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(record.BundlePath)))
	if err != nil {
		return err
	}
	var document ociDocument
	if err := json.Unmarshal(value, &document); err != nil {
		return err
	}
	if len(document.Manifests) == 0 {
		if len(image.Platforms) != 1 {
			return errors.New("single-manifest image must declare exactly one platform")
		}
		for platform, digest := range image.Platforms {
			if digest != image.Digest {
				return fmt.Errorf("platform %s does not resolve to the root manifest", platform)
			}
			return validateOCIConfigPlatform(root, document.Config, platform, records)
		}
	}
	if len(document.Manifests) != len(image.Platforms) {
		return errors.New("image index platform count does not match the signed platform map")
	}
	seen := make(map[string]struct{}, len(document.Manifests))
	for _, descriptor := range document.Manifests {
		if descriptor.Platform == nil {
			return errors.New("image index manifest has no platform")
		}
		platform := descriptor.Platform.OS + "/" + descriptor.Platform.Architecture
		if descriptor.Platform.Variant != "" {
			platform += "/" + descriptor.Platform.Variant
		}
		if platform == "/" || image.Platforms[platform] != descriptor.Digest {
			return fmt.Errorf("platform %s descriptor does not match the signed platform map", platform)
		}
		if _, duplicate := seen[platform]; duplicate {
			return fmt.Errorf("platform %s is duplicated", platform)
		}
		seen[platform] = struct{}{}
		childRecord := records[descriptor.Digest]
		child, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(childRecord.BundlePath)))
		if err != nil {
			return err
		}
		var childDocument ociDocument
		if err := json.Unmarshal(child, &childDocument); err != nil {
			return err
		}
		if err := validateOCIConfigPlatform(root, childDocument.Config, platform, records); err != nil {
			return err
		}
	}
	return nil
}

func validateOCIConfigPlatform(root string, descriptor ociDescriptor, platform string, records map[string]OCIRecord) error {
	if err := validateOCIDescriptor(descriptor); err != nil {
		return err
	}
	record, exists := records[descriptor.Digest]
	if !exists || record.Size != descriptor.Size || record.MediaType != descriptor.MediaType {
		return errors.New("image config descriptor does not match its signed OCI record")
	}
	value, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(record.BundlePath)))
	if err != nil {
		return err
	}
	var config struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
		Variant      string `json:"variant,omitempty"`
	}
	if err := json.Unmarshal(value, &config); err != nil {
		return fmt.Errorf("decode OCI image config: %w", err)
	}
	actual := config.OS + "/" + config.Architecture
	if config.Variant != "" {
		actual += "/" + config.Variant
	}
	if actual != platform {
		return fmt.Errorf("image config platform %s does not match %s", actual, platform)
	}
	return nil
}

func ociManifestSubject(root string, record OCIRecord) (string, error) {
	value, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(record.BundlePath)))
	if err != nil {
		return "", err
	}
	var document ociDocument
	if err := json.Unmarshal(value, &document); err != nil {
		return "", err
	}
	if document.Subject == nil {
		return "", errors.New("trust manifest has no subject")
	}
	if err := validateOCIDescriptor(*document.Subject); err != nil {
		return "", err
	}
	return document.Subject.Digest, nil
}

func OCIPath(digest string) string {
	return "oci/blobs/sha256/" + strings.TrimPrefix(digest, "sha256:")
}

func prepareDestination(destination string) (string, error) {
	if strings.TrimSpace(destination) == "" {
		return "", errors.New("bundle destination is required")
	}
	root, err := filepath.Abs(destination)
	if err != nil {
		return "", fmt.Errorf("resolve bundle destination: %w", err)
	}
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(root, 0o700); err != nil {
			return "", fmt.Errorf("create bundle destination: %w", err)
		}
		info, err = os.Lstat(root)
	}
	if err != nil {
		return "", fmt.Errorf("inspect bundle destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("bundle destination must be a real directory")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("read bundle destination: %w", err)
	}
	if len(entries) != 0 {
		return "", errors.New("bundle destination must be empty")
	}
	return root, nil
}

func secureCreatePath(root, slashPath string) (string, error) {
	parts := strings.Split(slashPath, "/")
	current := root
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if err := os.Mkdir(current, 0o700); err != nil {
				return "", err
			}
		case err != nil:
			return "", err
		case info.Mode()&os.ModeSymlink != 0 || !info.IsDir():
			return "", fmt.Errorf("parent %q is not a real directory", part)
		}
	}
	fullPath := filepath.Join(root, filepath.FromSlash(slashPath))
	relative, err := filepath.Rel(root, fullPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("resolved path escapes bundle destination")
	}
	return fullPath, nil
}

func decodeStrict(value []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("unexpected trailing JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON")
		}
		return err
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
