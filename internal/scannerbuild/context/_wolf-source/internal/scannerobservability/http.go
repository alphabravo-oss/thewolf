package scannerobservability

import (
	"encoding/json"
	"net/http"
)

func (r *Registry) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", r.MetricsHandler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, request *http.Request) {
		writeSnapshot(w, http.StatusOK, r.Snapshot(request.Context()))
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, request *http.Request) {
		snapshot := r.Snapshot(request.Context())
		status := http.StatusOK
		if !snapshot.Ready {
			status = http.StatusServiceUnavailable
		}
		writeSnapshot(w, status, snapshot)
	})
	return mux
}

func writeSnapshot(w http.ResponseWriter, status int, snapshot HealthSnapshot) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]HealthSnapshot{"data": snapshot})
}
