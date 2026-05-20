package apikey

import (
	"strings"
	"testing"
)

func TestGenerateProducesDistinctVerifiableTokens(t *testing.T) {
	p1, h1, pre1, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	p2, h2, _, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if p1 == p2 || h1 == h2 {
		t.Fatal("two Generate calls produced identical tokens")
	}
	if !strings.HasPrefix(p1, Prefix) {
		t.Errorf("token %q lacks %q prefix", p1, Prefix)
	}
	if !LooksLikeToken(p1) {
		t.Error("LooksLikeToken should accept a generated token")
	}
	if pre1 != p1[:8] {
		t.Errorf("prefix %q is not the first 8 chars of %q", pre1, p1)
	}
	if Hash(p1) != h1 {
		t.Error("Hash is not deterministic for the same plaintext")
	}
	if Hash(p1) == Hash(p2) {
		t.Error("distinct tokens hashed to the same value")
	}
}

func TestLooksLikeToken(t *testing.T) {
	if LooksLikeToken("eyJhbGciOiJIUzI1NiJ9.payload.sig") {
		t.Error("a JWT should not look like an API token")
	}
	if !LooksLikeToken("wolf_abc123") {
		t.Error("a wolf_-prefixed string should look like an API token")
	}
}

func TestParseScopesValidatesAndExpandsAliases(t *testing.T) {
	if _, err := ParseScopes(nil); err == nil {
		t.Error("empty scope list should be rejected")
	}
	if _, err := ParseScopes([]string{"read:nonsense"}); err == nil {
		t.Error("unknown scope should be rejected")
	}

	ro, err := ParseScopes([]string{"read-only"})
	if err != nil {
		t.Fatalf("read-only alias: %v", err)
	}
	for _, s := range ro {
		if !strings.HasPrefix(s, "read:") {
			t.Errorf("read-only expanded to non-read scope %q", s)
		}
	}
	if ro.Has(ScopeWriteScans) {
		t.Error("read-only must not grant write")
	}

	full, err := ParseScopes([]string{"full"})
	if err != nil {
		t.Fatalf("full alias: %v", err)
	}
	if !full.Has(ScopeAdmin) || !full.Has(ScopeWriteRepos) {
		t.Error("full alias must expand to every scope")
	}

	// Concrete scopes, deduplicated.
	cs, err := ParseScopes([]string{"read:scans", "read:scans", "write:scans"})
	if err != nil {
		t.Fatalf("concrete scopes: %v", err)
	}
	if len(cs) != 2 {
		t.Errorf("expected 2 deduplicated scopes, got %d: %v", len(cs), cs)
	}
}

func TestScopeSetHasImplications(t *testing.T) {
	// write implies read for the same resource.
	writer := ScopeSet{ScopeWriteScans}
	if !writer.Has(ScopeReadScans) {
		t.Error("write:scans must imply read:scans")
	}
	if writer.Has(ScopeWriteRepos) {
		t.Error("write:scans must not grant write:repos")
	}

	// admin satisfies everything.
	admin := AdminAll()
	for _, s := range AllScopes {
		if !admin.Has(s) {
			t.Errorf("admin set should satisfy %q", s)
		}
	}

	// HasAll requires every scope.
	set := ScopeSet{ScopeReadScans, ScopeWriteFindings}
	if !set.HasAll(ScopeReadScans, ScopeReadFindings) {
		t.Error("HasAll should pass when write:findings covers read:findings")
	}
	if set.HasAll(ScopeReadScans, ScopeWriteScans) {
		t.Error("HasAll should fail without write:scans")
	}
}

func TestScopeEncodeRoundTrip(t *testing.T) {
	original := ScopeSet{ScopeReadScans, ScopeWriteFindings}
	decoded := DecodeScopes(original.Encode())
	if len(decoded) != len(original) {
		t.Fatalf("round-trip changed length: %v -> %v", original, decoded)
	}
	for i := range original {
		if decoded[i] != original[i] {
			t.Errorf("round-trip mismatch at %d: %q != %q", i, decoded[i], original[i])
		}
	}
	if len(DecodeScopes("")) != 0 {
		t.Error("decoding empty string should yield an empty set")
	}
	if len(DecodeScopes("not json")) != 0 {
		t.Error("decoding invalid JSON should yield an empty set")
	}
}
