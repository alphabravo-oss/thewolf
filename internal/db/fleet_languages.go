package db

import (
	"encoding/json"
	"slices"
	"strings"
)

// parseLanguageList accepts detector counts and legacy array/CSV metadata.
func parseLanguageList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}
	var out []string
	switch raw[0] {
	case '{':
		var counts map[string]int
		if json.Unmarshal([]byte(raw), &counts) != nil {
			return nil
		}
		for language, count := range counts {
			if count > 0 {
				out = append(out, language)
			}
		}
	case '[':
		if json.Unmarshal([]byte(raw), &out) != nil {
			return nil
		}
	default:
		out = strings.Split(raw, ",")
	}
	clean := out[:0]
	for _, language := range out {
		if language = strings.TrimSpace(language); language != "" {
			clean = append(clean, language)
		}
	}
	slices.Sort(clean)
	return slices.Compact(clean)
}
