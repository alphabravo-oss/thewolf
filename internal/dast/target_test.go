package dast

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestTargetBoundary(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "ftp://example.com", "http://user:pass@example.com", "http://169.254.169.254/", "http://[fe80::1]/", "http://[fd00:ec2::254]/", "http://[::ffff:169.254.1.1]/", "http://metadata.google.internal./", "http://example.com/#secret"} {
		if _, err := ParseURL(raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.0.1", "::1", "fd00::1", "100.64.0.1"} {
		if checkIP(netip.MustParseAddr(ip), false) == nil || checkIP(netip.MustParseAddr(ip), true) != nil {
			t.Errorf("private approval rule for %s", ip)
		}
	}
	for _, ip := range []string{"169.254.1.1", "fe80::1", "fd00:ec2::254", "0.0.0.0", "224.0.0.1", "::ffff:169.254.169.254"} {
		if checkIP(netip.MustParseAddr(ip), true) == nil {
			t.Errorf("approved blocked IP %s", ip)
		}
	}
	old := lookupIPs
	t.Cleanup(func() { lookupIPs = old })
	lookupIPs = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("169.254.169.254")}, nil
	}
	if _, err := Resolve(t.Context(), "https://rebinding.example", true); err == nil {
		t.Fatal("mixed DNS answer bypassed metadata block")
	}
	for _, secret := range []string{`{"Authorization":"ok\r\nHost: bad"}`, `{"Host":"internal"}`, `{"Proxy-Authorization":"secret"}`, `{"Connection":"Authorization"}`, `null`} {
		if _, err := Headers(secret); err == nil {
			t.Errorf("accepted unsafe headers %s", secret)
		}
	}
	if _, err := Headers(`{"Authorization":"Bearer test","X-API-Key":"test"}`); err != nil {
		t.Fatal(err)
	}
	target := models.DASTTarget{URL: "https://example.com", Mode: "api", APISpecURL: "https://other.example.com/openapi.json"}
	if ValidateSettings(&target) == nil {
		t.Fatal("cross-origin spec accepted")
	}
}

func TestProxyScopeAndDNSRebinding(t *testing.T) {
	var gotHost string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.Header().Set("Location", "http://169.254.169.254/latest/meta-data/")
		w.WriteHeader(http.StatusFound)
	}))
	defer target.Close()
	p, err := StartProxy(t.Context(), models.DASTTarget{URL: target.URL, Mode: "baseline", ApprovedBy: "admin", AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	proxyURL, _ := url.Parse(p.URL)
	proxyURL.Host = "127.0.0.1:" + proxyURL.Port()
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !p.ReachedTarget() {
		t.Fatalf("redirect escaped scope or target not reached: %d", resp.StatusCode)
	}
	u, _ := url.Parse(target.URL)
	if gotHost != u.Host {
		t.Fatalf("wrong upstream Host: %s", gotHost)
	}
	// A fresh connection re-resolves and rejects even an approved private
	// destination if its DNS changes to metadata. Never dial by hostname.
	transport.CloseIdleConnections()
	p.transport.CloseIdleConnections()
	old := lookupIPs
	t.Cleanup(func() { lookupIPs = old })
	lookupIPs = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
	}
	resp, err = client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("DNS rebinding allowed: %d", resp.StatusCode)
	}
}
