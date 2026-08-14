package db

import (
	"context"
	"fmt"
	"strings"
)

// FleetInventory returns counts of repos by source_type, by collection name,
// and by detected language.
func (s *SQLiteStore) FleetInventory(ctx context.Context, userID string, fleetMode bool) (*FleetInventoryResult, error) {
	out := &FleetInventoryResult{
		BySourceType: map[string]int{},
		ByCollection: map[string]int{},
		ByLanguage:   map[string]int{},
	}

	// By source type.
	srcQ := `SELECT source_type, COUNT(*) FROM repos`
	srcArgs := []any{}
	if !fleetMode {
		srcQ += ` WHERE user_id = ?`
		srcArgs = append(srcArgs, userID)
	}
	srcQ += ` GROUP BY source_type`
	rows, err := s.db.QueryContext(ctx, srcQ, srcArgs...)
	if err != nil {
		return nil, fmt.Errorf("inventory by source_type: %w", err)
	}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out.BySourceType[st] = n
	}
	_ = rows.Close()

	// By collection (name).
	colQ := `SELECT c.name, COUNT(cr.repo_id)
	         FROM collections c
	         LEFT JOIN collection_repos cr ON cr.collection_id = c.id`
	colArgs := []any{}
	if !fleetMode {
		colQ += ` WHERE c.user_id = ?`
		colArgs = append(colArgs, userID)
	}
	colQ += ` GROUP BY c.id, c.name`
	if rows, err := s.db.QueryContext(ctx, colQ, colArgs...); err == nil {
		for rows.Next() {
			var name string
			var n int
			if err := rows.Scan(&name, &n); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out.ByCollection[name] = n
		}
		_ = rows.Close()
	}

	// By language: detected_languages is a JSON array of strings. Decode in
	// Go since SQLite JSON support varies.
	langQ := `SELECT detected_languages FROM repos`
	langArgs := []any{}
	if !fleetMode {
		langQ += ` WHERE user_id = ?`
		langArgs = append(langArgs, userID)
	}
	if rows, err := s.db.QueryContext(ctx, langQ, langArgs...); err == nil {
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				_ = rows.Close()
				return nil, err
			}
			for _, lang := range parseLanguageList(raw) {
				out.ByLanguage[lang]++
			}
		}
		_ = rows.Close()
	}

	return out, nil
}

// parseLanguageList decodes detected_languages, which may be a JSON array
// (`["go","ts"]`), a comma-separated list, or empty.
func parseLanguageList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "null" {
		return nil
	}
	// JSON array form.
	if strings.HasPrefix(raw, "[") {
		trimmed := strings.TrimPrefix(strings.TrimSuffix(raw, "]"), "[")
		parts := strings.Split(trimmed, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			p = strings.Trim(p, `"`)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	// Fallback: comma-separated.
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
