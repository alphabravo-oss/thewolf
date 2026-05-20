package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderJSONReemitsEnvelope(t *testing.T) {
	env := &Envelope{
		Data: json.RawMessage(`[{"id":"r1","name":"acme"}]`),
		Meta: &ListMeta{Total: 1, Page: 1, PerPage: 25},
	}
	var buf bytes.Buffer
	if err := Render(&buf, OutputJSON, env); err != nil {
		t.Fatalf("Render json: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("json output is not valid JSON: %v", err)
	}
	if out["data"] == nil || out["meta"] == nil {
		t.Errorf("json output missing data/meta: %s", buf.String())
	}
}

func TestRenderTableForRows(t *testing.T) {
	env := &Envelope{Data: json.RawMessage(`[
		{"id":"r1","name":"acme","status":"active"},
		{"id":"r2","name":"beta","status":"idle"}
	]`)}
	var buf bytes.Buffer
	if err := Render(&buf, OutputTable, env); err != nil {
		t.Fatalf("Render table: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"ID", "NAME", "STATUS", "r1", "acme", "beta"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderTableEmptyList(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, OutputTable, &Envelope{Data: json.RawMessage(`[]`)}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), "no results") {
		t.Errorf("expected an empty-list notice, got %q", buf.String())
	}
}

func TestRenderTableForObject(t *testing.T) {
	var buf bytes.Buffer
	env := &Envelope{Data: json.RawMessage(`{"id":"r1","name":"acme"}`)}
	if err := Render(&buf, OutputTable, env); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "ID") || !strings.Contains(out, "r1") {
		t.Errorf("object table missing fields:\n%s", out)
	}
}

func TestRenderYAML(t *testing.T) {
	var buf bytes.Buffer
	env := &Envelope{Data: json.RawMessage(`{"id":"r1","name":"acme"}`)}
	if err := Render(&buf, OutputYAML, env); err != nil {
		t.Fatalf("Render yaml: %v", err)
	}
	if !strings.Contains(buf.String(), "name: acme") {
		t.Errorf("yaml output unexpected:\n%s", buf.String())
	}
}
