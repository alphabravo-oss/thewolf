// Package registryauth validates OCI registry Bearer challenges and fetches
// bounded token responses without allowing a registry to select an arbitrary
// network destination.
package registryauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const (
	tokenRequestTimeout = 10 * time.Second
	maxTokenResponse    = 1 << 20
	maxTokenLength      = 64 << 10
)

// LookupIPFunc resolves a hostname for private-address validation.
type LookupIPFunc func(context.Context, string) ([]net.IP, error)

// FetchOptions describes the registry boundary that an authentication
// challenge is allowed to use.
type FetchOptions struct {
	Client            *http.Client
	Registry          string
	Repository        string
	LookupIP          LookupIPFunc
	AllowLoopbackHTTP bool
}

// FetchBearerToken validates a registry challenge, follows only validated
// redirects, and returns one bounded Bearer token.
func FetchBearerToken(ctx context.Context, challenge string, opts FetchOptions) (string, error) {
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return "", fmt.Errorf("registry requires unsupported authentication challenge")
	}
	params := parseAuthChallenge(challenge[len("Bearer "):])
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("registry authentication challenge has no realm")
	}
	tokenURL, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("parse registry authentication realm: %w", err)
	}
	query := tokenURL.Query()
	if service := params["service"]; service != "" {
		query.Set("service", service)
	}
	if scope := params["scope"]; scope != "" {
		query.Set("scope", scope)
	} else {
		query.Set("scope", "repository:"+opts.Repository+":pull")
	}
	tokenURL.RawQuery = query.Encode()

	lookupIP := opts.LookupIP
	if lookupIP == nil {
		lookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip", host)
		}
	}
	policy := realmPolicy{
		registry:          opts.Registry,
		lookupIP:          lookupIP,
		allowLoopbackHTTP: opts.AllowLoopbackHTTP,
	}
	if err := policy.validate(ctx, tokenURL); err != nil {
		return "", err
	}

	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: tokenRequestTimeout}
	}
	authClient := *client
	previousRedirect := authClient.CheckRedirect
	authClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := policy.validate(req.Context(), req.URL); err != nil {
			return err
		}
		if previousRedirect != nil {
			return previousRedirect(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 registry authentication redirects")
		}
		return nil
	}

	requestCtx, cancel := context.WithTimeout(ctx, tokenRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, tokenURL.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := authClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET registry token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("GET registry token: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponse+1))
	if err != nil {
		return "", fmt.Errorf("read registry token response: %w", err)
	}
	if len(data) > maxTokenResponse {
		return "", fmt.Errorf("registry token response exceeds %d-byte limit", maxTokenResponse)
	}
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("decode registry token response: %w", err)
	}
	token := payload.Token
	if token == "" {
		token = payload.AccessToken
	}
	if token == "" {
		return "", fmt.Errorf("registry token response has no token")
	}
	if len(token) > maxTokenLength {
		return "", fmt.Errorf("registry token exceeds %d-byte limit", maxTokenLength)
	}
	return token, nil
}

type realmPolicy struct {
	registry          string
	lookupIP          LookupIPFunc
	allowLoopbackHTTP bool
}

func (p realmPolicy) validate(ctx context.Context, candidate *url.URL) error {
	if candidate == nil || candidate.Host == "" || candidate.Opaque != "" {
		return fmt.Errorf("registry authentication realm must be an absolute URL")
	}
	if candidate.User != nil {
		return fmt.Errorf("registry authentication realm must not contain user information")
	}
	host := strings.ToLower(strings.TrimSuffix(candidate.Hostname(), "."))
	if host == "" {
		return fmt.Errorf("registry authentication realm has no host")
	}
	explicitLoopback := isExplicitLoopbackHost(host)
	switch strings.ToLower(candidate.Scheme) {
	case "https":
	case "http":
		if !p.allowLoopbackHTTP || !explicitLoopback {
			return fmt.Errorf("registry authentication realm must use HTTPS")
		}
	default:
		return fmt.Errorf("registry authentication realm must use HTTPS")
	}
	if !authAuthorityAllowed(p.registry, candidate) {
		return fmt.Errorf(
			"registry authentication host %q is not allowed for registry %q",
			candidate.Host,
			p.registry,
		)
	}
	addresses, err := resolveRealmAddresses(ctx, host, p.lookupIP)
	if err != nil {
		return fmt.Errorf("resolve registry authentication host %q: %w", host, err)
	}
	for _, address := range addresses {
		if isUnsafeAddress(address) && !(p.allowLoopbackHTTP && explicitLoopback && address.IsLoopback()) {
			return fmt.Errorf("registry authentication host %q resolves to a private or non-routable address", host)
		}
	}
	return nil
}

func authAuthorityAllowed(registry string, candidate *url.URL) bool {
	registryHost, registryPort := splitAuthority(registry, "443")
	authHost := strings.ToLower(strings.TrimSuffix(candidate.Hostname(), "."))
	authPort := candidate.Port()
	if authPort == "" {
		authPort = "443"
	}
	if authHost == registryHost && authPort == registryPort {
		return true
	}
	if authPort != "443" {
		return false
	}
	switch registryHost {
	case "docker.io", "index.docker.io", "registry-1.docker.io":
		return authHost == "auth.docker.io"
	case "ghcr.io":
		return authHost == "ghcr.io"
	default:
		return false
	}
}

func splitAuthority(authority, defaultPort string) (string, string) {
	parsed, err := url.Parse("https://" + authority)
	if err != nil {
		return "", ""
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	port := parsed.Port()
	if port == "" {
		port = defaultPort
	}
	return host, port
}

func resolveRealmAddresses(ctx context.Context, host string, lookup LookupIPFunc) ([]netip.Addr, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{address.Unmap()}, nil
	}
	resolved, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	addresses := make([]netip.Addr, 0, len(resolved))
	for _, ip := range resolved {
		if address, ok := netip.AddrFromSlice(ip); ok {
			addresses = append(addresses, address.Unmap())
		}
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("no IP addresses returned")
	}
	return addresses, nil
}

func isExplicitLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}

func isUnsafeAddress(address netip.Addr) bool {
	if !address.IsValid() || !address.IsGlobalUnicast() ||
		address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() {
		return true
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func parseAuthChallenge(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok {
			out[strings.ToLower(key)] = strings.Trim(value, `"`)
		}
	}
	return out
}
