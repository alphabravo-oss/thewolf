package routes

import "net/http"

func stub(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	w.Write([]byte(`{"error":{"code":"not_implemented","message":"not implemented yet"}}`))
}

// Scans — implemented in scans.go

// Findings — implemented in findings.go

// Fixes — implemented in fixes.go

// Loops — implemented in loops.go
