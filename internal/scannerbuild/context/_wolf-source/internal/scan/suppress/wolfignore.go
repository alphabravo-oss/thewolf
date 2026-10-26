package suppress

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
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
// Invalid syntax, unsupported negation, and read errors reject the entire file.
// Never apply a partial ruleset or broaden a malformed filter into a path rule.
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
		rule, err := parseWolfIgnoreLine(raw, source)
		if err != nil {
			return RuleSet{}, fmt.Errorf("%s:%d: %w", source, lineNo, err)
		}
		rs.Rules = append(rs.Rules, rule)
	}
	if err := sc.Err(); err != nil {
		return RuleSet{}, fmt.Errorf("%s:%d: %w", source, lineNo+1, err)
	}
	return rs, nil
}

// ParseWolfIgnoreFile is a convenience wrapper around ParseWolfIgnore that
// reads from a path. Returns an empty RuleSet (nil error) when the file
// does not exist — a missing .wolfignore is not an error, it just means
// "no extra rules".
func ParseWolfIgnoreFile(path string) (RuleSet, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		if os.IsNotExist(err) {
			return RuleSet{}, nil
		}
		return RuleSet{}, err
	}
	defer func() { _ = root.Close() }()
	// Pin a regular file inside the source root; a FIFO must not block the reader.
	f, err := root.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return RuleSet{}, nil
		}
		return RuleSet{}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return RuleSet{}, err
	}
	if !info.Mode().IsRegular() {
		return RuleSet{}, fmt.Errorf("%s: expected a regular file", filepath.Base(path))
	}
	return ParseWolfIgnore(f, filepath.Base(path))
}

func parseWolfIgnoreLine(raw, source string) (Rule, error) {
	parts := strings.Fields(raw)
	pathGlob := parts[0]
	if strings.HasPrefix(pathGlob, "!") {
		return Rule{}, fmt.Errorf("negation is not supported")
	}
	if key, _, ok := strings.Cut(pathGlob, "="); ok {
		switch strings.ToLower(key) {
		case "category", "categories", "rule", "rules", "rule_id", "rule-id", "reason":
			return Rule{}, fmt.Errorf("expected a path glob before filters")
		}
	}
	if strings.ContainsRune(pathGlob, 0) {
		return Rule{}, fmt.Errorf("path glob contains NUL")
	}
	if _, err := path.Match(pathGlob, ""); err != nil {
		return Rule{}, fmt.Errorf("invalid path glob: %w", err)
	}
	rule := Rule{PathGlob: pathGlob, Reason: source + ":" + pathGlob}
	for _, p := range parts[1:] {
		if strings.HasPrefix(p, "#") {
			break
		}
		key, val, ok := strings.Cut(p, "=")
		if !ok || val == "" || strings.ContainsRune(val, 0) {
			return Rule{}, fmt.Errorf("expected a nonempty filter or reason")
		}
		switch strings.ToLower(key) {
		case "category", "categories", "rule", "rules", "rule_id", "rule-id":
			values := strings.Split(val, ",")
			for _, value := range values {
				if value == "" {
					return Rule{}, fmt.Errorf("filter contains an empty value")
				}
			}
			if strings.EqualFold(key, "category") || strings.EqualFold(key, "categories") {
				rule.Categories = append(rule.Categories, values...)
			} else {
				rule.RuleIDs = append(rule.RuleIDs, values...)
			}
		case "reason":
			rule.Reason = val
		default:
			return Rule{}, fmt.Errorf("unknown filter %q", key)
		}
	}
	return rule, nil
}
