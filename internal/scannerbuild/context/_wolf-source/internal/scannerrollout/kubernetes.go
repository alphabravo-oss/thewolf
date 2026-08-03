package scannerrollout

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

const kubernetesResponseLimit = int64(4 << 20)

type KubernetesConfig struct {
	BaseURL      string
	Namespace    string
	Token        string
	TokenFile    string
	CAFile       string
	HTTP         *http.Client
	PollInterval time.Duration
	PullTimeout  time.Duration
	AllowHTTP    bool
}

type KubernetesControl struct {
	Config KubernetesConfig
}

func (c KubernetesControl) Apply(
	ctx context.Context,
	assignment DeploymentAssignment,
) error {
	client, err := newRolloutKubernetesClient(c.Config)
	if err != nil {
		return err
	}
	if err := client.applyAssignmentConfig(ctx, assignment); err != nil {
		return err
	}
	deployments, err := client.listCohortDeployments(ctx, assignment.CohortName)
	if err != nil {
		return err
	}
	if len(deployments) == 0 {
		return fmt.Errorf("Kubernetes cohort %q has no worker Deployments", assignment.CohortName)
	}
	for _, deployment := range deployments {
		if err := client.patchDeployment(ctx, deployment, assignment); err != nil {
			return err
		}
	}
	timeout := c.Config.PullTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	poll := c.Config.PollInterval
	if poll <= 0 {
		poll = time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		if _, err := c.Observe(ctx, assignment); err == nil {
			return nil
		}
		if !time.Now().Before(deadline) {
			return errors.New("Kubernetes cohort convergence timed out")
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c KubernetesControl) Observe(
	ctx context.Context,
	assignment DeploymentAssignment,
) (DeploymentObservation, error) {
	client, err := newRolloutKubernetesClient(c.Config)
	if err != nil {
		return DeploymentObservation{}, err
	}
	current, control, err := client.readAssignmentConfig(ctx, assignment.CohortID)
	if err != nil {
		return DeploymentObservation{}, err
	}
	if control != "resumed" {
		return DeploymentObservation{}, fmt.Errorf("Kubernetes cohort is %s", control)
	}
	if current.OperationID != assignment.OperationID ||
		current.ReleaseID != assignment.ReleaseID ||
		current.ManifestDigest != assignment.ManifestDigest ||
		!mapsEqual(current.ImageDigests, assignment.ImageDigests) {
		return DeploymentObservation{}, errors.New("Kubernetes assignment ConfigMap identity mismatch")
	}
	deployments, err := client.listCohortDeployments(ctx, assignment.CohortName)
	if err != nil {
		return DeploymentObservation{}, err
	}
	if len(deployments) == 0 {
		return DeploymentObservation{}, errors.New("Kubernetes cohort has no worker Deployments")
	}
	for _, deployment := range deployments {
		if !deployment.readyFor(assignment) {
			return DeploymentObservation{}, fmt.Errorf(
				"Kubernetes Deployment %q has not converged", deployment.Metadata.Name,
			)
		}
	}
	return DeploymentObservation{
		OperationID: assignment.OperationID, ReleaseID: assignment.ReleaseID,
		ManifestDigest: assignment.ManifestDigest,
		ImageDigests:   cloneStrings(assignment.ImageDigests),
		Ready:          true, ObservedAt: time.Now().UTC(),
	}, nil
}

func (c KubernetesControl) Pause(
	ctx context.Context,
	assignment DeploymentAssignment,
) error {
	return c.setLifecycle(ctx, assignment, "paused")
}

func (c KubernetesControl) Resume(
	ctx context.Context,
	assignment DeploymentAssignment,
) error {
	return c.setLifecycle(ctx, assignment, "resumed")
}

func (c KubernetesControl) Cancel(
	ctx context.Context,
	assignment DeploymentAssignment,
) error {
	return c.setLifecycle(ctx, assignment, "cancelled")
}

func (c KubernetesControl) setLifecycle(
	ctx context.Context,
	assignment DeploymentAssignment,
	state string,
) error {
	client, err := newRolloutKubernetesClient(c.Config)
	if err != nil {
		return err
	}
	return client.patchAssignmentControl(ctx, assignment.CohortID, state)
}

type KubernetesImageCache struct {
	Config KubernetesConfig
}

func ValidateKubernetesConfig(config KubernetesConfig) error {
	_, err := newRolloutKubernetesClient(config)
	return err
}

func (c KubernetesImageCache) Prepare(
	ctx context.Context,
	operationID string,
	plan DeploymentPlan,
) (CacheVerification, error) {
	client, err := newRolloutKubernetesClient(c.Config)
	if err != nil {
		return CacheVerification{}, err
	}
	return client.prepareImages(ctx, operationID, plan)
}

type rolloutKubernetesClient struct {
	config KubernetesConfig
	base   *url.URL
	http   *http.Client
}

func newRolloutKubernetesClient(config KubernetesConfig) (*rolloutKubernetesClient, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(config.BaseURL), "/"))
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("Kubernetes rollout API URL is invalid")
	}
	if parsed.Scheme != "https" &&
		!(config.AllowHTTP && parsed.Scheme == "http") {
		return nil, errors.New("Kubernetes rollout API must use HTTPS")
	}
	if strings.TrimSpace(config.Namespace) == "" {
		return nil, errors.New("Kubernetes rollout namespace is required")
	}
	if config.Token == "" && strings.TrimSpace(config.TokenFile) != "" {
		raw, readErr := os.ReadFile(config.TokenFile)
		if readErr != nil {
			return nil, fmt.Errorf("read Kubernetes service-account token: %w", readErr)
		}
		config.Token = strings.TrimSpace(string(raw))
		if config.Token == "" {
			return nil, errors.New("Kubernetes service-account token is empty")
		}
	}
	client := config.HTTP
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if strings.TrimSpace(config.CAFile) != "" {
			raw, readErr := os.ReadFile(config.CAFile)
			if readErr != nil {
				return nil, fmt.Errorf("read Kubernetes CA bundle: %w", readErr)
			}
			roots, rootErr := x509.SystemCertPool()
			if rootErr != nil || roots == nil {
				roots = x509.NewCertPool()
			}
			if !roots.AppendCertsFromPEM(raw) {
				return nil, errors.New("Kubernetes CA bundle contains no certificates")
			}
			transport.TLSClientConfig = &tls.Config{
				MinVersion: tls.VersionTLS12,
				RootCAs:    roots,
			}
		}
		client = &http.Client{Timeout: 30 * time.Second, Transport: transport}
	}
	return &rolloutKubernetesClient{
		config: config, base: parsed, http: client,
	}, nil
}

func (c *rolloutKubernetesClient) applyAssignmentConfig(
	ctx context.Context,
	assignment DeploymentAssignment,
) error {
	raw, err := json.Marshal(assignment)
	if err != nil {
		return err
	}
	images, err := json.Marshal(assignment.ImageDigests)
	if err != nil {
		return err
	}
	body := map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{
			"name":      assignmentConfigName(assignment.CohortID),
			"namespace": c.config.Namespace,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "wolf-rollout",
				"wolf.dev/scanner-cohort":      assignment.CohortName,
			},
		},
		"data": map[string]string{
			"assignment.json": string(raw), "release_id": assignment.ReleaseID,
			"manifest_digest":    assignment.ManifestDigest,
			"image_digests.json": string(images), "operation_id": assignment.OperationID,
			"control": "resumed",
		},
	}
	return c.request(
		ctx, http.MethodPatch,
		"/api/v1/namespaces/"+url.PathEscape(c.config.Namespace)+
			"/configmaps/"+url.PathEscape(assignmentConfigName(assignment.CohortID))+
			"?fieldManager=wolf-rollout&force=true",
		"application/apply-patch+yaml", body, nil,
	)
}

type kubernetesDeployment struct {
	Metadata struct {
		Name        string            `json:"name"`
		Generation  int64             `json:"generation"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Replicas int `json:"replicas"`
		Template struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			Spec struct {
				Containers []struct {
					Name string `json:"name"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		ObservedGeneration int64 `json:"observedGeneration"`
		UpdatedReplicas    int   `json:"updatedReplicas"`
		AvailableReplicas  int   `json:"availableReplicas"`
	} `json:"status"`
}

func (d kubernetesDeployment) readyFor(assignment DeploymentAssignment) bool {
	annotations := d.Spec.Template.Metadata.Annotations
	return annotations["wolf.dev/scanner-release"] == assignment.ReleaseID &&
		annotations["wolf.dev/scanner-manifest-digest"] == assignment.ManifestDigest &&
		annotations["wolf.dev/scanner-assignment-operation"] == assignment.OperationID &&
		d.Metadata.Generation <= d.Status.ObservedGeneration &&
		d.Status.UpdatedReplicas == d.Spec.Replicas &&
		d.Status.AvailableReplicas == d.Spec.Replicas
}

func (c *rolloutKubernetesClient) listCohortDeployments(
	ctx context.Context,
	cohort string,
) ([]kubernetesDeployment, error) {
	var response struct {
		Items []kubernetesDeployment `json:"items"`
	}
	selector := url.QueryEscape("wolf.dev/scanner-cohort=" + cohort)
	err := c.request(
		ctx, http.MethodGet,
		"/apis/apps/v1/namespaces/"+url.PathEscape(c.config.Namespace)+
			"/deployments?labelSelector="+selector,
		"", nil, &response,
	)
	return response.Items, err
}

func (c *rolloutKubernetesClient) patchDeployment(
	ctx context.Context,
	deployment kubernetesDeployment,
	assignment DeploymentAssignment,
) error {
	containers := make([]map[string]any, 0, len(deployment.Spec.Template.Spec.Containers))
	for _, container := range deployment.Spec.Template.Spec.Containers {
		containers = append(containers, map[string]any{
			"name": container.Name,
			"env": []map[string]string{
				{"name": "WOLF_SCANNER_RELEASE_ID", "value": assignment.ReleaseID},
				{"name": "WOLF_SCANNER_RELEASE_MANIFEST_DIGEST", "value": assignment.ManifestDigest},
				{"name": "WOLF_SCANNER_ASSIGNMENT_OPERATION_ID", "value": assignment.OperationID},
			},
		})
	}
	patch := map[string]any{
		"spec": map[string]any{"template": map[string]any{
			"metadata": map[string]any{"annotations": map[string]string{
				"wolf.dev/scanner-release":              assignment.ReleaseID,
				"wolf.dev/scanner-manifest-digest":      assignment.ManifestDigest,
				"wolf.dev/scanner-assignment-operation": assignment.OperationID,
			}},
			"spec": map[string]any{"containers": containers},
		}},
	}
	return c.request(
		ctx, http.MethodPatch,
		"/apis/apps/v1/namespaces/"+url.PathEscape(c.config.Namespace)+
			"/deployments/"+url.PathEscape(deployment.Metadata.Name),
		"application/strategic-merge-patch+json", patch, nil,
	)
}

func (c *rolloutKubernetesClient) readAssignmentConfig(
	ctx context.Context,
	cohortID string,
) (DeploymentAssignment, string, error) {
	var response struct {
		Data map[string]string `json:"data"`
	}
	if err := c.request(
		ctx, http.MethodGet,
		"/api/v1/namespaces/"+url.PathEscape(c.config.Namespace)+
			"/configmaps/"+url.PathEscape(assignmentConfigName(cohortID)),
		"", nil, &response,
	); err != nil {
		return DeploymentAssignment{}, "", err
	}
	var assignment DeploymentAssignment
	if err := json.Unmarshal([]byte(response.Data["assignment.json"]), &assignment); err != nil {
		return DeploymentAssignment{}, "", err
	}
	return assignment, response.Data["control"], nil
}

func (c *rolloutKubernetesClient) patchAssignmentControl(
	ctx context.Context,
	cohortID, state string,
) error {
	return c.request(
		ctx, http.MethodPatch,
		"/api/v1/namespaces/"+url.PathEscape(c.config.Namespace)+
			"/configmaps/"+url.PathEscape(assignmentConfigName(cohortID)),
		"application/merge-patch+json",
		map[string]any{"data": map[string]string{"control": state}}, nil,
	)
}

func (c *rolloutKubernetesClient) prepareImages(
	ctx context.Context,
	operationID string,
	plan DeploymentPlan,
) (CacheVerification, error) {
	name := "wolf-prepull-" + strings.TrimPrefix(
		digestSynthetic([]byte(operationID)), "sha256:",
	)[:20]
	keys := sortedDeploymentKeys(plan.ImageReferences)
	containerToKey := make(map[string]string, len(keys))
	containers := make([]map[string]any, 0, len(keys))
	for index, key := range keys {
		expectedDigest, err := immutableReferenceDigest(plan.ImageReferences[key])
		if err != nil || expectedDigest != plan.ImageDigests[key] {
			return CacheVerification{}, fmt.Errorf(
				"Kubernetes image %q reference/digest binding is invalid",
				key,
			)
		}
		containerName := fmt.Sprintf("image-%d", index)
		containerToKey[containerName] = key
		containers = append(containers, map[string]any{
			"name": containerName, "image": plan.ImageReferences[key],
			"imagePullPolicy": "Always",
			"command":         []string{"/bin/sh", "-c", "exit 0"},
		})
	}
	pod := map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{
			"name": name, "namespace": c.config.Namespace,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "wolf-rollout",
				"wolf.dev/prepull-operation":   name,
			},
		},
		"spec": map[string]any{
			"restartPolicy": "Never", "containers": containers,
		},
	}
	err := c.request(
		ctx, http.MethodPost,
		"/api/v1/namespaces/"+url.PathEscape(c.config.Namespace)+"/pods",
		"application/json", pod, nil,
	)
	if err != nil {
		return CacheVerification{}, err
	}
	defer func() {
		_ = c.request(
			context.WithoutCancel(ctx), http.MethodDelete,
			"/api/v1/namespaces/"+url.PathEscape(c.config.Namespace)+
				"/pods/"+url.PathEscape(name),
			"application/json", map[string]any{
				"apiVersion": "v1", "kind": "DeleteOptions",
				"gracePeriodSeconds": 0,
			}, nil,
		)
	}()
	timeout := c.config.PullTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	poll := c.config.PollInterval
	if poll <= 0 {
		poll = time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		var status struct {
			Status struct {
				ContainerStatuses []struct {
					Name    string `json:"name"`
					ImageID string `json:"imageID"`
					State   struct {
						Waiting *struct {
							Reason string `json:"reason"`
						} `json:"waiting"`
					} `json:"state"`
				} `json:"containerStatuses"`
			} `json:"status"`
		}
		if err := c.request(
			ctx, http.MethodGet,
			"/api/v1/namespaces/"+url.PathEscape(c.config.Namespace)+
				"/pods/"+url.PathEscape(name),
			"", nil, &status,
		); err != nil {
			return CacheVerification{}, err
		}
		verified := make(map[string]string, len(keys))
		for _, container := range status.Status.ContainerStatuses {
			key, exists := containerToKey[container.Name]
			if !exists {
				continue
			}
			if container.State.Waiting != nil &&
				(container.State.Waiting.Reason == "ImagePullBackOff" ||
					container.State.Waiting.Reason == "ErrImagePull") {
				return CacheVerification{}, fmt.Errorf(
					"Kubernetes image pull failed for %q", key,
				)
			}
			if container.ImageID == "" {
				if container.State.Waiting != nil {
					continue
				}
				return CacheVerification{}, fmt.Errorf(
					"Kubernetes image pull for %q returned no imageID",
					key,
				)
			}
			actualDigest, err := kubernetesImageIDDigest(container.ImageID)
			if err != nil {
				return CacheVerification{}, fmt.Errorf(
					"Kubernetes image pull for %q returned invalid imageID: %w",
					key, err,
				)
			}
			expectedDigest := plan.ImageDigests[key]
			if actualDigest != expectedDigest {
				return CacheVerification{}, fmt.Errorf(
					"Kubernetes image pull digest mismatch for %q: got %s, expected %s",
					key, actualDigest, expectedDigest,
				)
			}
			verified[key] = expectedDigest
		}
		if len(verified) == len(keys) {
			return CacheVerification{
				Digests: verified, VerifiedAt: time.Now().UTC(),
			}, nil
		}
		if !time.Now().Before(deadline) {
			return CacheVerification{}, errors.New("Kubernetes image pre-pull timed out")
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return CacheVerification{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func immutableReferenceDigest(value string) (string, error) {
	if strings.Count(value, "@") != 1 {
		return "", errors.New("immutable image reference must contain one digest")
	}
	repository, digest, ok := strings.Cut(value, "@")
	if !ok || repository == "" || !validSyntheticDigest(digest) {
		return "", errors.New("immutable image reference digest is invalid")
	}
	return digest, nil
}

func kubernetesImageIDDigest(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("imageID is empty")
	}
	for _, prefix := range []string{
		"docker-pullable://", "docker://", "containerd://", "cri-o://",
	} {
		if strings.HasPrefix(value, prefix) {
			value = strings.TrimPrefix(value, prefix)
			break
		}
	}
	if strings.Contains(value, "://") {
		return "", errors.New("imageID has an unsupported runtime prefix")
	}
	switch strings.Count(value, "@") {
	case 0:
		if !validSyntheticDigest(value) {
			return "", errors.New("imageID has no terminal sha256 digest")
		}
		return value, nil
	case 1:
		repository, digest, ok := strings.Cut(value, "@")
		if !ok || repository == "" || !validSyntheticDigest(digest) {
			return "", errors.New("imageID repository digest is invalid")
		}
		return digest, nil
	default:
		return "", errors.New("imageID contains ambiguous digests")
	}
}

func (c *rolloutKubernetesClient) request(
	ctx context.Context,
	method, requestPath, contentType string,
	body, output any,
) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	target := *c.base
	target.Path = path.Clean("/" + strings.TrimPrefix(requestPath, "/"))
	if query := strings.SplitN(requestPath, "?", 2); len(query) == 2 {
		target.Path = path.Clean("/" + strings.TrimPrefix(query[0], "/"))
		target.RawQuery = query[1]
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return err
	}
	if c.config.Token != "" {
		request.Header.Set("Authorization", "Bearer "+c.config.Token)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, kubernetesResponseLimit+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > kubernetesResponseLimit {
		return errors.New("Kubernetes rollout API response exceeds limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Kubernetes rollout API %s %s returned %d: %s",
			method, target.Path, response.StatusCode, strings.TrimSpace(string(raw)))
	}
	if output != nil && len(raw) != 0 {
		if err := json.Unmarshal(raw, output); err != nil {
			return err
		}
	}
	return nil
}

// requestBytes is deliberately narrow and is used by qualification to inspect
// Kubernetes subresources such as Pod logs that do not return JSON. It keeps
// the same authentication, bounded-response, and status validation as request.
func (c *rolloutKubernetesClient) requestBytes(
	ctx context.Context,
	method, requestPath string,
) ([]byte, error) {
	target := *c.base
	target.Path = path.Clean("/" + strings.TrimPrefix(requestPath, "/"))
	if query := strings.SplitN(requestPath, "?", 2); len(query) == 2 {
		target.Path = path.Clean("/" + strings.TrimPrefix(query[0], "/"))
		target.RawQuery = query[1]
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), nil)
	if err != nil {
		return nil, err
	}
	if c.config.Token != "" {
		request.Header.Set("Authorization", "Bearer "+c.config.Token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, kubernetesResponseLimit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > kubernetesResponseLimit {
		return nil, errors.New("Kubernetes response exceeds the configured limit")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Kubernetes API returned HTTP %d", response.StatusCode)
	}
	return raw, nil
}

func assignmentConfigName(cohortID string) string {
	return "wolf-scanner-release-" + strings.TrimPrefix(
		digestSynthetic([]byte(cohortID)), "sha256:",
	)[:20]
}

// InjectKubernetesJobAssignment binds a scanner Job to the same exact release
// manifest and image digests observed by the rollout controller.
func InjectKubernetesJobAssignment(
	job map[string]any,
	assignment DeploymentAssignment,
) error {
	if job == nil || !validSyntheticDigest(assignment.ManifestDigest) ||
		len(assignment.ImageDigests) == 0 {
		return errors.New("Kubernetes Job assignment is invalid")
	}
	metadata, ok := job["metadata"].(map[string]any)
	if !ok {
		return errors.New("Kubernetes Job metadata is missing")
	}
	spec, ok := job["spec"].(map[string]any)
	if !ok {
		return errors.New("Kubernetes Job spec is missing")
	}
	template, ok := spec["template"].(map[string]any)
	if !ok {
		return errors.New("Kubernetes Job template is missing")
	}
	templateMetadata, _ := template["metadata"].(map[string]any)
	if templateMetadata == nil {
		templateMetadata = map[string]any{}
		template["metadata"] = templateMetadata
	}
	annotations := map[string]string{
		"wolf.dev/scanner-release":              assignment.ReleaseID,
		"wolf.dev/scanner-manifest-digest":      assignment.ManifestDigest,
		"wolf.dev/scanner-assignment-operation": assignment.OperationID,
	}
	metadata["annotations"] = annotations
	templateMetadata["annotations"] = annotations
	podSpec, ok := template["spec"].(map[string]any)
	if !ok {
		return errors.New("Kubernetes Job pod spec is missing")
	}
	containers, ok := podSpec["containers"].([]any)
	if !ok || len(containers) == 0 {
		return errors.New("Kubernetes Job containers are missing")
	}
	allowed := make(map[string]struct{}, len(assignment.ImageDigests))
	for _, digest := range assignment.ImageDigests {
		allowed[digest] = struct{}{}
	}
	for _, raw := range containers {
		container, ok := raw.(map[string]any)
		if !ok {
			return errors.New("Kubernetes Job container is invalid")
		}
		image, _ := container["image"].(string)
		at := strings.LastIndexByte(image, '@')
		if at < 1 {
			return fmt.Errorf("Kubernetes Job image %q is mutable", image)
		}
		digest := image[at+1:]
		exact, referenceErr := immutableImageReference(image[:at], digest)
		if referenceErr != nil || exact != image {
			return fmt.Errorf("Kubernetes Job image %q is malformed", image)
		}
		if _, exists := allowed[digest]; !exists {
			return fmt.Errorf("Kubernetes Job image %q is outside the assigned release", image)
		}
		environment, environmentErr := appendJobEnvironment(
			container["env"], []map[string]string{
				{"name": "WOLF_SCANNER_RELEASE_ID", "value": assignment.ReleaseID},
				{"name": "WOLF_SCANNER_RELEASE_MANIFEST_DIGEST", "value": assignment.ManifestDigest},
				{"name": "WOLF_SCANNER_ASSIGNMENT_OPERATION_ID", "value": assignment.OperationID},
			},
		)
		if environmentErr != nil {
			return environmentErr
		}
		container["env"] = environment
	}
	return nil
}

func appendJobEnvironment(
	existing any,
	additions []map[string]string,
) ([]any, error) {
	protected := make(map[string]map[string]string, len(additions))
	for _, addition := range additions {
		protected[addition["name"]] = addition
	}
	normalized := make([]map[string]any, 0, len(additions))
	if values, ok := existing.([]any); ok {
		for _, value := range values {
			entry, ok := value.(map[string]any)
			if !ok {
				return nil, errors.New("Kubernetes Job environment entry is invalid")
			}
			name, _ := entry["name"].(string)
			if strings.TrimSpace(name) == "" {
				return nil, errors.New("Kubernetes Job environment name is invalid")
			}
			if _, replace := protected[name]; replace {
				continue
			}
			normalized = append(normalized, entry)
		}
	} else if existing != nil {
		return nil, errors.New("Kubernetes Job environment is invalid")
	}
	for _, addition := range additions {
		normalized = append(normalized, map[string]any{
			"name": addition["name"], "value": addition["value"],
		})
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		left, _ := normalized[i]["name"].(string)
		right, _ := normalized[j]["name"].(string)
		return left < right
	})
	result := make([]any, len(normalized))
	for index := range normalized {
		result[index] = normalized[index]
	}
	return result, nil
}

func mapsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
