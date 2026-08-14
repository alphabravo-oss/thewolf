package apikey

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Scope strings follow the form "verb:resource". The verb is read or write;
// "admin" is a standalone super-scope.
const (
	ScopeReadRepos        = "read:repos"
	ScopeWriteRepos       = "write:repos"
	ScopeReadScans        = "read:scans"
	ScopeWriteScans       = "write:scans"
	ScopeReadFindings     = "read:findings"
	ScopeWriteFindings    = "write:findings"
	ScopeReadFixes        = "read:fixes"
	ScopeWriteFixes       = "write:fixes"
	ScopeReadAgents       = "read:agents"
	ScopeWriteAgents      = "write:agents"
	ScopeReadConfig       = "read:config"
	ScopeWriteConfig      = "write:config"
	ScopeReadCredentials  = "read:credentials"
	ScopeWriteCredentials = "write:credentials"
	// Scanner supply-chain scopes are deliberately more granular than the
	// legacy read/write vocabulary. Operating a build or rollout, approving a
	// release, and administering registry trust are distinct enterprise
	// responsibilities.
	ScopeReadScannerSupplyChain    = "read:scanner-supply-chain"
	ScopeOperateScannerSupplyChain = "operate:scanner-supply-chain"
	ScopeApproveScannerReleases    = "approve:scanner-releases"
	ScopeManageScannerRegistries   = "manage:scanner-registries"
	ScopeAdminScannerSupplyChain   = "admin:scanner-supply-chain"
	ScopeAdmin                     = "admin"

	ScannerPersonaViewer                   = "viewer"
	ScannerPersonaOperator                 = "scanner_operator"
	ScannerPersonaApprover                 = "release_approver"
	ScannerPersonaRegistryAdministrator    = "registry_administrator"
	ScannerPersonaSupplyChainAdministrator = "supply_chain_administrator"
	ScannerPersonaAuditor                  = "auditor"
)

// ScannerPersonaPreset is a server-owned bundle of scanner supply-chain
// scopes assignable to a human user. Persisting preset IDs instead of
// caller-provided scopes prevents the user-admin endpoint from becoming an
// arbitrary scope-minting surface.
type ScannerPersonaPreset struct {
	ID     string
	Scopes ScopeSet
}

var ScannerPersonaPresets = []ScannerPersonaPreset{
	{ID: ScannerPersonaViewer, Scopes: ScopeSet{ScopeReadScannerSupplyChain}},
	{ID: ScannerPersonaOperator, Scopes: ScopeSet{ScopeReadScannerSupplyChain, ScopeOperateScannerSupplyChain}},
	{ID: ScannerPersonaApprover, Scopes: ScopeSet{ScopeReadScannerSupplyChain, ScopeApproveScannerReleases}},
	{ID: ScannerPersonaRegistryAdministrator, Scopes: ScopeSet{ScopeReadScannerSupplyChain, ScopeManageScannerRegistries}},
	{ID: ScannerPersonaSupplyChainAdministrator, Scopes: ScopeSet{ScopeAdminScannerSupplyChain}},
	{ID: ScannerPersonaAuditor, Scopes: ScopeSet{ScopeReadScannerSupplyChain}},
}

var scannerPersonaByID = func() map[string]ScannerPersonaPreset {
	out := make(map[string]ScannerPersonaPreset, len(ScannerPersonaPresets))
	for _, preset := range ScannerPersonaPresets {
		out[preset.ID] = preset
	}
	return out
}()

// AllScopes is the full, ordered scope vocabulary.
var AllScopes = []string{
	ScopeReadRepos, ScopeWriteRepos,
	ScopeReadScans, ScopeWriteScans,
	ScopeReadFindings, ScopeWriteFindings,
	ScopeReadFixes, ScopeWriteFixes,
	ScopeReadAgents, ScopeWriteAgents,
	ScopeReadConfig, ScopeWriteConfig,
	ScopeReadCredentials, ScopeWriteCredentials,
	ScopeReadScannerSupplyChain,
	ScopeOperateScannerSupplyChain,
	ScopeApproveScannerReleases,
	ScopeManageScannerRegistries,
	ScopeAdminScannerSupplyChain,
	ScopeAdmin,
}

// validScope is a fast membership set for AllScopes.
var validScope = func() map[string]bool {
	m := make(map[string]bool, len(AllScopes))
	for _, s := range AllScopes {
		m[s] = true
	}
	return m
}()

// ScopeSet is an authorization decision set: the scopes a credential holds.
type ScopeSet []string

// Has reports whether the set satisfies the required scope, accounting for
// two implication rules:
//   - "admin" satisfies every scope.
//   - "write:X" satisfies "read:X" (writers can always read).
func (s ScopeSet) Has(required string) bool {
	for _, held := range s {
		if held == ScopeAdmin || held == required {
			return true
		}
		if held == ScopeAdminScannerSupplyChain && isScannerSupplyChainScope(required) {
			return true
		}
		if required == ScopeReadScannerSupplyChain {
			switch held {
			case ScopeOperateScannerSupplyChain, ScopeApproveScannerReleases, ScopeManageScannerRegistries:
				return true
			}
		}
		if strings.HasPrefix(required, "read:") {
			resource := strings.TrimPrefix(required, "read:")
			if held == "write:"+resource {
				return true
			}
		}
	}
	return false
}

func isScannerSupplyChainScope(scope string) bool {
	switch scope {
	case ScopeReadScannerSupplyChain,
		ScopeOperateScannerSupplyChain,
		ScopeApproveScannerReleases,
		ScopeManageScannerRegistries,
		ScopeAdminScannerSupplyChain:
		return true
	default:
		return false
	}
}

// HasAll reports whether the set satisfies every required scope.
func (s ScopeSet) HasAll(required ...string) bool {
	for _, r := range required {
		if !s.Has(r) {
			return false
		}
	}
	return true
}

// AllowsDelegation reports whether a credential holding s may mint a new
// credential with requested scopes. Unlike Has, the admin super-scope does not
// imply that non-admin callers may delegate arbitrary privileges; role checks
// remain responsible for admin authority.
func (s ScopeSet) AllowsDelegation(requested ScopeSet) bool {
	for _, scope := range requested {
		if !s.Has(scope) {
			return false
		}
	}
	return true
}

// AdminAll is the implicit scope set granted to an administrator's browser
// session. Human sessions are role-derived by auth.Middleware; the browser is
// never an authorization boundary by itself.
func AdminAll() ScopeSet { return ScopeSet{ScopeAdmin} }

// UserSession is the least-privilege scope set for a non-administrator human
// session. It preserves the ordinary product read/write flows (whose handlers
// still enforce ownership) and grants scanner supply-chain visibility only.
// Operating, approving, publishing, registry, signer, policy, audit, and
// revocation surfaces remain unavailable without their explicit enterprise
// scopes or the administrator role.
func UserSession() ScopeSet {
	return UserSessionForScannerPersonas(nil)
}

// UserSessionForScannerPersonas preserves the ordinary per-owner product
// permissions while replacing only the scanner supply-chain portion with the
// server-validated persona union. Existing users with no stored assignment
// remain read-only scanner viewers.
func UserSessionForScannerPersonas(personas []string) ScopeSet {
	scannerScopes, _, err := ScannerScopesForPersonas(personas)
	if err != nil {
		scannerScopes = ScopeSet{ScopeReadScannerSupplyChain}
	}
	base := ScopeSet{
		ScopeReadRepos, ScopeWriteRepos,
		ScopeReadScans, ScopeWriteScans,
		ScopeReadFindings, ScopeWriteFindings,
		ScopeReadFixes, ScopeWriteFixes,
		ScopeReadAgents, ScopeWriteAgents,
		ScopeReadConfig, ScopeWriteConfig,
		ScopeReadCredentials, ScopeWriteCredentials,
	}
	return append(base, scannerScopes...)
}

// ScannerScopesForPersonas validates, normalizes, and expands composable
// scanner persona IDs. An empty assignment is the least-privilege Viewer.
// Supply-chain Administrator subsumes every other scanner persona.
func ScannerScopesForPersonas(raw []string) (ScopeSet, []string, error) {
	if len(raw) == 0 {
		raw = []string{ScannerPersonaViewer}
	}
	seenPersonas := make(map[string]bool, len(raw))
	seenScopes := make(map[string]bool)
	personas := make([]string, 0, len(raw))
	scopes := make(ScopeSet, 0, len(raw)+1)
	for _, value := range raw {
		id := strings.ToLower(strings.TrimSpace(value))
		preset, ok := scannerPersonaByID[id]
		if !ok {
			return nil, nil, fmt.Errorf("unknown scanner supply-chain persona %q", value)
		}
		if seenPersonas[id] {
			continue
		}
		seenPersonas[id] = true
		personas = append(personas, id)
		for _, scope := range preset.Scopes {
			if !seenScopes[scope] {
				seenScopes[scope] = true
				scopes = append(scopes, scope)
			}
		}
	}
	if seenPersonas[ScannerPersonaSupplyChainAdministrator] {
		return ScopeSet{ScopeAdminScannerSupplyChain}, []string{ScannerPersonaSupplyChainAdministrator}, nil
	}
	if len(personas) > 1 && (seenPersonas[ScannerPersonaViewer] || seenPersonas[ScannerPersonaAuditor]) {
		return nil, nil, fmt.Errorf("viewer and auditor are standalone scanner supply-chain personas")
	}
	if len(scopes) == 0 {
		scopes = ScopeSet{ScopeReadScannerSupplyChain}
	}
	sort.Strings(personas)
	return scopes, personas, nil
}

func EncodeScannerPersonas(raw []string) (string, []string, error) {
	_, personas, err := ScannerScopesForPersonas(raw)
	if err != nil {
		return "", nil, err
	}
	encoded, err := json.Marshal(personas)
	if err != nil {
		return "", nil, fmt.Errorf("encode scanner supply-chain personas: %w", err)
	}
	return string(encoded), personas, nil
}

// DecodeScannerPersonas fails closed to Viewer when persisted data is absent;
// malformed or unknown persisted values return an error so callers can audit
// corruption while still denying elevated capability.
func DecodeScannerPersonas(encoded string) ([]string, error) {
	if strings.TrimSpace(encoded) == "" {
		return []string{ScannerPersonaViewer}, nil
	}
	var raw []string
	if err := json.Unmarshal([]byte(encoded), &raw); err != nil {
		return []string{ScannerPersonaViewer}, fmt.Errorf("decode scanner supply-chain personas: %w", err)
	}
	_, personas, err := ScannerScopesForPersonas(raw)
	if err != nil {
		return []string{ScannerPersonaViewer}, err
	}
	return personas, nil
}

// ParseScopes validates and normalizes a raw scope list from a token-creation
// request. It expands the aliases "read-only" (every read:* scope) and "full"
// (every scope). Unknown scopes are rejected. An empty input is an error —
// a token with no scopes can do nothing and is almost certainly a mistake.
func ParseScopes(raw []string) (ScopeSet, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("at least one scope is required")
	}
	seen := make(map[string]bool)
	var out ScopeSet
	add := func(scope string) {
		if !seen[scope] {
			seen[scope] = true
			out = append(out, scope)
		}
	}
	for _, s := range raw {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "":
			continue
		case "read-only", "readonly":
			for _, sc := range AllScopes {
				if strings.HasPrefix(sc, "read:") {
					add(sc)
				}
			}
		case "read-write", "readwrite":
			// Every conventional read + write scope, but not approval,
			// registry-management, operation, or administrative scopes.
			for _, sc := range AllScopes {
				if strings.HasPrefix(sc, "read:") || strings.HasPrefix(sc, "write:") {
					add(sc)
				}
			}
		case "full", "all":
			for _, sc := range AllScopes {
				add(sc)
			}
		default:
			norm := strings.ToLower(strings.TrimSpace(s))
			if !validScope[norm] {
				return nil, fmt.Errorf("unknown scope %q (valid: %s)", s, strings.Join(AllScopes, ", "))
			}
			add(norm)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one scope is required")
	}
	sort.Strings(out)
	return out, nil
}

// Encode serializes a scope set to the JSON string stored in the database.
func (s ScopeSet) Encode() string {
	if s == nil {
		s = ScopeSet{}
	}
	b, _ := json.Marshal([]string(s))
	return string(b)
}

// DecodeScopes parses the JSON scope string stored in the database.
func DecodeScopes(stored string) ScopeSet {
	if strings.TrimSpace(stored) == "" {
		return ScopeSet{}
	}
	var out []string
	if err := json.Unmarshal([]byte(stored), &out); err != nil {
		return ScopeSet{}
	}
	return ScopeSet(out)
}
