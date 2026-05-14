package suppress

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ParseWolfIgnore reads a `.wolfignore` file and returns a RuleSet.
//
// Syntax (gitignore-style with extensions):
//
//	# this is a comment
//	**/legacy/**                                    # suppress everything under legacy/
//	**/testdata/** category=hardcoded-secret        # only suppress hardcoded secrets
//	* rule=semgrep.foo.bar                          # suppress one rule everywhere
//	**/internal/** category=xss,sql-injection rule=R1,R2
//
// Lines starting with '#' or empty are skipped. Whitespace separates the
// path glob from optional `category=` / `rule=` filters. Multiple values
// in a filter are comma-separated, no spaces.
//
// Returns parse errors that the caller can surface in `wolf scan` output;
// individual malformed lines are skipped with a log entry rather than
// aborting the whole file.
func ParseWolfIgnore(r io.Reader, source string) (RuleSet, error) {
	var rs RuleSet
	sc := bufio.NewScanner(r)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		rule, ok := parseWolfIgnoreLine(raw, source, lineNo)
		if ok {
			rs.Rules = append(rs.Rules, rule)
		}
	}
	if err := sc.Err(); err != nil {
		return rs, err
	}
	return rs, nil
}

// ParseWolfIgnoreFile is a convenience wrapper around ParseWolfIgnore that
// reads from a path. Returns an empty RuleSet (nil error) when the file
// does not exist — a missing .wolfignore is not an error, it just means
// "no extra rules".
func ParseWolfIgnoreFile(path string) (RuleSet, error) {
	// #nosec G304 -- reads .wolfignore at the scan root
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return RuleSet{}, nil
		}
		return RuleSet{}, err
	}
	defer f.Close()
	return ParseWolfIgnore(f, filepath.Base(path))
}

// parseWolfIgnoreLine splits one line into a Rule. Returns false when the
// line is malformed (no path glob, just filters) — the caller drops it.
func parseWolfIgnoreLine(raw, source string, lineNo int) (Rule, bool) {
	// Tokenize: first token is the path glob, remaining tokens are
	// "key=value" filters.
	parts := splitFields(raw)
	if len(parts) == 0 {
		return Rule{}, false
	}
	pathGlob := parts[0]
	rule := Rule{
		PathGlob: pathGlob,
		Reason:   source + ":" + pathGlob,
	}
	for _, p := range parts[1:] {
		key, val, ok := cutKV(p)
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case "category", "categories":
			rule.Categories = append(rule.Categories, splitCSV(val)...)
		case "rule", "rules", "rule_id", "rule-id":
			rule.RuleIDs = append(rule.RuleIDs, splitCSV(val)...)
		case "reason":
			rule.Reason = val
		}
	}
	return rule, true
}

// splitFields splits on any whitespace run, ignoring empties.
func splitFields(s string) []string {
	return strings.Fields(s)
}

func cutKV(s string) (key, val string, ok bool) {
	i := strings.IndexByte(s, '=')
	if i < 0 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
