package fix

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrDisabled = errors.New("autonomous fixing is disabled")

// CheckEnabled is the execution gate. Missing settings default off; unreadable
// settings fail closed rather than retaining a previously enabled value.
func CheckEnabled(ctx context.Context, store interface {
	GetSetting(context.Context, string) (string, error)
}) error {
	value, err := store.GetSetting(ctx, "autofix_enabled")
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDisabled
	}
	if err != nil {
		return fmt.Errorf("read autofix setting: %w", err)
	}
	if value != "true" {
		return ErrDisabled
	}
	return nil
}
