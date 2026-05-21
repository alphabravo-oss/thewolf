package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

// Output formats. The CLI defaults to table on a terminal and json when its
// stdout is piped — so a human sees a table and a script/AI gets JSON.
const (
	OutputJSON  = "json"
	OutputYAML  = "yaml"
	OutputTable = "table"
)

// Render writes an API response envelope to w in the requested format.
//
// Some endpoints (scan report, SARIF, findings export) return a raw payload
// rather than the JSON envelope — that content is emitted verbatim so those
// commands work regardless of the requested format.
func Render(w io.Writer, format string, env *Envelope) error {
	if env == nil {
		return nil
	}
	var data any
	if len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, &data); err != nil {
			// Non-JSON payload (markdown report, SARIF XML, CSV export).
			if _, werr := w.Write(env.Data); werr != nil {
				return werr
			}
			_, werr := w.Write([]byte("\n"))
			return werr
		}
	}
	switch format {
	case OutputJSON:
		return renderJSON(w, data, env.Meta)
	case OutputYAML:
		return renderYAML(w, data)
	default:
		return renderTable(w, data)
	}
}

func renderJSON(w io.Writer, data any, meta *ListMeta) error {
	// Re-emit the wire envelope so CLI JSON output is byte-compatible with
	// what the API itself returns — a script can treat them identically.
	out := map[string]any{"data": data}
	if meta != nil {
		out["meta"] = meta
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func renderYAML(w io.Writer, data any) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	defer func() { _ = enc.Close() }()
	return enc.Encode(data)
}

func renderTable(w io.Writer, data any) error {
	switch v := data.(type) {
	case nil:
		return nil
	case []any:
		return renderRows(w, v)
	case map[string]any:
		return renderObject(w, v)
	default:
		_, err := fmt.Fprintln(w, formatScalar(v))
		return err
	}
}

// renderRows prints a slice of objects as an aligned table. Columns are the
// keys of the first row; nested values are summarized.
func renderRows(w io.Writer, rows []any) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "(no results)")
		return err
	}
	first, ok := rows[0].(map[string]any)
	if !ok {
		for _, r := range rows {
			fmt.Fprintln(w, formatScalar(r))
		}
		return nil
	}
	cols := preferredColumns(first)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	headers := make([]string, len(cols))
	for i, c := range cols {
		headers[i] = strings.ToUpper(c)
	}
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, r := range rows {
		obj, _ := r.(map[string]any)
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = formatScalar(obj[c])
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return tw.Flush()
}

func renderObject(w io.Writer, obj map[string]any) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sortKeys(keys)
	for _, k := range keys {
		fmt.Fprintf(tw, "%s\t%s\n", strings.ToUpper(k), formatScalar(obj[k]))
	}
	return tw.Flush()
}

// preferredColumns keeps common identifying fields first, then the rest in
// a stable order, capped so wide objects stay readable.
func preferredColumns(obj map[string]any) []string {
	priority := []string{"id", "name", "email", "status", "severity", "title", "url", "branch", "created_at"}
	var cols []string
	seen := map[string]bool{}
	for _, p := range priority {
		if _, ok := obj[p]; ok {
			cols = append(cols, p)
			seen[p] = true
		}
	}
	var rest []string
	for k := range obj {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sortKeys(rest)
	cols = append(cols, rest...)
	if len(cols) > 8 {
		cols = cols[:8]
	}
	return cols
}

func sortKeys(keys []string) { sort.Strings(keys) }

// formatScalar renders any JSON value as a single compact table cell.
func formatScalar(v any) string {
	switch t := v.(type) {
	case nil:
		return "-"
	case string:
		return truncate(t, 60)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case []any:
		return fmt.Sprintf("[%d]", len(t))
	case map[string]any:
		return "{...}"
	default:
		return truncate(fmt.Sprintf("%v", t), 60)
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
