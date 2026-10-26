package dast

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// Proxy restricts scanner HTTP traffic to one approved origin, resolving and
// validating the destination on every connection, including HTTPS CONNECT.
// Its random credential is scoped to one execution and never persisted.
type Proxy struct {
	URL         string
	server      *http.Server
	transport   *http.Transport
	connections sync.Map
	requests    atomic.Int64
}

func StartProxy(ctx context.Context, t models.DASTTarget) (*Proxy, error) {
	if !t.Approved() {
		return nil, fmt.Errorf("DAST target is not approved")
	}
	if err := ValidateSettings(&t); err != nil {
		return nil, err
	}
	if _, err := Resolve(ctx, t.URL, t.AllowPrivate); err != nil {
		return nil, err
	}
	target, _ := ParseURL(t.URL)
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		return nil, err
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		_ = ln.Close()
		return nil, err
	}
	password := hex.EncodeToString(key[:])
	host := os.Getenv("WOLF_DAST_PROXY_HOST")
	if host == "" && container.DASTNetwork() != "" {
		// Docker DNS resolves this container's hostname on the DAST network.
		host, _ = os.Hostname()
	}
	if host == "" {
		host = "host.docker.internal"
	}
	p := &Proxy{URL: (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)), User: url.UserPassword("wolf", password)}).String()}
	dial := func(ctx context.Context, _, addr string) (net.Conn, error) {
		if addr != address(target) {
			return nil, fmt.Errorf("outside approved DAST origin")
		}
		ips, err := Resolve(ctx, t.URL, t.AllowPrivate)
		if err != nil {
			return nil, err
		}
		_, port, _ := net.SplitHostPort(addr)
		var conn net.Conn
		for _, ip := range ips {
			conn, err = (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, fmt.Errorf("DAST target is unreachable")
	}
	p.transport = &http.Transport{DialContext: dial, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second}
	forward := &httputil.ReverseProxy{
		Rewrite:   func(r *httputil.ProxyRequest) { r.Out.Header.Del("Proxy-Authorization"); r.Out.Host = target.Host },
		Transport: p.transport,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "DAST target is unreachable or blocked", http.StatusBadGateway)
		},
		ModifyResponse: func(_ *http.Response) error { p.requests.Add(1); return nil },
	}
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("wolf:"+password))
	p.server = &http.Server{ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Proxy-Authorization")), []byte(expected)) != 1 {
			w.Header().Set("Proxy-Authenticate", `Basic realm="wolf-dast"`)
			http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
			return
		}
		if r.Method != http.MethodConnect {
			if r.URL.User != nil || origin(r.URL) != origin(target) {
				http.Error(w, "outside approved DAST origin", http.StatusForbidden)
				return
			}
			forward.ServeHTTP(w, r)
			return
		}
		if target.Scheme != "https" || r.Host != address(target) {
			http.Error(w, "outside approved DAST origin", http.StatusForbidden)
			return
		}
		upstream, err := dial(r.Context(), "tcp", r.Host)
		if err != nil {
			http.Error(w, "DAST target is unreachable or blocked", http.StatusBadGateway)
			return
		}
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		p.connections.Store(client, upstream)
		defer func() { p.connections.Delete(client); _ = client.Close(); _ = upstream.Close() }()
		_ = client.SetDeadline(time.Now().Add(15 * time.Minute))
		_ = upstream.SetDeadline(time.Now().Add(15 * time.Minute))
		if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			return
		}
		p.requests.Add(1)
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, buffered); _ = upstream.Close(); close(done) }()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		<-done
	})}
	go func() { _ = p.server.Serve(ln) }()
	return p, nil
}

func (p *Proxy) Close() {
	_ = p.server.Close()
	p.transport.CloseIdleConnections()
	p.connections.Range(func(k, v any) bool { _ = k.(net.Conn).Close(); _ = v.(net.Conn).Close(); return true })
}

func (p *Proxy) ReachedTarget() bool { return p.requests.Load() > 0 }
