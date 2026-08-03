package db

import (
	"encoding/json"
	"strings"

	"github.com/jmoiron/sqlx"
)

// sanitizePersistedSecretMasks removes the legacy presentation-only "masked"
// field from durable metadata. Older versions stored a secret-derived suffix
// and exact length there, outside the encrypted value.
func sanitizePersistedSecretMasks(database *sqlx.DB) error {
	tx, err := database.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var rows []struct {
		ID           string `db:"id"`
		MetadataJSON string `db:"metadata_json"`
	}
	if err := tx.Select(&rows,
		`SELECT id, metadata_json FROM secrets WHERE metadata_json LIKE '%"masked"%'`); err != nil {
		return err
	}
	updateQuery := database.Rebind(`UPDATE secrets SET metadata_json = ? WHERE id = ?`)
	for _, row := range rows {
		var metadata map[string]json.RawMessage
		if err := json.Unmarshal([]byte(row.MetadataJSON), &metadata); err != nil {
			// Generated metadata was always valid JSON. If a manually altered
			// row contains the reserved key but is malformed, fail closed
			// rather than retaining potentially plaintext-derived material.
			if _, err := tx.Exec(updateQuery, "{}", row.ID); err != nil {
				return err
			}
			continue
		}
		if _, exists := metadata["masked"]; !exists {
			continue
		}
		delete(metadata, "masked")
		cleaned, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(cleaned)) == "null" {
			cleaned = []byte("{}")
		}
		if _, err := tx.Exec(updateQuery, string(cleaned), row.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
