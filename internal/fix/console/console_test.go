package console

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/fix/fixstore"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestLoginArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		engine string
		want0  string
	}{
		{"claude", "claude"},
		{"claude-code", "claude"},
		{"codex", "codex"},
		{"opencode", "opencode"},
	}
	for _, tc := range cases {
		got, err := LoginArgs(tc.engine)
		if err != nil {
			t.Fatalf("%s: %v", tc.engine, err)
		}
		if got[0] != tc.want0 {
			t.Errorf("%s: command %q, want %q", tc.engine, got[0], tc.want0)
		}
	}
	if _, err := LoginArgs("api"); err == nil {
		t.Fatal("expected error for api engine")
	}
}

func TestRunEchoesAndCapturesURL(t *testing.T) {
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fs := fixstore.New(t.TempDir())
	cons := &models.FixerConsole{
		ID:     "cons-echo",
		UserID: "u1",
		Kind:   models.FixerConsoleLogin,
		Engine: "claude",
		Status: models.FixerConsoleQueued,
	}
	if err := store.EnqueueFixerConsole(context.Background(), cons); err != nil {
		t.Fatal(err)
	}
	cons, err = store.ClaimNextFixerConsole(context.Background(), "test-worker")
	if err != nil || cons == nil {
		t.Fatalf("claim: %+v %v", cons, err)
	}
	orig := CommandForTest
	CommandForTest = func(*models.FixerConsole) ([]string, error) {
		return []string{"/bin/echo", "visit https://example.test/oauth"}, nil
	}
	t.Cleanup(func() { CommandForTest = orig })

	if err := Run(context.Background(), store, fs, cons); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, err := store.GetFixerConsoleByID(context.Background(), cons.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.FixerConsoleExited {
		t.Fatalf("status = %s", got.Status)
	}
	if got.LastURL != "https://example.test/oauth" {
		t.Fatalf("last_url = %q", got.LastURL)
	}
	log, _ := fs.ReadConsole(cons.ID)
	if !strings.Contains(log, "https://example.test/oauth") {
		t.Fatalf("log = %q", log)
	}
}

func TestRunPreservesPTYControlBytes(t *testing.T) {
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fs := fixstore.New(t.TempDir())
	cons := &models.FixerConsole{
		ID:     "cons-csi",
		UserID: "u1",
		Kind:   models.FixerConsoleLogin,
		Engine: "claude",
		Status: models.FixerConsoleQueued,
	}
	if err := store.EnqueueFixerConsole(context.Background(), cons); err != nil {
		t.Fatal(err)
	}
	cons, err = store.ClaimNextFixerConsole(context.Background(), "test-worker")
	if err != nil || cons == nil {
		t.Fatalf("claim: %+v %v", cons, err)
	}
	orig := CommandForTest
	CommandForTest = func(*models.FixerConsole) ([]string, error) {
		return []string{"/bin/sh", "-c", `printf '\033[2mEnter\r\033[22mconfirm'`}, nil
	}
	t.Cleanup(func() { CommandForTest = orig })

	if err := Run(context.Background(), store, fs, cons); err != nil {
		t.Fatalf("run: %v", err)
	}
	log, err := fs.ReadConsole(cons.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(log, "\nE\n") > 0 || strings.Contains(log, "\n[\n2\nm\n") {
		t.Fatalf("CSI was split into log lines: %q", log)
	}
	if !strings.Contains(log, "\x1b[2mEnter\r\x1b[22mconfirm") {
		t.Fatalf("raw PTY bytes missing from log: %q", log)
	}
}

func TestFirstURL(t *testing.T) {
	t.Parallel()
	got := firstURL(`Open https://claude.ai/oauth/authorize?x=1 then continue`)
	if got != "https://claude.ai/oauth/authorize?x=1" {
		t.Fatalf("got %q", got)
	}
	if firstURL("no link here") != "" {
		t.Fatal("expected empty")
	}
}

type cancelBeforeConsoleResult struct{ db.Store }

func (s cancelBeforeConsoleResult) UpdateFixerConsole(ctx context.Context, cons *models.FixerConsole) error {
	if cons.Status == models.FixerConsoleExited {
		latest, err := s.Store.GetFixerConsoleByID(ctx, cons.ID)
		if err != nil {
			return err
		}
		latest.Status = models.FixerConsoleCancelled
		if err := s.Store.UpdateFixerConsole(ctx, latest); err != nil {
			return err
		}
	}
	return s.Store.UpdateFixerConsole(ctx, cons)
}

func TestConsoleCancellationWinsOverLateResult(t *testing.T) {
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()
	cons := &models.FixerConsole{ID: "cancel-at-exit", Kind: models.FixerConsoleShell}
	if err := store.EnqueueFixerConsole(ctx, cons); err != nil {
		t.Fatal(err)
	}
	cons, err = store.ClaimNextFixerConsole(ctx, "worker")
	if err != nil || cons == nil {
		t.Fatalf("claim: %+v %v", cons, err)
	}
	original := CommandForTest
	CommandForTest = func(*models.FixerConsole) ([]string, error) {
		return []string{"/bin/echo", "https://example.test/login"}, nil
	}
	t.Cleanup(func() { CommandForTest = original })
	err = Run(ctx, cancelBeforeConsoleResult{store}, fixstore.New(t.TempDir()), cons)
	if !errors.Is(err, db.ErrFixerConsoleConflict) {
		t.Fatalf("late result: %v", err)
	}
	got, err := store.GetFixerConsoleByID(ctx, cons.ID)
	if err != nil || got.Status != models.FixerConsoleCancelled || got.LastURL != "https://example.test/login" {
		t.Fatalf("cancelled state lost: %+v %v", got, err)
	}
	// A stale runner cannot restart the cancelled session either.
	if err := Run(ctx, store, nil, cons); !errors.Is(err, db.ErrFixerConsoleConflict) {
		t.Fatalf("cancelled console restarted: %v", err)
	}
}
