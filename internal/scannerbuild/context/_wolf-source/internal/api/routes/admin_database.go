package routes

import (
	"encoding/json"
	"net/http"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/db"
)

func AdminGetDatabase(w http.ResponseWriter, r *http.Request) {
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: db.ResolveDatabase().View()})
}

func AdminTestDatabase(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DSN string `json:"dsn"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.WriteError(w, http.StatusBadRequest, "invalid_json", "invalid json")
		return
	}
	if err := db.PingPostgres(r.Context(), body.DSN); err != nil {
		response.WriteError(w, http.StatusBadRequest, "db_unreachable", err.Error())
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]bool{"ok": true}})
}

func AdminSaveDatabase(w http.ResponseWriter, r *http.Request) {
	cfg := db.ResolveDatabase()
	if cfg.EnvManaged {
		response.WriteError(w, http.StatusConflict, "env_managed", "WOLF_DB_DSN is set by Helm or the environment. Change postgres.mode / postgres.external and helm upgrade.")
		return
	}
	var body struct {
		DSN string `json:"dsn"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.WriteError(w, http.StatusBadRequest, "invalid_json", "invalid json")
		return
	}
	if err := db.PingPostgres(r.Context(), body.DSN); err != nil {
		response.WriteError(w, http.StatusBadRequest, "db_unreachable", err.Error())
		return
	}
	if err := db.SavePostgresOverride(body.DSN); err != nil {
		response.WriteError(w, http.StatusBadRequest, "invalid_dsn", err.Error())
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{"restart": true}})
}
