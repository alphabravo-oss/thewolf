package middleware

import (
	"context"
	"net/http"
	"net/netip"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

type proxyContextKey struct{}
type proxyRequest struct {
	ip    string
	https bool
}

// ForwardedHeaders accepts proxy headers only from explicitly trusted peer
// addresses/CIDRs. An empty or invalid configuration trusts no proxy. The
// socket peer stays in RemoteAddr; audit and throttling use ClientIP.
func ForwardedHeaders(config string) func(http.Handler) http.Handler {
	var trusted []netip.Prefix
	if strings.TrimSpace(config) != "" {
		for _, value := range strings.Split(config, ",") {
			value = strings.TrimSpace(value)
			prefix, err := netip.ParsePrefix(value)
			if err != nil {
				if addr, addrErr := netip.ParseAddr(value); addrErr == nil {
					prefix = netip.PrefixFrom(addr, addr.BitLen())
					err = nil
				}
			}
			if err != nil {
				wolflog.Warn().Msg("invalid WOLF_TRUSTED_PROXIES; forwarded headers will not be trusted")
				trusted = nil
				break
			}
			if prefix.Addr().Is4In6() && prefix.Bits() >= 96 {
				prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
			}
			trusted = append(trusted, prefix.Masked())
		}
	}
	isTrusted := func(addr netip.Addr) bool {
		for _, prefix := range trusted {
			if prefix.Contains(addr.Unmap()) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Internal dispatch (MCP) retains this server-owned context. Its
			// synthetic headers must not replace the validated transport.
			if _, ok := r.Context().Value(proxyContextKey{}).(proxyRequest); ok {
				next.ServeHTTP(w, r)
				return
			}
			peer := rateLimitRemoteKey(r.RemoteAddr)
			addr, err := netip.ParseAddr(peer)
			if err == nil {
				peer = addr.Unmap().String()
			}
			resolved := proxyRequest{ip: peer}
			if err == nil && isTrusted(addr) {
				protocols := r.Header.Values("X-Forwarded-Proto")
				resolved.https = len(protocols) == 1 && strings.EqualFold(strings.TrimSpace(protocols[0]), "https")
				if forwarded := r.Header.Values("X-Forwarded-For"); len(forwarded) > 0 {
					hops := strings.Split(strings.Join(forwarded, ","), ",")
					current := addr
					for i := len(hops) - 1; i >= 0 && isTrusted(current); i-- {
						hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
						if err != nil {
							resolved.ip = peer
							break
						}
						current = hop.Unmap()
						resolved.ip = current.String()
					}
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), proxyContextKey{}, resolved)))
		})
	}
}

// ClientIP returns the validated proxy-chain address, or the actual socket peer
// when proxy middleware has not run. Raw forwarding headers are never a fallback.
func ClientIP(r *http.Request) string {
	if resolved, ok := r.Context().Value(proxyContextKey{}).(proxyRequest); ok {
		return resolved.ip
	}
	peer := rateLimitRemoteKey(r.RemoteAddr)
	if addr, err := netip.ParseAddr(peer); err == nil {
		return addr.Unmap().String()
	}
	return peer
}

// IsHTTPS accepts actual TLS or an HTTPS assertion from a configured proxy.
func IsHTTPS(r *http.Request) bool {
	resolved, _ := r.Context().Value(proxyContextKey{}).(proxyRequest)
	return r.TLS != nil || resolved.https
}
