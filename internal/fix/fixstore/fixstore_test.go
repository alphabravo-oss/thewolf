package fixstore

import (
	"context"
	"testing"
)

func TestLogRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	const job = "job-1"

	if got, err := s.ReadLog(job); err != nil || got != "" {
		t.Fatalf("empty log: got %q err %v", got, err)
	}
	if err := s.AppendLog(job, "line one"); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.AppendLog(job, "line two"); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := s.ReadLog(job)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "line one\nline two\n" {
		t.Errorf("log = %q", got)
	}
}

func TestConsoleLogRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	const id = "cons-1"
	if got, err := s.ReadConsole(id); err != nil || got != "" {
		t.Fatalf("empty: got %q err %v", got, err)
	}
	if err := s.AppendConsole(id, "open https://example.test/login"); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := s.ReadConsole(id)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "open https://example.test/login\n" {
		t.Errorf("log = %q", got)
	}
}

func TestConsoleRawPreservesCarriageReturnAndCSI(t *testing.T) {
	s := New(t.TempDir())
	const id = "cons-raw"
	chunk := []byte("\x1b[2mEnter\r\x1b[22mconfirm")
	if err := s.AppendConsoleRaw(id, chunk); err != nil {
		t.Fatalf("append raw: %v", err)
	}
	if err := s.AppendConsoleRaw(id, []byte("\n")); err != nil {
		t.Fatalf("append nl: %v", err)
	}
	got, err := s.ReadConsole(id)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != string(chunk)+"\n" {
		t.Fatalf("log = %q", got)
	}
}

func TestDiffRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	const job = "job-2"

	if got, err := s.ReadDiff(job); err != nil || got != "" {
		t.Fatalf("empty diff: got %q err %v", got, err)
	}
	const diff = "--- a\n+++ b\n"
	id, err := s.SaveDiff(context.Background(), job, diff)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if id != job+".diff" {
		t.Errorf("artifact id = %q", id)
	}
	got, err := s.ReadDiff(job)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != diff {
		t.Errorf("diff = %q", got)
	}
}
