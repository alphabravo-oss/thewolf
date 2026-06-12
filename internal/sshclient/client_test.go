package sshclient

import "testing"

func TestShellQuote(t *testing.T) {
	got := ShellQuote("repo's path")
	want := "'repo'\\''s path'"
	if got != want {
		t.Fatalf("ShellQuote() = %q, want %q", got, want)
	}
	if ShellQuote("") != "''" {
		t.Fatalf("ShellQuote empty did not return quoted empty string")
	}
}
