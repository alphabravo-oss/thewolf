package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scannerregistry"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

// releaseRegistryClientFactory resolves only opaque registry secret
// references. The resulting errors never contain decrypted credential data.
type releaseRegistryClientFactory struct {
	store db.Store
}

func (f releaseRegistryClientFactory) Single(
	ctx context.Context,
	target *scannerrelease.RegistryTarget,
) (scannerregistry.Client, string, error) {
	client, host, err := f.build(ctx, target)
	return client, host, err
}

func (f releaseRegistryClientFactory) Pair(
	ctx context.Context,
	source, destination *scannerrelease.RegistryTarget,
) (scannerregistry.Client, string, string, error) {
	sourceClient, sourceHost, err := f.build(ctx, source)
	if err != nil {
		return scannerregistry.Client{}, "", "", err
	}
	destinationClient, destinationHost, err := f.build(ctx, destination)
	if err != nil {
		return scannerregistry.Client{}, "", "", err
	}
	if sourceHost == destinationHost {
		return scannerregistry.Client{}, "", "", errors.New("source and destination registry origins must differ")
	}
	for host, endpoint := range destinationClient.Endpoints {
		sourceClient.Endpoints[host] = endpoint
	}
	for host, allowed := range destinationClient.TokenHosts {
		sourceClient.TokenHosts[host] = allowed
	}
	authorizations := map[string]string{}
	for _, configured := range []struct {
		target *scannerrelease.RegistryTarget
		host   string
	}{
		{source, sourceHost}, {destination, destinationHost},
	} {
		value, authErr := f.authorization(ctx, configured.target, configured.host)
		if authErr != nil {
			return scannerregistry.Client{}, "", "", authErr
		}
		authorizations[configured.host] = value
	}
	sourceClient.Credentials = scannerregistry.CredentialProviderFunc(
		func(_ context.Context, registry string) (string, error) {
			value, exists := authorizations[registry]
			if !exists {
				return "", errors.New("registry credential requested for an unconfigured origin")
			}
			return value, nil
		},
	)
	return sourceClient, sourceHost, destinationHost, nil
}

func (f releaseRegistryClientFactory) build(
	ctx context.Context,
	target *scannerrelease.RegistryTarget,
) (scannerregistry.Client, string, error) {
	if target == nil {
		return scannerregistry.Client{}, "", errors.New("registry target is required")
	}
	origin, host, err := releaseRegistryOrigin(target.Host)
	if err != nil {
		return scannerregistry.Client{}, "", err
	}
	tokenHosts, err := releaseRegistryTokenHosts(target, host)
	if err != nil {
		return scannerregistry.Client{}, "", err
	}
	authorization, err := f.authorization(ctx, target, host)
	if err != nil {
		return scannerregistry.Client{}, "", err
	}
	client := scannerregistry.Client{
		Endpoints: map[string]scannerregistry.Endpoint{
			host: {BaseURL: origin},
		},
		TokenHosts: map[string][]string{host: tokenHosts},
	}
	client.Credentials = scannerregistry.CredentialProviderFunc(
		func(_ context.Context, registry string) (string, error) {
			if registry != host {
				return "", errors.New("registry credential requested for an unconfigured origin")
			}
			return authorization, nil
		},
	)
	return client, host, nil
}

func releaseRegistryOrigin(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("registry host is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", "", errors.New("registry host must be an HTTP(S) origin without credentials or a path")
	}
	return parsed.Scheme + "://" + parsed.Host, parsed.Host, nil
}

func releaseRegistryTokenHosts(
	target *scannerrelease.RegistryTarget,
	registryHost string,
) ([]string, error) {
	var policy struct {
		TokenHosts []string `json:"token_hosts"`
	}
	if strings.TrimSpace(target.PlatformPolicyJSON) != "" {
		if err := json.Unmarshal([]byte(target.PlatformPolicyJSON), &policy); err != nil {
			return nil, errors.New("registry platform policy is invalid")
		}
	}
	switch strings.ToLower(registryHost) {
	case "docker.io", "registry-1.docker.io", "index.docker.io":
		policy.TokenHosts = append(policy.TokenHosts, "auth.docker.io")
	}
	seen := make(map[string]struct{}, len(policy.TokenHosts))
	var result []string
	for _, value := range policy.TokenHosts {
		value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
		if value == "" || strings.ContainsAny(value, "/?#@") {
			return nil, errors.New("registry token host must be a hostname with an optional port")
		}
		parsed, parseErr := url.Parse("https://" + value)
		if parseErr != nil || parsed.Host != value || parsed.Hostname() == "" {
			return nil, errors.New("registry token host is invalid")
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func (f releaseRegistryClientFactory) authorization(
	ctx context.Context,
	target *scannerrelease.RegistryTarget,
	host string,
) (string, error) {
	if target.SecretReference == "" {
		return "", nil
	}
	if f.store == nil {
		return "", errors.New("registry credential store is unavailable")
	}
	secretID := strings.TrimPrefix(target.SecretReference, "secret:")
	credential, err := f.store.GetSecretByID(ctx, secretID)
	if err != nil {
		return "", errors.New("registry credential reference was not found")
	}
	if !releaseSecretAllowsHost(credential, host) {
		return "", errors.New("registry credential is not authorized for this host")
	}
	value, err := secrets.Decrypt(credential.EncryptedValue)
	if err != nil {
		return "", errors.New("registry credential could not be decrypted")
	}
	if strings.ContainsAny(value, "\r\n") {
		return "", errors.New("registry credential contains an invalid newline")
	}
	var username string
	switch credential.KeyType {
	case models.KeyTypeGitHTTPS:
		var metadata struct {
			Username string `json:"username"`
		}
		_ = json.Unmarshal([]byte(credential.MetadataJSON), &metadata)
		username = strings.TrimSpace(metadata.Username)
	case models.KeyTypeDockerHubToken:
		username = strings.TrimSpace(credential.KeyName)
	default:
		return "", fmt.Errorf("credential type %q is not supported for OCI registry authentication", credential.KeyType)
	}
	if username == "" || strings.ContainsAny(username, ":\r\n") {
		return "", errors.New("registry credential has no valid username")
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+value)), nil
}

func releaseSecretAllowsHost(credential *models.Secret, host string) bool {
	var allowed []string
	if json.Unmarshal([]byte(credential.AllowedHosts), &allowed) == nil {
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		for _, value := range allowed {
			value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
			if host == value ||
				(strings.HasPrefix(value, "*.") &&
					strings.HasSuffix(host, strings.TrimPrefix(value, "*")) &&
					host != strings.TrimPrefix(value, "*.")) {
				return true
			}
		}
	}
	if credential.KeyType == models.KeyTypeDockerHubToken {
		switch strings.ToLower(host) {
		case "docker.io", "registry-1.docker.io", "index.docker.io":
			return true
		}
	}
	return false
}
