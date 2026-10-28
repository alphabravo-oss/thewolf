package controlbind

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/secrets"
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
	raw, err := control.FleetAPI().Report(t.Context(), "")
	if err != nil || !strings.Contains(string(raw), `"repo_count":0`) || !strings.Contains(string(raw), `"aging":[`) {
		t.Fatalf("fleet report: %s %v", raw, err)
	}
	if control.FleetAPI().IsAdmin(t.Context()) {
		t.Fatal("no caller must not be admin")
	}
}

func TestSealerRoundTrip(t *testing.T) {
	secrets.SetMasterKey(make([]byte, 32))
	store, err := db.NewSQLite(t.TempDir() + "/wolf.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	Bind(store)
	s := control.SealerAPI()
	sealed, err := s.Seal("Bearer example")
	if err != nil || !strings.HasPrefix(sealed, control.SealedPrefix) || strings.Contains(sealed, "example") {
		t.Fatalf("seal: %q %v", sealed, err)
	}
	if got, err := s.Open(sealed); err != nil || got != "Bearer example" {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := s.Open("Bearer example"); err == nil {
		t.Fatal("unsealed value must not open")
	}
	flip := map[byte]string{'0': "1"}[sealed[len(sealed)-1]]
	if flip == "" {
		flip = "0"
	}
	if _, err := s.Open(sealed[:len(sealed)-1] + flip); err == nil {
		t.Fatal("tampered value must not open")
	}
}
