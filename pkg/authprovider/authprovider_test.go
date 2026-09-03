package authprovider

import (
	"context"
	"testing"
)

func TestNamesDefaultLocal(t *testing.T) {
	r := New()
	got := r.Names()
	if len(got) != 1 || got[0] != Local {
		t.Fatalf("Names() = %#v", got)
	}
}

type stub struct{ name string }

func (s stub) Name() string { return s.name }
func (stub) Authenticate(context.Context, string, string) (string, error) {
	return "", nil
}

func TestRegisterKeepsLocal(t *testing.T) {
	r := New()
	r.Register(stub{name: "oidc"})
	got := r.Names()
	if len(got) != 2 || got[0] != Local || got[1] != "oidc" {
		t.Fatalf("Names() = %#v", got)
	}
}

type redirStub struct{ stub }

func (redirStub) AuthorizationURL(state, nonce, redirectURI string) (string, error) {
	return "https://idp.example/auth?state=" + state, nil
}
func (redirStub) Redeem(context.Context, string, string) (string, error) {
	return "user@example.com", nil
}

func TestInfosMarksRedirect(t *testing.T) {
	r := New()
	r.Register(redirStub{stub{name: "oidc"}})
	infos := r.Infos()
	if len(infos) != 2 || infos[0].Kind != KindPassword || infos[1].Kind != KindRedirect {
		t.Fatalf("Infos() = %#v", infos)
	}
	if r.Lookup("oidc") == nil || r.Lookup("missing") != nil {
		t.Fatal("Lookup")
	}
}
