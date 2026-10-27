package scantarget

import "testing"

func TestParseGitLabSource(t *testing.T) {
	good := map[string][2]string{
		"https://gitlab.com/group/project":                {"gitlab.com", "group/project"},
		"https://gitlab.com/group/sub/deeper/project.git": {"gitlab.com", "group/sub/deeper/project"},
		"https://gitlab.example.com:8443/team/app.git/":   {"gitlab.example.com:8443", "team/app"},
		"https://GitLab.Example.com:443/team/app":         {"gitlab.example.com", "team/app"},
	}
	for in, want := range good {
		host, project, err := ParseGitLabSource(in)
		if err != nil || host != want[0] || project != want[1] {
			t.Errorf("ParseGitLabSource(%q) = %q %q %v; want %v", in, host, project, err, want)
		}
	}
	for _, in := range []string{"", "gitlab.com/group/project", "http://gitlab.com/g/p", "ssh://git@gitlab.com/g/p.git", "git@gitlab.com:g/p.git", "https://gitlab.com/solo", "https://gitlab.com/g/../p", "https://u:p@gitlab.com/g/p", "https://gitlab.com/g/p?x=1", "https://gitlab.com/g//p"} {
		if _, _, err := ParseGitLabSource(in); err == nil {
			t.Errorf("accepted %q", in)
		}
	}
}
