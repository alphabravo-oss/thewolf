package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerregistry"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannerreleasebackend"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworkspace"
	"github.com/alphabravocompany/thewolf/internal/scannersigning"
)

type managedReleaseContextProvider struct {
	store     scannerrelease.Persistence
	sourceURL string
	primaryID string
	mirrorID  string
}

func (p managedReleaseContextProvider) ExecutionContext(
	ctx context.Context,
	_ scannerreleaseworker.StepRequest,
) (scannerreleaseworkspace.ExecutionContext, error) {
	if p.store == nil {
		return scannerreleaseworkspace.ExecutionContext{}, errors.New("managed release persistence is required")
	}
	sourceURL, err := managedSourceURL(p.sourceURL)
	if err != nil {
		return scannerreleaseworkspace.ExecutionContext{}, err
	}
	primary, err := p.store.GetRegistryTarget(ctx, p.primaryID)
	if err != nil {
		return scannerreleaseworkspace.ExecutionContext{}, errors.New("managed primary registry target was not found")
	}
	mirror, err := p.store.GetRegistryTarget(ctx, p.mirrorID)
	if err != nil {
		return scannerreleaseworkspace.ExecutionContext{}, errors.New("managed mirror registry target was not found")
	}
	primarySnapshot, err := managedRegistrySnapshot(primary)
	if err != nil {
		return scannerreleaseworkspace.ExecutionContext{}, fmt.Errorf("primary registry: %w", err)
	}
	mirrorSnapshot, err := managedRegistrySnapshot(mirror)
	if err != nil {
		return scannerreleaseworkspace.ExecutionContext{}, fmt.Errorf("mirror registry: %w", err)
	}
	stable, err := managedStableRelease(ctx, p.store, primarySnapshot)
	if err != nil {
		return scannerreleaseworkspace.ExecutionContext{}, err
	}
	return scannerreleaseworkspace.ExecutionContext{
		SourceURL: sourceURL, Primary: primarySnapshot, Mirror: mirrorSnapshot, Stable: stable,
	}, nil
}

func managedStableRelease(
	ctx context.Context,
	store scannerrelease.Persistence,
	primary scannerreleaseworkspace.RegistryTarget,
) (*scannerreleaseworkspace.StableRelease, error) {
	page, err := store.ListReleases(ctx, scannerrelease.ReleaseFilter{
		State: scannerrelease.ReleaseStable,
	}, scannerrelease.PageRequest{Limit: 1})
	if err != nil {
		return nil, errors.New("load stable scanner release baseline")
	}
	if len(page.Items) != 1 {
		return nil, errors.New("managed release requires an imported or previously promoted stable baseline")
	}
	inventory, err := store.GetReleaseInventory(ctx, page.Items[0].ID)
	if err != nil || inventory == nil {
		return nil, errors.New("load stable scanner release inventory")
	}
	stable := &scannerreleaseworkspace.StableRelease{
		ID: inventory.Release.ID, LockDigest: inventory.Release.LockDigest,
		ManifestDigest:   inventory.Release.ManifestDigest,
		DefinitionCommit: inventory.Release.DefinitionCommit,
	}
	imageKeys := make(map[string]bool)
	for _, image := range inventory.Images {
		if image.RegistryTargetID != primary.ID || !scannerrelease.IsRuntimeScannerImage(image) {
			continue
		}
		if imageKeys[image.ImageKey] || !strings.HasPrefix(image.Repository, primary.Host+"/") {
			return nil, errors.New("stable release primary scanner image inventory is ambiguous")
		}
		imageKeys[image.ImageKey] = true
		stable.Images = append(stable.Images, scannerreleaseworkspace.StableImage{
			Key: image.ImageKey, Repository: image.Repository, Digest: image.Digest,
			PlatformDigests: image.PlatformDigests,
		})
	}
	for _, tool := range inventory.Tools {
		var metadata struct {
			ImageKey string `json:"image_key"`
			Kind     string `json:"kind"`
		}
		if json.Unmarshal([]byte(tool.MetadataJSON), &metadata) != nil ||
			!imageKeys[metadata.ImageKey] || (metadata.Kind != "wolf" && metadata.Kind != "upstream") {
			return nil, fmt.Errorf("stable release tool %q metadata is incomplete", tool.ToolKey)
		}
		stable.Tools = append(stable.Tools, scannerreleaseworkspace.StableTool{
			Key: tool.ToolKey, ImageKey: metadata.ImageKey, Kind: metadata.Kind,
			SourceReference: tool.SourceReference, SourceDigest: tool.SourceDigest,
			ParserCompatibility: tool.ParserCompatibility,
		})
	}
	sort.Slice(stable.Images, func(i, j int) bool { return stable.Images[i].Key < stable.Images[j].Key })
	sort.Slice(stable.Tools, func(i, j int) bool { return stable.Tools[i].Key < stable.Tools[j].Key })
	if len(stable.Images) == 0 || len(stable.Tools) == 0 {
		return nil, errors.New("stable release baseline has no primary scanner inventory")
	}
	return stable, nil
}

func managedSourceURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		strings.Trim(parsed.Path, "/") == "" {
		return "", errors.New("managed release source URL must be credential-free HTTPS without query or fragment")
	}
	return parsed.String(), nil
}

func managedRegistrySnapshot(
	target *scannerrelease.RegistryTarget,
) (scannerreleaseworkspace.RegistryTarget, error) {
	if target == nil || !target.Enabled || target.ID == "" || target.Version <= 0 ||
		strings.TrimSpace(target.Namespace) == "" {
		return scannerreleaseworkspace.RegistryTarget{}, errors.New("registry target is disabled or incomplete")
	}
	_, host, err := releaseRegistryOrigin(target.Host)
	if err != nil {
		return scannerreleaseworkspace.RegistryTarget{}, err
	}
	return scannerreleaseworkspace.RegistryTarget{
		ID: target.ID, Version: target.Version, Host: host,
		Namespace: target.Namespace, Repository: target.Namespace,
	}, nil
}

type managedRegistryClients struct {
	store   scannerrelease.Persistence
	factory releaseRegistryClientFactory
}

func (p managedRegistryClients) Client(
	ctx context.Context,
	execution scannerreleaseworkspace.ExecutionContext,
) (scannerregistry.Client, error) {
	primary, err := p.store.GetRegistryTarget(ctx, execution.Primary.ID)
	if err != nil {
		return scannerregistry.Client{}, errors.New("primary registry target was not found")
	}
	mirror, err := p.store.GetRegistryTarget(ctx, execution.Mirror.ID)
	if err != nil {
		return scannerregistry.Client{}, errors.New("mirror registry target was not found")
	}
	primarySnapshot, err := managedRegistrySnapshot(primary)
	if err != nil || primarySnapshot != execution.Primary {
		return scannerregistry.Client{}, errors.New("primary registry target changed after build binding")
	}
	mirrorSnapshot, err := managedRegistrySnapshot(mirror)
	if err != nil || mirrorSnapshot != execution.Mirror {
		return scannerregistry.Client{}, errors.New("mirror registry target changed after build binding")
	}
	client, primaryHost, mirrorHost, err := p.factory.Pair(ctx, primary, mirror)
	if err != nil {
		return scannerregistry.Client{}, err
	}
	if primaryHost != execution.Primary.Host || mirrorHost != execution.Mirror.Host {
		return scannerregistry.Client{}, errors.New("registry origin does not match immutable execution context")
	}
	return client, nil
}

type managedMirrorSigner struct {
	profilePath string
	adapterPath string
	journalRoot string
	environment []string
}

type managedLaneIdentity struct {
	name              string
	serviceAccount    string
	credentialSecrets []string
}

func validateManagedLaneIdentitySeparation(identities []managedLaneIdentity) error {
	serviceAccounts := make(map[string]string)
	credentialSecrets := make(map[string]string)
	for _, identity := range identities {
		if value := strings.TrimSpace(identity.serviceAccount); value != "" {
			if owner := serviceAccounts[value]; owner != "" {
				return fmt.Errorf(
					"managed release lanes %s and %s share service account %q",
					owner, identity.name, value,
				)
			}
			serviceAccounts[value] = identity.name
		}
		for _, raw := range identity.credentialSecrets {
			value := strings.TrimSpace(raw)
			if value == "" {
				continue
			}
			if owner := credentialSecrets[value]; owner != "" {
				return fmt.Errorf(
					"managed release lanes %s and %s share credential secret %q",
					owner, identity.name, value,
				)
			}
			credentialSecrets[value] = identity.name
		}
	}
	return nil
}

type managedGitAuthorizationFile string

func (file managedGitAuthorizationFile) Authorization(
	_ context.Context,
	_ string,
) (string, error) {
	path := strings.TrimSpace(string(file))
	if path == "" {
		return "", nil
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("managed Git authorization file must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 64<<10 {
		return "", errors.New("managed Git authorization file is not a bounded regular file")
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("read managed Git authorization file")
	}
	authorization := strings.TrimSpace(string(value))
	if strings.ContainsAny(authorization, "\x00\r\n") {
		return "", errors.New("managed Git authorization file is invalid")
	}
	return authorization, nil
}

func (s managedMirrorSigner) SignMirror(
	ctx context.Context,
	operationID string,
	binding scannerreleasebackend.Binding,
	artifact scannersigning.Artifact,
) (scannerreleasebackend.MirrorSigningReceipt, error) {
	profile, err := scannersigning.ReadProfileFile(s.profilePath)
	if err != nil {
		return scannerreleasebackend.MirrorSigningReceipt{}, err
	}
	evidence, result, err := (scannersigning.Service{
		Adapter: scannersigning.CommandAdapter{
			Path: s.adapterPath, Environment: s.environment,
		},
		JournalRoot: s.journalRoot, RequireDurableArtifact: true,
	}).Sign(ctx, profile, artifact, scannersigning.Binding{
		DefinitionCommit: binding.DefinitionCommit, LockDigest: binding.LockDigest,
		PolicyID: binding.PolicyID, PolicyRevision: binding.PolicyRevision,
	}, operationID)
	if err != nil {
		return scannerreleasebackend.MirrorSigningReceipt{}, err
	}
	return scannerreleasebackend.MirrorSigningReceipt{
		ExternalOperationID: result.ExternalOperationID,
		SignatureURI:        evidence.SignatureURI, SignatureDigest: evidence.SignatureDigest,
		SignatureArtifactDigest: evidence.SignatureArtifactDigest,
		SignatureMediaType:      evidence.SignatureMediaType,
		SignatureArtifactSize:   evidence.SignatureArtifactSize,
		CertificateDigest:       evidence.CertificateDigest,
		Identity:                evidence.ObservedIdentity,
		Issuer:                  evidence.ObservedIssuer,
		Subject:                 evidence.ObservedSubject,
		TrustRoot:               evidence.ObservedTrustRoot,
	}, nil
}

func managedScannerReleaseBackend(
	rawStore db.Store,
	persistence scannerrelease.Persistence,
	platforms []string,
) (scannerreleasebackend.Backend, error) {
	if rawStore == nil || persistence == nil {
		return nil, errors.New("managed scanner release store is required")
	}
	sourceURL := strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_SOURCE_URL"))
	primaryID := strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_PRIMARY_REGISTRY_ID"))
	mirrorID := strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_MIRROR_REGISTRY_ID"))
	if sourceURL == "" || primaryID == "" || mirrorID == "" {
		return nil, errors.New("managed releases require source URL and primary/mirror registry target IDs")
	}
	buildx, err := scannerreleasebackend.ManagedBuildxFromEnvironment(platforms)
	if err != nil {
		return nil, err
	}
	managedBuildx, ok := buildx.(scannerreleasebackend.BuildxBackend)
	if !ok {
		return nil, errors.New("managed buildx lane has an unexpected implementation")
	}
	if _, err := managedBuildx.Capabilities(context.Background()); err != nil {
		return nil, err
	}
	stepLane, err := scannerreleasebackend.KubernetesFromEnvironment(platforms)
	if err != nil {
		return nil, err
	}
	baseKubernetes, ok := stepLane.(scannerreleasebackend.KubernetesBackend)
	if !ok {
		return nil, errors.New("managed Kubernetes lane has an unexpected implementation")
	}
	adapterLane := func(
		lane scannerreleasebackend.AdapterLane,
		imageEnvironment, legacyCredentialEnvironment, registryCredentialEnvironment,
		engineCredentialEnvironment, serviceAccountEnvironment string,
	) (scannerreleasebackend.Backend, error) {
		configured := baseKubernetes
		configured.Image = strings.TrimSpace(os.Getenv(imageEnvironment))
		configured.AdapterPath = strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_ADAPTER_PATH"))
		if configured.AdapterPath == "" {
			configured.AdapterPath = "/usr/local/bin/wolf-release-adapter"
		}
		configured.SignerProfileSecret = ""
		configured.SignerCredentialSecret = ""
		configured.SignerAdapterPath = ""
		configured.SignerWorkloadIdentity = false
		configured.ServiceAccount = ""
		configured.AdapterRegistryCredentialSecret = strings.TrimSpace(os.Getenv(registryCredentialEnvironment))
		if configured.AdapterRegistryCredentialSecret == "" {
			configured.AdapterRegistryCredentialSecret = strings.TrimSpace(os.Getenv(legacyCredentialEnvironment))
		}
		if configured.AdapterRegistryCredentialSecret == "" {
			return nil, fmt.Errorf(
				"%s release adapter requires %s with registry config.json credentials",
				lane, registryCredentialEnvironment,
			)
		}
		configured.AdapterRegistryCredentialMount = "/run/wolf/adapter-registry"
		if engineCredentialEnvironment != "" {
			configured.AdapterEngineCredentialSecret = strings.TrimSpace(os.Getenv(engineCredentialEnvironment))
			if configured.AdapterEngineCredentialSecret == "" {
				return nil, fmt.Errorf(
					"%s release adapter requires %s with only remote-engine mTLS files",
					lane, engineCredentialEnvironment,
				)
			}
			configured.AdapterEngineCredentialMount = "/run/wolf/adapter-engine"
		}
		workloadEnvironment := strings.TrimSuffix(registryCredentialEnvironment, "_SECRET") + "_WORKLOAD_IDENTITY"
		if raw := strings.TrimSpace(os.Getenv(workloadEnvironment)); raw != "" {
			value, parseErr := strconv.ParseBool(raw)
			if parseErr != nil {
				return nil, fmt.Errorf("parse %s: %w", workloadEnvironment, parseErr)
			}
			configured.AdapterWorkloadIdentity = value
		}
		if serviceAccount := strings.TrimSpace(os.Getenv(serviceAccountEnvironment)); serviceAccount != "" {
			configured.ServiceAccount = serviceAccount
		}
		if configured.AdapterWorkloadIdentity && configured.ServiceAccount == "" {
			return nil, fmt.Errorf("%s requires an explicit service account when workload identity is enabled", lane)
		}
		backend := scannerreleasebackend.AdapterBackend{Lane: lane, Kubernetes: configured}
		if _, err := backend.Capabilities(context.Background()); err != nil {
			return nil, fmt.Errorf("configure %s release adapter: %w", lane, err)
		}
		return backend, nil
	}
	fixedLane, err := adapterLane(
		scannerreleasebackend.AdapterLaneFixed,
		"WOLF_SCANNER_RELEASE_FIXED_ADAPTER_IMAGE",
		"WOLF_SCANNER_RELEASE_FIXED_CREDENTIAL_SECRET",
		"WOLF_SCANNER_RELEASE_FIXED_REGISTRY_CREDENTIAL_SECRET",
		"",
		"WOLF_SCANNER_RELEASE_FIXED_SERVICE_ACCOUNT",
	)
	if err != nil {
		return nil, err
	}
	qualityLane, err := adapterLane(
		scannerreleasebackend.AdapterLaneQuality,
		"WOLF_SCANNER_RELEASE_QUALITY_ADAPTER_IMAGE",
		"WOLF_SCANNER_RELEASE_QUALITY_CREDENTIAL_SECRET",
		"WOLF_SCANNER_RELEASE_QUALITY_REGISTRY_CREDENTIAL_SECRET",
		"WOLF_SCANNER_RELEASE_QUALITY_ENGINE_CREDENTIAL_SECRET",
		"WOLF_SCANNER_RELEASE_QUALITY_SERVICE_ACCOUNT",
	)
	if err != nil {
		return nil, err
	}
	integrationLane, err := adapterLane(
		scannerreleasebackend.AdapterLaneIntegration,
		"WOLF_SCANNER_RELEASE_INTEGRATION_ADAPTER_IMAGE",
		"WOLF_SCANNER_RELEASE_INTEGRATION_CREDENTIAL_SECRET",
		"WOLF_SCANNER_RELEASE_INTEGRATION_REGISTRY_CREDENTIAL_SECRET",
		"WOLF_SCANNER_RELEASE_INTEGRATION_ENGINE_CREDENTIAL_SECRET",
		"WOLF_SCANNER_RELEASE_INTEGRATION_SERVICE_ACCOUNT",
	)
	if err != nil {
		return nil, err
	}
	signingLane := baseKubernetes
	signingLane.Actions = []string{"signature/*", "release-manifest-signature"}
	signingLane.AdapterRegistryCredentialSecret = ""
	signingLane.AdapterRegistryCredentialMount = ""
	signingLane.AdapterEngineCredentialSecret = ""
	signingLane.AdapterEngineCredentialMount = ""
	signingLane.AdapterWorkloadIdentity = false
	signingLane.AdapterPath = ""
	signingLane.ExecutionLane = scannerreleasebackend.KubernetesExecutionLaneSigner
	signingLane.ServiceAccount = strings.TrimSpace(
		os.Getenv("WOLF_SCANNER_RELEASE_SIGNER_SERVICE_ACCOUNT"),
	)
	if signingLane.SignerWorkloadIdentity && signingLane.ServiceAccount == "" {
		return nil, errors.New(
			"signer workload identity requires WOLF_SCANNER_RELEASE_SIGNER_SERVICE_ACCOUNT",
		)
	}
	fixedIdentity := fixedLane.(scannerreleasebackend.AdapterBackend).Kubernetes
	qualityIdentity := qualityLane.(scannerreleasebackend.AdapterBackend).Kubernetes
	integrationIdentity := integrationLane.(scannerreleasebackend.AdapterBackend).Kubernetes
	if err := validateManagedLaneIdentitySeparation([]managedLaneIdentity{
		{name: "ordinary-step", serviceAccount: baseKubernetes.ServiceAccount},
		{
			name: "fixed", serviceAccount: fixedIdentity.ServiceAccount,
			credentialSecrets: []string{fixedIdentity.AdapterRegistryCredentialSecret},
		},
		{
			name: "quality", serviceAccount: qualityIdentity.ServiceAccount,
			credentialSecrets: []string{
				qualityIdentity.AdapterRegistryCredentialSecret,
				qualityIdentity.AdapterEngineCredentialSecret,
			},
		},
		{
			name: "integration", serviceAccount: integrationIdentity.ServiceAccount,
			credentialSecrets: []string{
				integrationIdentity.AdapterRegistryCredentialSecret,
				integrationIdentity.AdapterEngineCredentialSecret,
			},
		},
		{
			name: "signer", serviceAccount: signingLane.ServiceAccount,
			credentialSecrets: []string{signingLane.SignerCredentialSecret},
		},
	}); err != nil {
		return nil, err
	}
	if _, err := signingLane.Capabilities(context.Background()); err != nil {
		return nil, fmt.Errorf("configure managed signing lane: %w", err)
	}
	profilePath := strings.TrimSpace(os.Getenv("WOLF_SCANNER_SIGNER_PROFILE_FILE"))
	adapterPath := strings.TrimSpace(os.Getenv("WOLF_SCANNER_SIGNER_ADAPTER"))
	journalRoot := strings.TrimSpace(os.Getenv("WOLF_SCANNER_SIGNER_JOURNAL"))
	if !filepath.IsAbs(profilePath) || !filepath.IsAbs(adapterPath) || !filepath.IsAbs(journalRoot) {
		return nil, errors.New("managed releases require absolute signer profile, adapter, and journal paths")
	}
	environment, err := selectedEnvironment(executorEnvironmentAllowlist("WOLF_SCANNER_SIGNER_ENV"))
	if err != nil {
		return nil, err
	}
	registry := scannerreleasebackend.RegistryBackend{
		Clients: managedRegistryClients{
			store: persistence, factory: releaseRegistryClientFactory{store: rawStore},
		},
		MirrorSigner: managedMirrorSigner{
			profilePath: profilePath, adapterPath: adapterPath,
			journalRoot: journalRoot, environment: environment,
		},
		Platforms: append([]string(nil), platforms...),
	}
	plan, err := scannerpipeline.Default(scannerpipeline.Inputs{
		Images:         scannerpipeline.ManagedReleaseImages(),
		RequireCompose: true, RequireKubernetes: true, RequireMirror: true,
	})
	if err != nil {
		return nil, err
	}
	managed := scannerreleasebackend.ManagedBackend{
		// Registry precedes the step lane for image-manifest and published-verify;
		// it is the only route authorized to mutate OCI sinks.
		Router: scannerreleasebackend.Router{Backends: []scannerreleasebackend.Backend{
			registry, managedBuildx, signingLane, fixedLane, qualityLane, integrationLane,
		}},
		Contexts: managedReleaseContextProvider{
			store: persistence, sourceURL: sourceURL,
			primaryID: primaryID, mirrorID: mirrorID,
		},
		Sources: &scannerreleasebackend.GitSourceMaterializer{
			Runtime: scannerreleasebackend.ExecRuntime{MaxOutputBytes: 64 << 10},
			GitPath: envOr("WOLF_SCANNER_RELEASE_GIT_PATH", "/usr/bin/git"),
			Authorization: managedGitAuthorizationFile(
				os.Getenv("WOLF_SCANNER_RELEASE_GIT_AUTHORIZATION_FILE"),
			),
		},
		RequiredPlan: plan, RequiredPlatforms: []string{"linux/amd64", "linux/arm64"},
	}
	if _, err := managed.Capabilities(context.Background()); err != nil {
		return nil, fmt.Errorf("managed scanner release coverage is incomplete: %w", err)
	}
	return managed, nil
}
