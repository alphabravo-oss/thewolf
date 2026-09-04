package authprovider

import (
	"context"
	"sync"
)

// Provider is a login source. Community uses local email/password.
// Enterprise registers OIDC/SAML/LDAP implementations on this interface.
type Provider interface {
	Name() string
	Authenticate(ctx context.Context, principal, secret string) (userID string, err error)
}

// Redirector is an optional SSO flow (OIDC/SAML). Password providers omit it.
type Redirector interface {
	AuthorizationURL(state, nonce, redirectURI string) (string, error)
	Redeem(ctx context.Context, code, redirectURI string) (email string, err error)
}

// Identity is the SSO redeem result. Groups are IdP group names used to map Wolf roles.
type Identity struct {
	Email  string
	Groups []string
}

// GroupedRedirector is optional. SSO uses it when the IdP returns groups.
type GroupedRedirector interface {
	RedeemIdentity(ctx context.Context, code, redirectURI string) (Identity, error)
}

// Local is the Community password provider name. The real verifier stays in
// internal/auth; this package is the registration contract.
const (
	Local        = "local"
	KindPassword = "password"
	KindRedirect = "redirect"
)

type Info struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Default is the process-wide provider registry. Community lists local when
// empty; the Enterprise overlay Register()s additional providers.
var Default = New()

type Registry struct {
	mu        sync.Mutex
	providers []Provider
}

func New() *Registry { return &Registry{} }

func (r *Registry) Register(p Provider) {
	if p == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers = append(r.providers, p)
}

func (r *Registry) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.providers) == 0 {
		return []string{Local}
	}
	out := make([]string, 0, len(r.providers))
	seen := map[string]struct{}{}
	for _, p := range r.providers {
		n := p.Name()
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	if _, ok := seen[Local]; !ok {
		out = append([]string{Local}, out...)
	}
	return out
}

func (r *Registry) Lookup(name string) Provider {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.providers {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

func (r *Registry) Infos() []Info {
	out := []Info{{Name: Local, Kind: KindPassword}}
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[string]struct{}{Local: {}}
	for _, p := range r.providers {
		n := p.Name()
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		kind := KindPassword
		if _, ok := p.(Redirector); ok {
			kind = KindRedirect
		}
		out = append(out, Info{Name: n, Kind: kind})
	}
	return out
}
