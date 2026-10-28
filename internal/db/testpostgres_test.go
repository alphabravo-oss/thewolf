package db

import "testing"

func TestNewTestPostgresIsolatesTests(t *testing.T) {
	t.Parallel()
	a, b := NewTestPostgres(t), NewTestPostgres(t)
	if err := a.SetSetting(t.Context(), "isolation", "a"); err != nil {
		t.Fatal(err)
	}
	if v, err := b.GetSetting(t.Context(), "isolation"); err == nil {
		t.Fatalf("test databases leaked: %q", v)
	}
}
