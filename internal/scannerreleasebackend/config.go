package scannerreleasebackend

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// FromEnvironment constructs a built-in backend using a fixed configuration
// schema. Secrets are read from service-account files or environment values
// and are never placed in a child argv.
func FromEnvironment(kind string, platforms []string) (Backend, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case "local", "offline":
		return localFromEnvironment(platforms)
	case "buildkit", "buildx":
		buildx, err := buildxFromEnvironment(platforms)
		if err != nil {
			return nil, err
		}
		local, err := localFromEnvironment(platforms)
		if err != nil {
			return nil, fmt.Errorf(
				"buildx backend also requires the local evidence-step sandbox: %w", err,
			)
		}
		return Router{Backends: []Backend{buildx, local}}, nil
	case "kubernetes", "kubernetes-job":
		return kubernetesFromEnvironment(platforms)
	default:
		return nil, fmt.Errorf("unsupported built-in scanner release backend %q", kind)
	}
}

func localFromEnvironment(platforms []string) (Backend, error) {
	engine := envDefault("WOLF_SCANNER_RELEASE_CONTAINER_ENGINE", "podman")
	if !filepath.IsAbs(engine) {
		resolved, err := lookPathWithoutShell(engine)
		if err != nil {
			return nil, err
		}
		engine = resolved
	}
	hostWorkspaceRoot := strings.TrimSpace(
		os.Getenv("WOLF_SCANNER_RELEASE_HOST_WORKSPACE"),
	)
	containerWorkspaceRoot := ""
	if hostWorkspaceRoot != "" {
		containerWorkspaceRoot = envDefault(
			"WOLF_SCANNER_RELEASE_WORKSPACE", "/workspace",
		)
	}
	backend := LocalSandbox{
		Runtime:                ExecRuntime{MaxOutputBytes: maxBackendResultBytes},
		EnginePath:             engine,
		Image:                  strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_STEP_IMAGE")),
		Program:                envDefault("WOLF_SCANNER_RELEASE_STEP_PROGRAM", "/usr/local/bin/wolf"),
		Environment:            selectedBackendEnvironment(),
		Platforms:              append([]string(nil), platforms...),
		HostWorkspaceRoot:      hostWorkspaceRoot,
		ContainerWorkspaceRoot: containerWorkspaceRoot,
		SignerProfileHostFile: strings.TrimSpace(
			os.Getenv("WOLF_SCANNER_SIGNER_PROFILE_HOST_FILE"),
		),
		SignerCredentialHostDir: strings.TrimSpace(
			os.Getenv("WOLF_SCANNER_SIGNER_CREDENTIAL_HOST_DIR"),
		),
		SignerAdapterPath: strings.TrimSpace(
			os.Getenv("WOLF_SCANNER_SIGNER_ADAPTER"),
		),
		SignerNetwork: strings.TrimSpace(
			os.Getenv("WOLF_SCANNER_SIGNER_NETWORK"),
		),
	}
	if _, err := backend.Capabilities(nil); err != nil {
		return nil, err
	}
	return backend, nil
}

func buildxFromEnvironment(platforms []string) (Backend, error) {
	backend, err := configuredBuildxFromEnvironment(platforms)
	if err != nil {
		return nil, err
	}
	if _, err := backend.Capabilities(nil); err != nil {
		return nil, err
	}
	return backend, nil
}

func configuredBuildxFromEnvironment(platforms []string) (BuildxBackend, error) {
	path := envDefault("WOLF_SCANNER_RELEASE_BUILDX_PATH", "docker-buildx")
	if !filepath.IsAbs(path) {
		resolved, err := lookPathWithoutShell(path)
		if err != nil {
			return BuildxBackend{}, err
		}
		path = resolved
	}
	backend := BuildxBackend{
		Runtime:     ExecRuntime{MaxOutputBytes: maxBackendLogBytes},
		BuildxPath:  path,
		Registry:    strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_REGISTRY")),
		Environment: selectedBackendEnvironment(),
		Platforms:   append([]string(nil), platforms...),
		Push:        envBool("WOLF_SCANNER_RELEASE_BUILDX_PUSH", true),
		KubernetesNamespace: strings.TrimSpace(
			os.Getenv("WOLF_SCANNER_RELEASE_BUILDX_NAMESPACE"),
		),
		BuildKitServiceAccount: strings.TrimSpace(
			os.Getenv("WOLF_SCANNER_RELEASE_BUILDKIT_SERVICE_ACCOUNT"),
		),
		DockerConfigDirectory: strings.TrimSpace(
			os.Getenv("WOLF_SCANNER_RELEASE_BUILDX_DOCKER_CONFIG"),
		),
	}
	return backend, nil
}

// BuildxFromEnvironment exposes the narrowly scoped build lane to managed
// composition without implicitly adding the legacy local evidence route.
func BuildxFromEnvironment(platforms []string) (Backend, error) {
	return buildxFromEnvironment(platforms)
}

// ManagedBuildxFromEnvironment never reads a static registry destination.
// Each execution derives that coordinate from the immutable workspace target.
func ManagedBuildxFromEnvironment(platforms []string) (Backend, error) {
	backend, err := configuredBuildxFromEnvironment(platforms)
	if err != nil {
		return nil, err
	}
	backend.Registry = ""
	backend.UseWorkspaceRegistry = true
	backend.RequirePush = true
	backend.RequireKubernetesIdentity = true
	backend.RequireRegistryAuth = true
	if _, err := backend.Capabilities(nil); err != nil {
		return nil, err
	}
	return backend, nil
}

func kubernetesFromEnvironment(platforms []string) (Backend, error) {
	caFile := envDefault(
		"WOLF_SCANNER_RELEASE_K8S_CA_FILE",
		"/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
	)
	client, err := kubernetesHTTPClient(caFile)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_K8S_TOKEN"))
	if token == "" {
		tokenFile := envDefault(
			"WOLF_SCANNER_RELEASE_K8S_TOKEN_FILE",
			"/var/run/secrets/kubernetes.io/serviceaccount/token",
		)
		value, readErr := os.ReadFile(tokenFile)
		if readErr != nil {
			return nil, fmt.Errorf("read Kubernetes backend token: %w", readErr)
		}
		token = strings.TrimSpace(string(value))
	}
	namespace := strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_K8S_NAMESPACE"))
	if namespace == "" {
		namespaceFile := envDefault(
			"WOLF_SCANNER_RELEASE_K8S_NAMESPACE_FILE",
			"/var/run/secrets/kubernetes.io/serviceaccount/namespace",
		)
		if value, readErr := os.ReadFile(namespaceFile); readErr == nil {
			namespace = strings.TrimSpace(string(value))
		}
	}
	backend := KubernetesBackend{
		APIServer: envDefault(
			"WOLF_SCANNER_RELEASE_K8S_API_SERVER",
			"https://kubernetes.default.svc",
		),
		Namespace: namespace, Token: token, HTTPClient: client,
		Instance: strings.TrimSpace(
			os.Getenv("WOLF_SCANNER_RELEASE_K8S_INSTANCE"),
		),
		ExecutionLane:  KubernetesExecutionLaneOrdinary,
		WorkspacePVC:   strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_WORKSPACE_PVC")),
		WorkspaceRoot:  envDefault("WOLF_SCANNER_RELEASE_WORKSPACE", "/workspace"),
		Image:          strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_STEP_IMAGE")),
		Program:        envDefault("WOLF_SCANNER_RELEASE_STEP_PROGRAM", "/usr/local/bin/wolf"),
		ServiceAccount: strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_STEP_SERVICE_ACCOUNT")),
		SignerProfileSecret: strings.TrimSpace(
			os.Getenv("WOLF_SCANNER_SIGNER_PROFILE_SECRET"),
		),
		SignerCredentialSecret: strings.TrimSpace(
			os.Getenv("WOLF_SCANNER_SIGNER_CREDENTIAL_SECRET"),
		),
		SignerAdapterPath: strings.TrimSpace(
			os.Getenv("WOLF_SCANNER_SIGNER_ADAPTER"),
		),
		SignerWorkloadIdentity: envBool(
			"WOLF_SCANNER_SIGNER_WORKLOAD_IDENTITY", false,
		),
		PollInterval:  envDuration("WOLF_SCANNER_RELEASE_K8S_POLL", time.Second),
		JobTTLSeconds: envInt("WOLF_SCANNER_RELEASE_K8S_JOB_TTL", 300),
		Platforms:     append([]string(nil), platforms...),
	}
	if _, err := backend.Capabilities(nil); err != nil {
		return nil, err
	}
	return backend, nil
}

// KubernetesFromEnvironment exposes the fixed-action Job lane to managed
// composition while preserving the standalone backend configuration.
func KubernetesFromEnvironment(platforms []string) (Backend, error) {
	return kubernetesFromEnvironment(platforms)
}

func kubernetesHTTPClient(caFile string) (*http.Client, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if value, err := os.ReadFile(caFile); err == nil {
		if !pool.AppendCertsFromPEM(value) {
			return nil, errors.New("Kubernetes backend CA file contains no certificates")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12, RootCAs: pool,
		}},
		Timeout: 30 * time.Second,
	}, nil
}

func selectedBackendEnvironment() []string {
	names := []string{
		"PATH", "HOME", "SSL_CERT_FILE", "SSL_CERT_DIR",
		"DOCKER_CONFIG", "KUBECONFIG", "XDG_RUNTIME_DIR", "CONTAINER_HOST",
	}
	values := make([]string, 0, len(names))
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			values = append(values, name+"="+value)
		}
	}
	return values
}

func lookPathWithoutShell(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("resolve backend executable %q: %w", name, err)
	}
	return path, nil
}

func envDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
