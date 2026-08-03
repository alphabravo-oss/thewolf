package scantarget

import (
	"context"
	"testing"
)

func TestCanonicalGitURLAllowsHTTPSAndSSHOnly(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		host    string
		wantErr bool
	}{
		{"https://Git.Example.com/acme/repo.git/", "https://git.example.com/acme/repo.git", "git.example.com", false},
		{"git@git.example.com:acme/repo.git", "ssh://git@git.example.com/acme/repo.git", "git.example.com", false},
		{"ssh://git@git.example.com/acme/repo.git", "ssh://git@git.example.com/acme/repo.git", "git.example.com", false},
		{"ssh://git@git.example.com:22/acme/repo.git", "ssh://git@git.example.com/acme/repo.git", "git.example.com", false},
		{"https://git.example.com:443/acme/repo.git", "https://git.example.com/acme/repo.git", "git.example.com", false},
		{"file:///tmp/repo", "", "", true},
		{"git://git.example.com/repo", "", "", true},
		{"https://token@git.example.com/repo", "", "", true},
		{"https://user:secret@git.example.com/repo", "", "", true},
		{"ssh://git.example.com/repo", "", "", true},
		{"ssh://-oProxyCommand@git.example.com/repo", "", "", true},
		{"ssh://git@-bad.example.com/repo", "", "", true},
		{"https://git.example.com", "", "", true},
		{"/tmp/repo", "", "", true},
	}
	for _, test := range tests {
		got, host, err := CanonicalGitURL(test.input)
		if (err != nil) != test.wantErr {
			t.Fatalf("CanonicalGitURL(%q) err=%v wantErr=%v", test.input, err, test.wantErr)
		}
		if !test.wantErr && (got != test.want || host != test.host) {
			t.Fatalf("CanonicalGitURL(%q)=(%q,%q), want (%q,%q)", test.input, got, host, test.want, test.host)
		}
	}
}

func TestValidateGitDestinationBlocksLocalhost(t *testing.T) {
	if err := ValidateRemoteDestination(context.Background(), "localhost"); err == nil {
		t.Fatal("expected localhost to be blocked")
	}
	if err := ValidateRemoteDestination(context.Background(), "127.0.0.1"); err == nil {
		t.Fatal("expected loopback IP to be blocked")
	}
}
