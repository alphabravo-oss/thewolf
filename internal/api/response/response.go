package response

import (
	"encoding/json"
	"net/http"

	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// SuccessResponse wraps a single resource.
type SuccessResponse struct {
	Data interface{} `json:"data"`
}

// ListMeta contains pagination metadata.
type ListMeta struct {
	Total   int `json:"total"`
	Page    int `json:"page"`
	PerPage int `json:"per_page"`
}

// ListResponse wraps a list of resources with pagination.
type ListResponse struct {
	Data interface{} `json:"data"`
	Meta ListMeta    `json:"meta"`
}

// ErrorDetail contains error information.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ErrorResponse wraps an error.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// WriteJSON writes a JSON response.
func WriteJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		wolflog.Error().Err(err).Msg("failed to write JSON response")
	}
}

// WriteError writes an error response with structured logging.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	if status >= 500 {
		wolflog.Error().Int("status", status).Str("code", code).Str("message", message).Msg("API error")
	} else if status >= 400 {
		wolflog.Debug().Int("status", status).Str("code", code).Str("message", message).Msg("client error")
	}
	WriteJSON(w, status, ErrorResponse{
		Error: ErrorDetail{Code: code, Message: message},
	})
}
