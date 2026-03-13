package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	payload := map[string]string{"hello": "world"}

	WriteJSON(w, http.StatusOK, payload)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}

	var got map[string]string
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if got["hello"] != "world" {
		t.Errorf("expected hello=world, got %v", got)
	}
}

func TestWriteJSON_CustomStatus(t *testing.T) {
	w := httptest.NewRecorder()
	WriteJSON(w, http.StatusCreated, SuccessResponse{Data: "created"})

	if w.Code != http.StatusCreated {
		t.Errorf("expected status %d, got %d", http.StatusCreated, w.Code)
	}

	var resp SuccessResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp.Data != "created" {
		t.Errorf("expected data=created, got %v", resp.Data)
	}
}

func TestWriteError(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		code    string
		message string
	}{
		{"not found", http.StatusNotFound, "not_found", "resource not found"},
		{"bad request", http.StatusBadRequest, "bad_request", "invalid input"},
		{"server error", http.StatusInternalServerError, "internal_error", "something went wrong"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteError(w, tc.status, tc.code, tc.message)

			if w.Code != tc.status {
				t.Errorf("expected status %d, got %d", tc.status, w.Code)
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("expected Content-Type application/json, got %s", ct)
			}

			var resp ErrorResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode error response: %v", err)
			}
			if resp.Error.Code != tc.code {
				t.Errorf("expected error code %q, got %q", tc.code, resp.Error.Code)
			}
			if resp.Error.Message != tc.message {
				t.Errorf("expected error message %q, got %q", tc.message, resp.Error.Message)
			}
		})
	}
}

func TestListResponse_JSONShape(t *testing.T) {
	w := httptest.NewRecorder()
	resp := ListResponse{
		Data: []string{"a", "b"},
		Meta: ListMeta{Total: 2, Page: 1, PerPage: 10},
	}
	WriteJSON(w, http.StatusOK, resp)

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if _, ok := raw["data"]; !ok {
		t.Error("expected 'data' key in JSON")
	}
	if _, ok := raw["meta"]; !ok {
		t.Error("expected 'meta' key in JSON")
	}

	var meta ListMeta
	if err := json.Unmarshal(raw["meta"], &meta); err != nil {
		t.Fatalf("failed to decode meta: %v", err)
	}
	if meta.Total != 2 {
		t.Errorf("expected total=2, got %d", meta.Total)
	}
	if meta.Page != 1 {
		t.Errorf("expected page=1, got %d", meta.Page)
	}
	if meta.PerPage != 10 {
		t.Errorf("expected per_page=10, got %d", meta.PerPage)
	}
}
