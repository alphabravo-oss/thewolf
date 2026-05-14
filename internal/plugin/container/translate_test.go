package container

import "testing"

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"scan_root_alone", "/scan", ""},
		{"scan_with_file", "/scan/foo.py", "foo.py"},
		{"scan_nested", "/scan/pkg/sub/foo.py", "pkg/sub/foo.py"},
		{"scan_with_trailing_slash", "/scan/", ""}, // treated as equivalent to "/scan" (the root of the mount)
		{"relative", "foo.py", "foo.py"},
		{"absolute_unrelated", "/etc/passwd", "/etc/passwd"},
		{"empty", "", ""},
		{"scan_prefix_in_subpath", "/scanner/foo.py", "/scanner/foo.py"},
		{"scan_prefix_not_dir_boundary", "/scan-extra/foo", "/scan-extra/foo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizePath(tc.in)
			if got != tc.want {
				t.Errorf("NormalizePath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestContainerSubPath(t *testing.T) {
	cases := []struct {
		repo, sub, want string
	}{
		{"/home/me/repo", "/home/me/repo/cmd/foo", "/scan/cmd/foo"},
		{"/home/me/repo", "/home/me/repo", "/scan"},
		{"/home/me/repo", "/elsewhere", "/scan"},
		{"/home/me/repo", "", "/scan"},
		{"", "/x", "/scan"},
		{"/home/me/repo", "/home/me/repo-other", "/scan"}, // not a subdir even though prefix matches
	}
	for _, c := range cases {
		got := ContainerSubPath(c.repo, c.sub)
		if got != c.want {
			t.Errorf("ContainerSubPath(%q, %q) = %q, want %q", c.repo, c.sub, got, c.want)
		}
	}
}

func TestNormalizePaths(t *testing.T) {
	paths := []string{"/scan/a", "/scan/b/c", "external", "/scan"}
	NormalizePaths(paths)
	want := []string{"a", "b/c", "external", ""}
	for i := range paths {
		if paths[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}
