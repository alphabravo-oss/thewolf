package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannertrace"
)

func TestScannerOperationTraceContinuesValidTraceAndOperation(t *testing.T) {
	var observed scannertrace.Correlation
	handler := ScannerOperationTrace(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		observed, _ = scannertrace.FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	request.Header.Set(
		scannertrace.HeaderTraceParent,
		"00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
	)
	request.Header.Set(scannertrace.HeaderOperationID, "operation-client-123")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if observed.TraceID != "0123456789abcdef0123456789abcdef" ||
		observed.OperationID != "operation-client-123" ||
		observed.ParentSpanID != "0123456789abcdef" {
		t.Fatalf("unexpected continued trace: %#v", observed)
	}
	if response.Header().Get(scannertrace.HeaderTraceID) != observed.TraceID ||
		response.Header().Get(scannertrace.HeaderOperationID) != observed.OperationID {
		t.Fatal("correlation response headers do not match request context")
	}
}

func TestScannerOperationTraceDoesNotReflectMalformedIdentifiers(t *testing.T) {
	handler := ScannerOperationTrace(http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(scannertrace.HeaderTraceParent, "bad\r\ntrace")
	request.Header.Set(scannertrace.HeaderOperationID, "secret value\ninjected")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if !scannertrace.ValidTraceID(response.Header().Get(scannertrace.HeaderTraceID)) ||
		!scannertrace.ValidOperationID(
			response.Header().Get(scannertrace.HeaderOperationID),
		) {
		t.Fatalf("invalid generated correlation headers: %#v", response.Header())
	}
	if response.Header().Get(scannertrace.HeaderOperationID) ==
		request.Header.Get(scannertrace.HeaderOperationID) {
		t.Fatal("malformed operation ID was reflected")
	}
}
