package controlbind

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/pkg/control"
)

func TestRecordsAndUsersRoundTrip(t *testing.T) {
	store, err := db.NewSQLite(t.TempDir() + "/wolf.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	Bind(store)

	u, err := control.UsersAPI().Create(t.Context(), "a@b.co", "Ann", "user")
	if err != nil || u == nil || u.Email != "a@b.co" || u.Name != "Ann" {
		t.Fatalf("create user: %#v %v", u, err)
	}
	got, err := control.UsersAPI().GetByEmail(t.Context(), "a@b.co")
	if err != nil || got == nil || got.ID != u.ID {
		t.Fatalf("get by email: %#v %v", got, err)
	}
	if err := control.RecordsAPI().Put(t.Context(), control.Record{
		Kind: "workspaces", ID: "w1", Name: "Main", Body: `{"region":"us"}`,
	}); err != nil {
		t.Fatal(err)
	}
	list, err := control.RecordsAPI().List(t.Context(), "workspaces")
	if err != nil || len(list) != 1 || list[0].Name != "Main" {
		t.Fatalf("list: %#v %v", list, err)
	}
	row, err := control.RecordsAPI().Get(t.Context(), "workspaces", "w1")
	if err != nil || row == nil || row.ID != "w1" {
		t.Fatalf("get: %#v %v", row, err)
	}
	missing, err := control.RecordsAPI().Get(t.Context(), "workspaces", "nope")
	if err != nil || missing != nil {
		t.Fatalf("missing: %#v %v", missing, err)
	}
}
