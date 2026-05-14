package knowledge

import "sync"

var (
	mu         sync.RWMutex
	entries    = map[string]Entry{}    // key: tool + ":" + ruleID
	strategies = map[string]Strategy{} // key: strategy ID
)

// RegisterEntry adds a rule mapping. Called from each data_<tool>.go init().
// Later registrations overwrite earlier ones, but the package is intended to
// have no duplicate (tool, rule_id) keys; duplicates indicate a bug to fix
// in the data files rather than a runtime concern.
func RegisterEntry(e Entry) {
	mu.Lock()
	defer mu.Unlock()
	entries[entryKey(e.Tool, e.RuleID)] = e
}

// RegisterStrategy adds a fix-strategy template. Same overwrite semantics.
func RegisterStrategy(s Strategy) {
	mu.Lock()
	defer mu.Unlock()
	strategies[s.ID] = s
}

// Lookup returns the Entry for a (tool, ruleID) pair. ok=false when not
// known to the knowledge base — caller should leave fine_category empty.
func Lookup(tool, ruleID string) (Entry, bool) {
	mu.RLock()
	defer mu.RUnlock()
	e, ok := entries[entryKey(tool, ruleID)]
	return e, ok
}

// GetStrategy returns the Strategy template for id. ok=false when not known.
func GetStrategy(id string) (Strategy, bool) {
	mu.RLock()
	defer mu.RUnlock()
	s, ok := strategies[id]
	return s, ok
}

// AllStrategies returns a snapshot of all registered strategies. Order is
// not stable across runs; callers that need ordering should sort by ID.
func AllStrategies() []Strategy {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Strategy, 0, len(strategies))
	for _, s := range strategies {
		out = append(out, s)
	}
	return out
}

// Stats returns coverage numbers — total entries registered and total
// strategies. Used by the (eventual) `wolf knowledge` admin command.
func Stats() (entryCount, strategyCount int) {
	mu.RLock()
	defer mu.RUnlock()
	return len(entries), len(strategies)
}

// Validate cross-references every entry's FixStrategy against the strategy
// registry and returns the names of any dangling references. A non-empty
// result signals a knowledge-base bug — wire this into a unit test and a
// future `wolf knowledge validate` subcommand.
func Validate() []string {
	mu.RLock()
	defer mu.RUnlock()
	var dangling []string
	for _, e := range entries {
		if e.FixStrategy == "" {
			continue
		}
		if _, ok := strategies[e.FixStrategy]; !ok {
			dangling = append(dangling, e.Tool+":"+e.RuleID+" → "+e.FixStrategy)
		}
	}
	return dangling
}

func entryKey(tool, ruleID string) string {
	return tool + ":" + ruleID
}
