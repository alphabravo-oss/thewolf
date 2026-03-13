package routes

import (
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// Handler holds shared dependencies for route handlers.
type Handler struct {
	Store    db.Store
	Registry *plugin.Registry
}

// DefaultHandler is the global handler instance set by the server.
var DefaultHandler *Handler

// SetHandler sets the global handler used by route functions.
func SetHandler(store db.Store, registry *plugin.Registry) {
	DefaultHandler = &Handler{
		Store:    store,
		Registry: registry,
	}
}
