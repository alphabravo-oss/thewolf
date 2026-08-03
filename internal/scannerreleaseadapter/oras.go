package scannerreleaseadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/scannerreleasebackend"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworkspace"
)

const (
	ociManifestMediaType = "application/vnd.oci.image.manifest.v1+json"
	ociEmptyMediaType    = "application/vnd.oci.empty.v1+json"
)

type orasPublisher struct {
	Path          string
	CredentialDir string
	ScratchRoot   string
	// execute is test-only command injection. Production always leaves it nil
	// and executes the compiled ORAS command forms below.
	execute func(context.Context, string, ...string) ([]byte, []byte, error)
}

func ProductionRunner(lane scannerreleasebackend.AdapterLane) (Runner, error) {
	if _, err := scannerreleasebackend.AdapterActionPatterns(lane); err != nil {
		return Runner{}, err
	}
	credentialDir := strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_REGISTRY_CREDENTIAL_DIR"))
	if !filepath.IsAbs(credentialDir) {
		return Runner{}, errors.New("absolute WOLF_SCANNER_RELEASE_REGISTRY_CREDENTIAL_DIR is required")
	}
	scratch := strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_SCRATCH_DIR"))
	if scratch == "" {
		scratch = "/work"
	}
	if !filepath.IsAbs(scratch) {
		return Runner{}, errors.New("absolute WOLF_SCANNER_RELEASE_SCRATCH_DIR is required")
	}
	return Runner{
		Lane: lane, Actions: productionActions{},
		Publisher: orasPublisher{Path: orasPath, CredentialDir: credentialDir, ScratchRoot: scratch},
	}, nil
}

func (p orasPublisher) Publish(ctx context.Context, request PublishRequest) (PublishedArtifact, error) {
	if !filepath.IsAbs(p.Path) || !filepath.IsAbs(p.CredentialDir) || !filepath.IsAbs(p.ScratchRoot) ||
		!filepath.IsAbs(request.Workspace) || !digest(request.OperationID) ||
		request.Action == "" || request.CommandID == "" || len(request.Payload) == 0 ||
		request.MediaType == "" {
		return PublishedArtifact{}, errors.New("OCI adapter publisher configuration or request is invalid")
	}
	credentialFile, err := dockerCredentialFile(p.CredentialDir)
	if err != nil {
		return PublishedArtifact{}, err
	}
	execution, err := scannerreleaseworkspace.ReadContext(request.Workspace)
	if err != nil {
		return PublishedArtifact{}, fmt.Errorf("read adapter registry context: %w", err)
	}
	repository := execution.Primary.Host + "/" + path.Join(
		strings.Trim(execution.Primary.Repository, "/"), "wolf-release-evidence",
	)
	// OCI 1.1 referrers must be stored in the subject repository. Ordinary
	// evidence uses the dedicated release-evidence repository.
	if request.SubjectURI != "" {
		subjectReference := strings.TrimPrefix(request.SubjectURI, "oci://")
		var found bool
		repository, _, found = strings.Cut(subjectReference, "@")
		if !found {
			return PublishedArtifact{}, errors.New("adapter referrer subject has no digest separator")
		}
	}
	if strings.ContainsAny(repository, " \t\r\n") || strings.Contains(repository, "..") {
		return PublishedArtifact{}, errors.New("adapter evidence repository is invalid")
	}
	directory, err := os.MkdirTemp(p.ScratchRoot, "wolf-release-evidence-")
	if err != nil {
		return PublishedArtifact{}, err
	}
	defer os.RemoveAll(directory)
	payloadPath := filepath.Join(directory, "evidence")
	configPath := filepath.Join(directory, "config.json")
	manifestPath := filepath.Join(directory, "manifest.json")
	readbackPath := filepath.Join(directory, "readback")
	if err := os.WriteFile(payloadPath, request.Payload, 0o600); err != nil {
		return PublishedArtifact{}, err
	}
	config := []byte("{}")
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		return PublishedArtifact{}, err
	}
	payloadDigest := sha256Digest(request.Payload)
	configDigest := sha256Digest(config)
	subject, err := p.subjectDescriptor(ctx, credentialFile, request)
	if err != nil {
		return PublishedArtifact{}, err
	}
	manifest := ociArtifactManifest{
		SchemaVersion: 2, MediaType: ociManifestMediaType,
		ArtifactType: request.MediaType,
		Config:       ociDescriptor{MediaType: ociEmptyMediaType, Digest: configDigest, Size: int64(len(config))},
		Layers:       []ociDescriptor{{MediaType: request.MediaType, Digest: payloadDigest, Size: int64(len(request.Payload))}},
		Subject:      subject,
		Annotations: map[string]string{
			"dev.wolf.operation-id": request.OperationID,
			"dev.wolf.action":       request.Action,
			"dev.wolf.command-id":   request.CommandID,
		},
	}
	manifestValue, err := json.Marshal(manifest)
	if err != nil {
		return PublishedArtifact{}, err
	}
	manifestDigest := sha256Digest(manifestValue)
	if err := os.WriteFile(manifestPath, manifestValue, 0o600); err != nil {
		return PublishedArtifact{}, err
	}
	operationReference := repository + ":wolf-op-" + strings.TrimPrefix(request.OperationID, "sha256:")
	if existing, found, err := p.readOperationArtifact(
		ctx, credentialFile, operationReference, repository, manifestValue,
		manifestDigest, payloadDigest, request,
	); err != nil {
		return PublishedArtifact{}, err
	} else if found {
		return existing, nil
	}
	for _, blob := range []struct {
		file, mediaType, digest string
	}{
		{configPath, ociEmptyMediaType, configDigest},
		{payloadPath, request.MediaType, payloadDigest},
	} {
		if _, err := p.run(ctx, credentialFile,
			"blob", "push", "--media-type", blob.mediaType,
			repository+"@"+blob.digest, blob.file,
		); err != nil {
			return PublishedArtifact{}, fmt.Errorf("push adapter evidence blob: %w", err)
		}
	}
	if _, err := p.run(ctx, credentialFile,
		"manifest", "push", "--media-type", ociManifestMediaType,
		repository+"@"+manifestDigest, manifestPath,
	); err != nil {
		return PublishedArtifact{}, fmt.Errorf("push adapter evidence manifest: %w", err)
	}
	// The operation alias is the externally queryable recovery pointer. The
	// immutable action journal guarantees that retries for this operation
	// reconstruct byte-identical manifest and payload bytes.
	if _, err := p.run(ctx, credentialFile,
		"manifest", "push", "--media-type", ociManifestMediaType,
		operationReference, manifestPath,
	); err != nil {
		return PublishedArtifact{}, fmt.Errorf("commit adapter evidence operation alias: %w", err)
	}
	reference := repository + "@" + manifestDigest
	descriptorValue, err := p.run(ctx, credentialFile,
		"manifest", "fetch", "--descriptor", reference,
	)
	if err != nil {
		return PublishedArtifact{}, fmt.Errorf("read back adapter evidence manifest: %w", err)
	}
	var descriptor ociDescriptor
	if err := decodeStrictJSON(descriptorValue, &descriptor); err != nil ||
		descriptor.Digest != manifestDigest || descriptor.MediaType != ociManifestMediaType ||
		descriptor.Size != int64(len(manifestValue)) {
		return PublishedArtifact{}, errors.New("adapter evidence manifest readback mismatch")
	}
	if _, err := p.run(ctx, credentialFile,
		"blob", "fetch", "--output", readbackPath,
		repository+"@"+payloadDigest,
	); err != nil {
		return PublishedArtifact{}, fmt.Errorf("read back adapter evidence payload: %w", err)
	}
	readback, err := os.ReadFile(readbackPath)
	if err != nil || !bytes.Equal(readback, request.Payload) {
		return PublishedArtifact{}, errors.New("adapter evidence payload readback mismatch")
	}
	aliasDescriptorValue, err := p.run(
		ctx, credentialFile, "manifest", "fetch", "--descriptor", operationReference,
	)
	var aliasDescriptor ociDescriptor
	if err != nil || decodeStrictJSON(aliasDescriptorValue, &aliasDescriptor) != nil ||
		aliasDescriptor.Digest != manifestDigest || aliasDescriptor.MediaType != ociManifestMediaType ||
		aliasDescriptor.Size != int64(len(manifestValue)) {
		return PublishedArtifact{}, errors.New("adapter evidence operation alias readback mismatch")
	}
	return PublishedArtifact{
		URI: "oci://" + reference, Digest: manifestDigest,
		PayloadDigest: payloadDigest, MediaType: request.MediaType,
		SizeBytes: int64(len(request.Payload)), StorageMediaType: ociManifestMediaType,
		StorageSizeBytes: int64(len(manifestValue)), ReadBackVerified: true,
	}, nil
}

func (p orasPublisher) readOperationArtifact(
	ctx context.Context,
	credentialFile, operationReference, repository string,
	expectedManifest []byte,
	manifestDigest, payloadDigest string,
	request PublishRequest,
) (PublishedArtifact, bool, error) {
	descriptorValue, found, err := p.runOptionalManifest(
		ctx, credentialFile, "manifest", "fetch", "--descriptor", operationReference,
	)
	if err != nil || !found {
		return PublishedArtifact{}, false, err
	}
	var descriptor ociDescriptor
	if decodeStrictJSON(descriptorValue, &descriptor) != nil ||
		descriptor.Digest != manifestDigest || descriptor.MediaType != ociManifestMediaType ||
		descriptor.Size != int64(len(expectedManifest)) {
		return PublishedArtifact{}, false, errors.New("adapter operation alias is bound to conflicting evidence")
	}
	manifestValue, err := p.run(ctx, credentialFile, "manifest", "fetch", operationReference)
	if err != nil || !bytes.Equal(manifestValue, expectedManifest) || sha256Digest(manifestValue) != manifestDigest {
		return PublishedArtifact{}, false, errors.New("adapter operation alias manifest bytes do not match the request")
	}
	directory, err := os.MkdirTemp(p.ScratchRoot, "wolf-release-recovery-")
	if err != nil {
		return PublishedArtifact{}, false, err
	}
	defer os.RemoveAll(directory)
	payloadPath := filepath.Join(directory, "payload")
	if _, err := p.run(ctx, credentialFile,
		"blob", "fetch", "--output", payloadPath, repository+"@"+payloadDigest,
	); err != nil {
		return PublishedArtifact{}, false, errors.New("adapter operation payload recovery failed")
	}
	payload, err := os.ReadFile(payloadPath)
	if err != nil || !bytes.Equal(payload, request.Payload) || sha256Digest(payload) != payloadDigest {
		return PublishedArtifact{}, false, errors.New("adapter operation payload recovery mismatch")
	}
	return PublishedArtifact{
		URI:    "oci://" + repository + "@" + manifestDigest,
		Digest: manifestDigest, PayloadDigest: payloadDigest, MediaType: request.MediaType,
		SizeBytes: int64(len(request.Payload)), StorageMediaType: ociManifestMediaType,
		StorageSizeBytes: int64(len(expectedManifest)), ReadBackVerified: true,
	}, true, nil
}

type ociArtifactManifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	ArtifactType  string            `json:"artifactType"`
	Config        ociDescriptor     `json:"config"`
	Layers        []ociDescriptor   `json:"layers"`
	Subject       *ociDescriptor    `json:"subject,omitempty"`
	Annotations   map[string]string `json:"annotations"`
}

type ociDescriptor struct {
	MediaType    string `json:"mediaType"`
	Digest       string `json:"digest"`
	Size         int64  `json:"size"`
	ArtifactType string `json:"artifactType,omitempty"`
}

func (p orasPublisher) subjectDescriptor(
	ctx context.Context,
	credentialFile string,
	request PublishRequest,
) (*ociDescriptor, error) {
	if request.SubjectDigest == "" && request.SubjectURI == "" {
		return nil, nil
	}
	if !digest(request.SubjectDigest) || !strings.HasPrefix(request.SubjectURI, "oci://") ||
		!strings.Contains(request.SubjectURI, request.SubjectDigest) {
		return nil, errors.New("adapter OCI referrer subject is invalid")
	}
	reference := strings.TrimPrefix(request.SubjectURI, "oci://")
	value, err := p.run(ctx, credentialFile, "manifest", "fetch", "--descriptor", reference)
	if err != nil {
		return nil, fmt.Errorf("read adapter referrer subject: %w", err)
	}
	var descriptor ociDescriptor
	if err := decodeStrictJSON(value, &descriptor); err != nil ||
		descriptor.Digest != request.SubjectDigest || descriptor.MediaType == "" || descriptor.Size <= 0 {
		return nil, errors.New("adapter referrer subject descriptor mismatch")
	}
	return &descriptor, nil
}

func (p orasPublisher) run(
	ctx context.Context,
	credentialFile string,
	args ...string,
) ([]byte, error) {
	stdout, _, err := p.runStatus(ctx, credentialFile, args...)
	return stdout, err
}

func (p orasPublisher) runOptionalManifest(
	ctx context.Context,
	credentialFile string,
	args ...string,
) ([]byte, bool, error) {
	stdout, stderr, err := p.runStatus(ctx, credentialFile, args...)
	if err == nil {
		return stdout, true, nil
	}
	message := strings.ToLower(string(stderr))
	if strings.Contains(message, "not found") || strings.Contains(message, "manifest unknown") ||
		strings.Contains(message, "404") {
		return nil, false, nil
	}
	return nil, false, err
}

func (p orasPublisher) runStatus(
	ctx context.Context,
	credentialFile string,
	args ...string,
) ([]byte, []byte, error) {
	if len(args) < 2 {
		return nil, nil, errors.New("ORAS command form is incomplete")
	}
	if p.execute != nil {
		return p.execute(ctx, credentialFile, args...)
	}
	commandArgs := append([]string(nil), args[:2]...)
	commandArgs = append(commandArgs, "--registry-config", credentialFile)
	commandArgs = append(commandArgs, args[2:]...)
	command := exec.CommandContext(ctx, p.Path, commandArgs...) // #nosec G204 -- path and command forms are compiled above.
	command.Env = publisherEnvironment()
	var stdout, stderr boundedBuffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if stdout.exceeded || stderr.exceeded {
		return nil, nil, errors.New("ORAS output exceeded its independent size bound")
	}
	if err != nil {
		return stdout.value.Bytes(), stderr.value.Bytes(), fmt.Errorf("ORAS command failed: %w", err)
	}
	return stdout.value.Bytes(), stderr.value.Bytes(), nil
}

func dockerCredentialFile(directory string) (string, error) {
	for _, name := range []string{"config.json", ".dockerconfigjson"} {
		if _, err := readCredentialFile(directory, name, 1<<20); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", errors.New("adapter registry config must be a bounded in-volume regular file")
		}
		return filepath.Join(directory, name), nil
	}
	return "", errors.New("adapter registry credential directory has no config.json or .dockerconfigjson")
}

func publisherEnvironment() []string {
	allowed := map[string]bool{
		"PATH": true, "HOME": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
		"AWS_REGION": true, "AWS_DEFAULT_REGION": true, "AWS_ROLE_ARN": true,
		"AWS_WEB_IDENTITY_TOKEN_FILE": true, "GOOGLE_APPLICATION_CREDENTIALS": true,
		"GOOGLE_CLOUD_PROJECT": true, "AZURE_CLIENT_ID": true, "AZURE_TENANT_ID": true,
		"AZURE_FEDERATED_TOKEN_FILE": true,
	}
	result := make([]string, 0, len(allowed))
	for _, item := range os.Environ() {
		name, _, ok := strings.Cut(item, "=")
		if ok && allowed[name] {
			result = append(result, item)
		}
	}
	sort.Strings(result)
	return result
}

func decodeStrictJSON(value []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON value has trailing content")
		}
		return err
	}
	return nil
}
