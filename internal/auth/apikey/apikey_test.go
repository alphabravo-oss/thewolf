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

	// read-write expands to conventional read+write scopes but not privileged
	// release approval, registry management, operation, or administration.
	rw, err := ParseScopes([]string{"read-write"})
	if err != nil {
		t.Fatalf("read-write alias: %v", err)
	}
	if !rw.Has(ScopeWriteRepos) || !rw.Has(ScopeReadScans) {
		t.Error("read-write must grant read and write scopes")
	}
	for _, s := range rw {
		if !strings.HasPrefix(s, "read:") && !strings.HasPrefix(s, "write:") {
			t.Errorf("read-write expanded to privileged non-read/write scope %q", s)
		}
	}
	if rw.Has(ScopeOperateScannerSupplyChain) ||
		rw.Has(ScopeApproveScannerReleases) ||
		rw.Has(ScopeManageScannerRegistries) ||
		rw.Has(ScopeAdminScannerSupplyChain) {
		t.Error("read-write must not grant scanner supply-chain privileged scopes")
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

	// Scanner release roles can see the supply-chain inventory without
	// implicitly granting one another's privileged actions.
	for _, held := range []string{
		ScopeOperateScannerSupplyChain,
		ScopeApproveScannerReleases,
		ScopeManageScannerRegistries,
	} {
		if !(ScopeSet{held}).Has(ScopeReadScannerSupplyChain) {
			t.Errorf("%q must imply %q", held, ScopeReadScannerSupplyChain)
		}
	}
	if (ScopeSet{ScopeOperateScannerSupplyChain}).Has(ScopeApproveScannerReleases) {
		t.Error("scanner operator must not implicitly approve releases")
	}
	if (ScopeSet{ScopeApproveScannerReleases}).Has(ScopeManageScannerRegistries) {
		t.Error("release approver must not implicitly manage registries")
	}
	scannerAdmin := ScopeSet{ScopeAdminScannerSupplyChain}
	for _, required := range []string{
		ScopeReadScannerSupplyChain,
		ScopeOperateScannerSupplyChain,
		ScopeApproveScannerReleases,
		ScopeManageScannerRegistries,
	} {
		if !scannerAdmin.Has(required) {
			t.Errorf("scanner supply-chain admin must satisfy %q", required)
		}
	}
	if scannerAdmin.Has(ScopeWriteScans) {
		t.Error("scanner supply-chain admin must not imply unrelated scan write scope")
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

func TestScannerPersonasValidateNormalizeAndCompose(t *testing.T) {
	tests := []struct {
		name         string
		personas     []string
		wantPersonas []string
		wantScopes   []string
	}{
		{"default viewer", nil, []string{ScannerPersonaViewer}, []string{ScopeReadScannerSupplyChain}},
		{"viewer", []string{ScannerPersonaViewer}, []string{ScannerPersonaViewer}, []string{ScopeReadScannerSupplyChain}},
		{"operator", []string{ScannerPersonaOperator}, []string{ScannerPersonaOperator}, []string{ScopeReadScannerSupplyChain, ScopeOperateScannerSupplyChain}},
		{"approver", []string{ScannerPersonaApprover}, []string{ScannerPersonaApprover}, []string{ScopeReadScannerSupplyChain, ScopeApproveScannerReleases}},
		{"registry admin", []string{ScannerPersonaRegistryAdministrator}, []string{ScannerPersonaRegistryAdministrator}, []string{ScopeReadScannerSupplyChain, ScopeManageScannerRegistries}},
		{"auditor", []string{ScannerPersonaAuditor}, []string{ScannerPersonaAuditor}, []string{ScopeReadScannerSupplyChain}},
		{
			"composable sorted and deduplicated",
			[]string{ScannerPersonaOperator, ScannerPersonaApprover, ScannerPersonaOperator},
			[]string{ScannerPersonaApprover, ScannerPersonaOperator},
			[]string{ScopeReadScannerSupplyChain, ScopeOperateScannerSupplyChain, ScopeApproveScannerReleases},
		},
		{
			"supply admin exclusive",
			[]string{ScannerPersonaViewer, ScannerPersonaSupplyChainAdministrator, ScannerPersonaOperator},
			[]string{ScannerPersonaSupplyChainAdministrator},
			[]string{ScopeAdminScannerSupplyChain},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scopes, personas, err := ScannerScopesForPersonas(tt.personas)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(personas, ",") != strings.Join(tt.wantPersonas, ",") {
				t.Fatalf("personas = %v, want %v", personas, tt.wantPersonas)
			}
			for _, scope := range tt.wantScopes {
				if !scopes.Has(scope) {
					t.Errorf("missing scope %q from %v", scope, scopes)
				}
			}
		})
	}

	if _, _, err := ScannerScopesForPersonas([]string{"arbitrary:scope"}); err == nil {
		t.Fatal("unknown persona must be rejected")
	}
	for _, standalone := range []string{ScannerPersonaViewer, ScannerPersonaAuditor} {
		if _, _, err := ScannerScopesForPersonas([]string{standalone, ScannerPersonaOperator}); err == nil {
			t.Fatalf("standalone persona %q composed with operator", standalone)
		}
	}
}

func TestScannerPersonaPersistenceRoundTripAndFailClosed(t *testing.T) {
	encoded, normalized, err := EncodeScannerPersonas([]string{ScannerPersonaOperator, ScannerPersonaApprover})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeScannerPersonas(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(decoded, ",") != strings.Join(normalized, ",") {
		t.Fatalf("decoded personas = %v, want %v", decoded, normalized)
	}

	for _, corrupt := range []string{`not-json`, `["unknown"]`} {
		got, err := DecodeScannerPersonas(corrupt)
		if err == nil {
			t.Fatalf("DecodeScannerPersonas(%q) unexpectedly succeeded", corrupt)
		}
		if len(got) != 1 || got[0] != ScannerPersonaViewer {
			t.Fatalf("corrupt persisted personas must fail closed to viewer, got %v", got)
		}
	}
}
