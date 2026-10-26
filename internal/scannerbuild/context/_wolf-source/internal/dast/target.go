// Package dast enforces the network boundary shared by DAST scanners.
package dast

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"golang.org/x/net/http/httpguts"
)

var lookupIPs = net.DefaultResolver.LookupNetIP

func ParseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 4096 || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return nil, fmt.Errorf("DAST URL must be an absolute HTTP(S) URL without userinfo or fragment")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "metadata.google.internal" || strings.HasSuffix(host, ".metadata.google.internal") || strings.Contains(host, "%") {
		return nil, fmt.Errorf("DAST destination is blocked")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if err := checkIP(ip, true); err != nil {
			return nil, err
		}
	}
	return u, nil
}

func ValidateSettings(t *models.DASTTarget) error {
	if t.Mode == "" {
		t.Mode = "baseline"
	}
	if t.APIFormat == "" {
		t.APIFormat = "openapi"
	}
	if t.Mode != "baseline" && t.Mode != "full" && t.Mode != "api" {
		return fmt.Errorf("mode must be baseline, full, or api")
	}
	if t.APIFormat != "openapi" && t.APIFormat != "graphql" {
		return fmt.Errorf("API format must be openapi or graphql")
	}
	if t.URL == "" {
		if t.APISpecURL != "" || t.AuthSecretID != "" {
			return fmt.Errorf("a target URL is required")
		}
		return nil
	}
	u, err := ParseURL(t.URL)
	if err != nil {
		return err
	}
	if t.Mode == "api" && t.APISpecURL == "" {
		return fmt.Errorf("API mode requires a specification URL (or GraphQL endpoint)")
	}
	if t.APISpecURL != "" {
		spec, err := ParseURL(t.APISpecURL)
		if err != nil {
			return err
		}
		if origin(spec) != origin(u) {
			return fmt.Errorf("API specification must use the approved target origin (scheme, host and port)")
		}
	}
	return nil
}

func origin(u *url.URL) string { return u.Scheme + "://" + address(u) }
func address(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")), port)
}

func checkIP(ip netip.Addr, allowPrivate bool) error {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip == netip.MustParseAddr("fd00:ec2::254") {
		return fmt.Errorf("DAST destination resolves to a blocked metadata, link-local, or special-purpose address")
	}
	if (ip.IsPrivate() || ip.IsLoopback() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip)) && !allowPrivate {
		return fmt.Errorf("private-network DAST targets require explicit administrator approval")
	}
	return nil
}

// Resolve checks every answer and returns the exact IPs to dial. Callers must
// not validate and then dial the hostname (a DNS rebinding race).
func Resolve(ctx context.Context, raw string, allowPrivate bool) ([]netip.Addr, error) {
	u, err := ParseURL(raw)
	if err != nil {
		return nil, err
	}
	ips, err := lookupIPs(ctx, "ip", u.Hostname())
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("cannot resolve DAST destination")
	}
	for _, ip := range ips {
		if err := checkIP(ip, allowPrivate); err != nil {
			return nil, err
		}
	}
	return ips, nil
}

// Headers accepts a JSON object from a custom Wolf secret. Routing and proxy
// headers are forbidden; credentials cannot redirect the approved request.
func Headers(value string) (map[string]string, error) {
	var headers map[string]string
	if len(value) > 16384 || json.Unmarshal([]byte(value), &headers) != nil || len(headers) == 0 || len(headers) > 32 {
		return nil, fmt.Errorf("auth-header secret must be a JSON object of HTTP header names and values")
	}
	for k, v := range headers {
		name := http.CanonicalHeaderKey(k)
		if !httpguts.ValidHeaderFieldName(k) || !httpguts.ValidHeaderFieldValue(v) || strings.ContainsAny(v, "\r\n") {
			return nil, fmt.Errorf("auth-header secret contains an invalid HTTP header")
		}
		switch name {
		case "Host", "Connection", "Content-Length", "Transfer-Encoding", "Proxy-Authorization", "Proxy-Connection", "Upgrade", "Trailer", "Te":
			return nil, fmt.Errorf("auth-header secret contains a forbidden routing header")
		}
	}
	return headers, nil
}
