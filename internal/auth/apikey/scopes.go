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
	ScopeReadRepos     = "read:repos"
	ScopeWriteRepos    = "write:repos"
	ScopeReadScans     = "read:scans"
	ScopeWriteScans    = "write:scans"
	ScopeReadFindings  = "read:findings"
	ScopeWriteFindings = "write:findings"
	ScopeReadFixes     = "read:fixes"
	ScopeWriteFixes    = "write:fixes"
	ScopeReadLoops     = "read:loops"
	ScopeWriteLoops    = "write:loops"
	ScopeReadConfig    = "read:config"
	ScopeWriteConfig   = "write:config"
	ScopeAdmin         = "admin"
)

// AllScopes is the full, ordered scope vocabulary.
var AllScopes = []string{
	ScopeReadRepos, ScopeWriteRepos,
	ScopeReadScans, ScopeWriteScans,
	ScopeReadFindings, ScopeWriteFindings,
	ScopeReadFixes, ScopeWriteFixes,
	ScopeReadLoops, ScopeWriteLoops,
	ScopeReadConfig, ScopeWriteConfig,
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
		if strings.HasPrefix(required, "read:") {
			resource := strings.TrimPrefix(required, "read:")
			if held == "write:"+resource {
				return true
			}
		}
	}
	return false
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

// AdminAll is the implicit scope set granted to JWT (UI) sessions — the UI
// is fully trusted, so it carries every privilege.
func AdminAll() ScopeSet { return ScopeSet{ScopeAdmin} }

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
