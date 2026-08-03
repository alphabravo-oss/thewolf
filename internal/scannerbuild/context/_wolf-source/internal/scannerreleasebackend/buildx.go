package scannerreleasebackend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworkspace"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
)

// BuildxBackend creates one operation-scoped Kubernetes-driver BuildKit
// builder. Kubernetes driver resource requests/limits enforce CPU, memory, and
// ephemeral storage on the actual BuildKit workload, rather than merely on the
// buildx client process.
type BuildxBackend struct {
	Runtime     CommandRuntime
	BuildxPath  string
	Registry    string
	Environment []string
	Platforms   []string
	Push        bool
	// UseWorkspaceRegistry binds managed builds to the immutable primary
	// target snapshot provisioned in the workspace. Legacy standalone buildx
	// continues to use Registry.
	UseWorkspaceRegistry bool
	// RequirePush prevents managed mode from advertising OCI publication when
	// buildx was configured to emit only a local tar.
	RequirePush bool
	// KubernetesNamespace and BuildKitServiceAccount bind the Kubernetes
	// driver to a dedicated, least-privilege BuildKit identity.
	KubernetesNamespace       string
	BuildKitServiceAccount    string
	RequireKubernetesIdentity bool
	// DockerConfigDirectory is a deployment-mounted, per-primary-target
	// Docker config. Managed mode validates that it contains exactly the bound
	// primary registry host and overrides any ambient DOCKER_CONFIG.
	DockerConfigDirectory string
	RequireRegistryAuth   bool
}

func (b BuildxBackend) Name() string { return "buildkit-buildx" }

func (b BuildxBackend) Capabilities(context.Context) (Capabilities, error) {
	if b.Runtime == nil || strings.TrimSpace(b.BuildxPath) == "" {
		return Capabilities{}, errors.New("buildx backend requires a command runtime and buildx executable")
	}
	if b.RequirePush && !b.Push {
		return Capabilities{}, errors.New("managed buildx backend requires registry push")
	}
	if b.RequireKubernetesIdentity &&
		(!componentPattern.MatchString(b.KubernetesNamespace) ||
			!componentPattern.MatchString(b.BuildKitServiceAccount)) {
		return Capabilities{}, errors.New("managed buildx backend requires a namespace and BuildKit service account")
	}
	if b.RequireRegistryAuth && !filepath.IsAbs(b.DockerConfigDirectory) {
		return Capabilities{}, errors.New("managed buildx backend requires an absolute target-bound Docker config directory")
	}
	if !b.UseWorkspaceRegistry {
		if _, err := safeRegistryBase(b.Registry); err != nil {
			return Capabilities{}, err
		}
	}
	gib := int64(1 << 30)
	return Capabilities{
		Name: b.Name(), Actions: []string{"build/*"},
		Kinds:     []scannerpipeline.StepKind{scannerpipeline.StepBuild},
		Platforms: append([]string(nil), b.Platforms...),
		MaxCPU:    64000, MaxMemory: 256 * gib, MaxDisk: 1024 * gib,
		MaxTimeout: 24 * time.Hour, MaxConcurrency: 64,
		EnforcesCPU: true, EnforcesMemory: true, EnforcesDisk: true,
		EnforcesTimeout: true, EnforcesCancellation: true, Idempotent: true,
		ExternalIdempotency: true,
	}, nil
}

func (b BuildxBackend) Execute(
	ctx context.Context,
	invocation Invocation,
) (BackendResult, error) {
	if _, err := b.Capabilities(ctx); err != nil {
		return BackendResult{}, err
	}
	if invocation.Action.Kind != scannerpipeline.StepBuild ||
		invocation.Action.Image == "" || invocation.Action.Platform == "" {
		return BackendResult{}, fmt.Errorf("%w: buildx action %q", ErrUnsupportedStep, invocation.Action.Name)
	}
	lock, err := scannerlock.LoadFile(filepath.Join(
		invocation.Request.Workspace, scannerlock.DefaultLockPath,
	))
	if err != nil {
		return BackendResult{}, fmt.Errorf("load buildx scanner lock: %w", err)
	}
	if lock.LockDigest != invocation.Binding.LockDigest {
		return BackendResult{}, fmt.Errorf("%w: checked-out lock is %s", ErrBinding, lock.LockDigest)
	}
	selection, err := resolveBuildSelection(lock, invocation.Action.Image)
	if err != nil {
		return BackendResult{}, err
	}
	variant := selection.Variant
	if !contains(variant.Platforms, invocation.Action.Platform) {
		return BackendResult{}, fmt.Errorf(
			"%w: variant %q does not declare %q",
			ErrUnsupportedStep, invocation.Action.Image, invocation.Action.Platform,
		)
	}
	buildArguments, err := b.resolveBuildArguments(invocation, lock, selection)
	if err != nil {
		return BackendResult{}, err
	}
	registry, err := b.registryBase(invocation)
	if err != nil {
		return BackendResult{}, err
	}
	if b.DockerConfigDirectory != "" {
		host, _, found := strings.Cut(registry, "/")
		if !found || strings.TrimSpace(host) == "" {
			return BackendResult{}, errors.New("managed buildx registry host is invalid")
		}
		if err := validateBuildxDockerConfig(b.DockerConfigDirectory, host); err != nil {
			return BackendResult{}, err
		}
		b.Environment = environmentAssignment(b.Environment, "DOCKER_CONFIG", b.DockerConfigDirectory)
	}
	commands, metadataPath, reference, builder, err := b.render(
		invocation, variant, buildArguments, registry,
	)
	if err != nil {
		return BackendResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(metadataPath), 0o700); err != nil {
		return BackendResult{}, err
	}
	var logs strings.Builder
	cleanupRegistered := false
	for index, command := range commands {
		output, runErr := b.Runtime.Run(ctx, command)
		logs.Write(output.Stdout)
		logs.Write(output.Stderr)
		if runErr != nil {
			// A deterministic builder may remain after process loss. Inspecting
			// it is safe; any other error remains terminal and no arbitrary
			// fallback command is accepted.
			if index == 0 {
				if _, inspectErr := b.Runtime.Run(ctx, Command{
					Path:        b.BuildxPath,
					Args:        []string{"inspect", builder},
					Environment: b.Environment,
				}); inspectErr == nil {
					if !cleanupRegistered {
						cleanupRegistered = true
						defer b.cleanup(builder)
					}
					continue
				}
			}
			return BackendResult{}, runErr
		}
		if !cleanupRegistered {
			cleanupRegistered = true
			defer b.cleanup(builder)
		}
	}
	metadata, err := os.ReadFile(metadataPath)
	if err != nil {
		return BackendResult{}, fmt.Errorf("read buildx metadata: %w", err)
	}
	if len(metadata) > maxBackendResultBytes {
		return BackendResult{}, errors.New("buildx metadata exceeds size limit")
	}
	var values map[string]any
	if err := json.Unmarshal(metadata, &values); err != nil {
		return BackendResult{}, fmt.Errorf("decode buildx metadata: %w", err)
	}
	digest, _ := values["containerimage.digest"].(string)
	if !digestPattern.MatchString(digest) {
		return BackendResult{}, errors.New("buildx did not return an immutable image digest")
	}
	return BackendResult{
		Binding: invocation.Binding, ExternalOperationID: invocation.OperationID,
		Result: scannerreleaseworkerResult(
			"oci://"+reference+"@"+digest, digest,
			map[string]any{
				"image": variant.Image, "variant": invocation.Action.Image,
				"platform": invocation.Action.Platform,
				"builder":  builder, "pushed": b.Push,
			},
		),
		Log: logs.String(),
	}, nil
}

func (b BuildxBackend) cleanup(builder string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = b.Runtime.Run(ctx, Command{
		Path: b.BuildxPath, Args: []string{"rm", "--force", builder},
		Environment: b.Environment,
	})
}

func (b BuildxBackend) render(
	invocation Invocation,
	variant scannerlock.BuildVariant,
	buildArguments map[string]string,
	registryBase string,
) ([]Command, string, string, string, error) {
	registry, err := safeRegistryBase(registryBase)
	if err != nil {
		return nil, "", "", "", err
	}
	dockerfile, err := resolveWorkspaceFile(invocation.Request.Workspace, variant.Dockerfile)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("resolve scanner lock Dockerfile: %w", err)
	}
	contextPath := variant.Context
	if strings.TrimSpace(contextPath) == "" {
		contextPath = "."
	}
	contextDirectory, err := resolveWorkspaceDirectory(invocation.Request.Workspace, contextPath)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("resolve scanner lock build context: %w", err)
	}
	buildContext := contextDirectory
	dockerfileArgument := dockerfile
	if b.UseWorkspaceRegistry {
		contextSubdirectory := path.Clean(filepath.ToSlash(contextPath))
		dockerfileRelative, relativeErr := filepath.Rel(contextDirectory, dockerfile)
		if relativeErr != nil || filepath.IsAbs(dockerfileRelative) ||
			dockerfileRelative == ".." || strings.HasPrefix(dockerfileRelative, ".."+string(filepath.Separator)) {
			return nil, "", "", "", errors.New("managed Buildx Dockerfile escapes its locked context")
		}
		dockerfileArgument = filepath.ToSlash(dockerfileRelative)
		buildContext = wolfImageSource + ".git#" + invocation.Request.DefinitionCommit
		if contextSubdirectory != "." {
			buildContext += ":" + contextSubdirectory
		}
	}
	builder := "wolf-" + strings.TrimPrefix(invocation.OperationID, "sha256:")[:24]
	resources := invocation.Resources
	driverOptions := strings.Join([]string{
		"rootless=true",
		"requests.cpu=" + strconv.FormatInt(resources.CPUMilli, 10) + "m",
		"limits.cpu=" + strconv.FormatInt(resources.CPUMilli, 10) + "m",
		"requests.memory=" + strconv.FormatInt(resources.MemoryBytes, 10),
		"limits.memory=" + strconv.FormatInt(resources.MemoryBytes, 10),
		"requests.ephemeral-storage=" + strconv.FormatInt(resources.DiskBytes, 10),
		"limits.ephemeral-storage=" + strconv.FormatInt(resources.DiskBytes, 10),
	}, ",")
	if b.KubernetesNamespace != "" {
		driverOptions += ",namespace=" + b.KubernetesNamespace
	}
	if b.BuildKitServiceAccount != "" {
		driverOptions += ",serviceaccount=" + b.BuildKitServiceAccount
	}
	metadata := filepath.Join(
		invocation.Request.Workspace, ".wolf-release-buildx",
		strings.TrimPrefix(invocation.OperationID, "sha256:")+".json",
	)
	reference := strings.TrimSuffix(registry, "/") + "/" + variant.Image
	tag := reference + ":operation-" +
		strings.TrimPrefix(invocation.OperationID, "sha256:")
	buildArgs := []string{
		"build", "--builder", builder,
		"--file", dockerfileArgument,
		"--platform", invocation.Action.Platform,
		"--tag", tag,
		"--metadata-file", metadata,
		"--provenance=mode=max,version=v1,builder-id=" + wolfBuildxBuilderID, "--sbom=true",
	}
	labels := map[string]string{
		annotationSource:    wolfImageSource,
		annotationRevision:  invocation.Request.DefinitionCommit,
		annotationVersion:   invocation.Request.CandidateID,
		annotationCandidate: invocation.Request.CandidateID,
		annotationImageKind: buildArguments["WOLF_IMAGE_KIND"],
		annotationVariant:   invocation.Action.Image,
	}
	labelNames := make([]string, 0, len(labels))
	for name := range labels {
		labelNames = append(labelNames, name)
	}
	sort.Strings(labelNames)
	for _, name := range labelNames {
		buildArgs = append(buildArgs, "--label", name+"="+labels[name])
	}
	argumentNames := make([]string, 0, len(buildArguments))
	for name := range buildArguments {
		argumentNames = append(argumentNames, name)
	}
	sort.Strings(argumentNames)
	for _, name := range argumentNames {
		buildArgs = append(buildArgs, "--build-arg", name+"="+buildArguments[name])
	}
	if b.Push {
		buildArgs = append(buildArgs, "--push")
	} else {
		output := filepath.Join(
			invocation.Request.Workspace, ".wolf-release-buildx",
			strings.TrimPrefix(invocation.OperationID, "sha256:")+".oci.tar",
		)
		buildArgs = append(buildArgs, "--output", "type=oci,dest="+output)
	}
	buildArgs = append(buildArgs, buildContext)
	commands := []Command{
		{
			Path: b.BuildxPath,
			Args: []string{
				"create", "--name", builder, "--driver", "kubernetes",
				"--driver-opt", driverOptions,
			},
			Environment: b.Environment,
		},
		{
			Path:        b.BuildxPath,
			Args:        []string{"inspect", "--bootstrap", builder},
			Environment: b.Environment,
		},
		{
			Path: b.BuildxPath, Args: buildArgs,
			Directory: invocation.Request.Workspace, Environment: b.Environment,
		},
	}
	return commands, metadata, reference, builder, nil
}

func validateBuildxDockerConfig(directory, registryHost string) error {
	if !filepath.IsAbs(directory) || strings.TrimSpace(registryHost) == "" {
		return errors.New("Buildx Docker config binding is invalid")
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil || !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("Buildx Docker config directory must be a real directory")
	}
	path := filepath.Join(directory, "config.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 1<<20 {
		return errors.New("Buildx Docker config must be a bounded regular config.json")
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return errors.New("read Buildx Docker config")
	}
	var document struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil || ensureEOF(decoder) != nil || len(document.Auths) != 1 {
		return errors.New("Buildx Docker config must contain exactly one registry auth entry")
	}
	raw, exists := document.Auths[registryHost]
	if !exists {
		return errors.New("Buildx Docker config is not bound to the immutable primary registry host")
	}
	var credential struct {
		Auth          string `json:"auth,omitempty"`
		IdentityToken string `json:"identitytoken,omitempty"`
		RegistryToken string `json:"registrytoken,omitempty"`
		Username      string `json:"username,omitempty"`
		Password      string `json:"password,omitempty"`
	}
	credentialDecoder := json.NewDecoder(strings.NewReader(string(raw)))
	credentialDecoder.DisallowUnknownFields()
	if err := credentialDecoder.Decode(&credential); err != nil || ensureEOF(credentialDecoder) != nil ||
		(strings.TrimSpace(credential.Auth) == "" && strings.TrimSpace(credential.IdentityToken) == "" &&
			strings.TrimSpace(credential.RegistryToken) == "" &&
			(strings.TrimSpace(credential.Username) == "" || strings.TrimSpace(credential.Password) == "")) {
		return errors.New("Buildx Docker config registry credential is invalid")
	}
	return nil
}

func environmentAssignment(environment []string, name, value string) []string {
	prefix := name + "="
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+value)
}

func (b BuildxBackend) registryBase(invocation Invocation) (string, error) {
	if !b.UseWorkspaceRegistry {
		return safeRegistryBase(b.Registry)
	}
	execution, err := scannerreleaseworkspace.ReadContext(invocation.Request.Workspace)
	if err != nil {
		return "", fmt.Errorf("read managed buildx execution context: %w", err)
	}
	return safeRegistryBase(
		strings.TrimSuffix(execution.Primary.Host, "/") + "/" +
			strings.Trim(execution.Primary.Repository, "/"),
	)
}

type buildSelection struct {
	Kind    string
	Key     string
	Variant scannerlock.BuildVariant
}

func resolveBuildSelection(lock *scannerlock.Lock, actionImage string) (buildSelection, error) {
	if variant, ok := lock.ReleaseInputs.Variants[actionImage]; ok {
		if strings.TrimSpace(variant.Image) == "" {
			return buildSelection{}, fmt.Errorf("%w: scanner variant %q has no canonical image", ErrBinding, actionImage)
		}
		return buildSelection{Kind: "scanner", Key: actionImage, Variant: variant}, nil
	}
	if strings.HasPrefix(actionImage, "fixer-") {
		key := strings.TrimPrefix(actionImage, "fixer-")
		if variant, ok := lock.ReleaseInputs.FixerVariants[key]; ok {
			if strings.TrimSpace(variant.Image) == "" {
				return buildSelection{}, fmt.Errorf("%w: fixer variant %q has no canonical image", ErrBinding, key)
			}
			return buildSelection{Kind: "fixer", Key: key, Variant: variant}, nil
		}
	}
	return buildSelection{}, fmt.Errorf("%w: unknown image variant %q", ErrUnsupportedStep, actionImage)
}

func (b BuildxBackend) resolveBuildArguments(
	invocation Invocation,
	lock *scannerlock.Lock,
	selection buildSelection,
) (map[string]string, error) {
	arguments := make(map[string]string, len(selection.Variant.BuildArgs)+7)
	for name, value := range selection.Variant.BuildArgs {
		arguments[name] = value
	}
	arguments["WOLF_DEFINITION_COMMIT"] = invocation.Request.DefinitionCommit
	arguments["WOLF_LOCK_DIGEST"] = lock.LockDigest
	arguments["WOLF_CANDIDATE_ID"] = invocation.Request.CandidateID
	arguments["WOLF_IMAGE_KIND"] = selection.Kind
	arguments["WOLF_IMAGE_VARIANT"] = invocation.Action.Image
	arguments["WOLF_BUILD_PLATFORM"] = invocation.Action.Platform
	if selection.Kind == "scanner" || selection.Key == "base" {
		arguments["WOLF_VERSION"] = invocation.Request.CandidateID
	}
	if selection.Kind != "fixer" || len(selection.Variant.DependsOn) == 0 {
		return arguments, nil
	}
	if !b.Push {
		return nil, errors.New("dependent fixer builds require a pushed immutable base image")
	}
	for _, dependency := range selection.Variant.DependsOn {
		if dependency != "base" {
			return nil, fmt.Errorf("%w: unsupported fixer dependency %q", ErrUnsupportedStep, dependency)
		}
		base, ok := lock.ReleaseInputs.FixerVariants[dependency]
		if !ok || strings.TrimSpace(base.Image) == "" {
			return nil, fmt.Errorf("%w: fixer dependency %q is absent", ErrBinding, dependency)
		}
		dependencyKey := "image-manifest/fixer-" + dependency
		evidence, ok := invocation.Request.Dependencies[dependencyKey]
		if !ok || !digestPattern.MatchString(evidence.OutputDigest) {
			return nil, fmt.Errorf(
				"%w: fixer dependency %q has no verified image-manifest digest",
				ErrBinding, dependency,
			)
		}
		registry, err := b.registryBase(invocation)
		if err != nil {
			return nil, err
		}
		arguments["WOLF_FIXER_BASE_REF"] = strings.TrimSuffix(registry, "/") + "/" +
			base.Image + "@" + evidence.OutputDigest
	}
	return arguments, nil
}

func resolveWorkspaceFile(workspace, relative string) (string, error) {
	path, info, err := resolveWorkspacePath(workspace, relative)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("path is not a regular file")
	}
	return path, nil
}

func resolveWorkspaceDirectory(workspace, relative string) (string, error) {
	path, info, err := resolveWorkspacePath(workspace, relative)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("path is not a directory")
	}
	return path, nil
}

func resolveWorkspacePath(workspace, relative string) (string, os.FileInfo, error) {
	if filepath.IsAbs(relative) || strings.ContainsAny(relative, "\x00\r\n") {
		return "", nil, errors.New("path is unsafe")
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", nil, errors.New("path escapes the workspace")
	}
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", nil, err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, clean))
	if err != nil {
		return "", nil, err
	}
	if path != root && !strings.HasPrefix(path, root+string(filepath.Separator)) {
		return "", nil, errors.New("path escapes the workspace through a symlink")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", nil, err
	}
	return path, info, nil
}

func safeRegistryBase(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return "", errors.New("buildx registry namespace is required")
	}
	parsed, err := url.Parse("https://" + value)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("buildx registry namespace is invalid")
	}
	return strings.TrimSuffix(value, "/"), nil
}
