package middleware

import (
	"net/http"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/scannertrace"
)

// ScannerOperationTrace establishes one safe correlation context for an API
// request. Standards-compliant traceparent values can continue a distributed
// trace; Wolf operation IDs are accepted only from a deliberately narrow,
// bounded character set. Malformed values are replaced rather than reflected.
func ScannerOperationTrace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlation := scannertrace.New("api")
		if traceID, parentSpanID, ok := scannertrace.ParseTraceparent(
			r.Header.Get(scannertrace.HeaderTraceParent),
		); ok {
			correlation.TraceID = traceID
			correlation.ParentSpanID = parentSpanID
		}
		if operationID := strings.TrimSpace(
			r.Header.Get(scannertrace.HeaderOperationID),
		); scannertrace.ValidOperationID(operationID) {
			correlation.OperationID = operationID
		}
		correlation = scannertrace.Normalize(correlation, "api")
		w.Header().Set(scannertrace.HeaderTraceID, correlation.TraceID)
		w.Header().Set(scannertrace.HeaderOperationID, correlation.OperationID)
		w.Header().Set(scannertrace.HeaderTraceParent, scannertrace.Traceparent(correlation))
		next.ServeHTTP(w, r.WithContext(scannertrace.With(r.Context(), correlation)))
	})
}
