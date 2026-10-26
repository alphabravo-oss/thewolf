package fix

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

type settingResult struct {
	value string
	err   error
}

func (s settingResult) GetSetting(context.Context, string) (string, error) { return s.value, s.err }

func TestCheckEnabledFailsClosed(t *testing.T) {
	unavailable := errors.New("database unavailable")
	for _, tc := range []struct {
		name, value   string
		readErr, want error
	}{
		{"enabled", "true", nil, nil},
		{"disabled", "false", nil, ErrDisabled},
		{"empty", "", nil, ErrDisabled},
		{"invalid", "TRUE", nil, ErrDisabled},
		{"missing", "", sql.ErrNoRows, ErrDisabled},
		{"unreadable", "true", unavailable, unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := CheckEnabled(t.Context(), settingResult{tc.value, tc.readErr}); !errors.Is(err, tc.want) {
				t.Fatalf("CheckEnabled = %v, want %v", err, tc.want)
			}
		})
	}
}
