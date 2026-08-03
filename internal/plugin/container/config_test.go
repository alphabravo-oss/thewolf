package container

import (
	"strings"
	"testing"
)

func TestParsePullPolicy(t *testing.T) {
	tests := []struct {
		in      string
		want    PullPolicy
		wantErr bool
	}{
		{"", PullIfNotPresent, false},
		{"IfNotPresent", PullIfNotPresent, false},
		{"ifnotpresent", PullIfNotPresent, false},
		{"if-not-present", PullIfNotPresent, false},
		{"Always", PullAlways, false},
		{"ALWAYS", PullAlways, false},
		{"Never", PullNever, false},
		{"  Never  ", PullNever, false},
		{"bogus", PullIfNotPresent, true},
	}
	for _, tc := range tests {
		got, err := ParsePullPolicy(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParsePullPolicy(%q) err=%v, wantErr=%v", tc.in, err, tc.wantErr)
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("ParsePullPolicy(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestPullPolicy_String(t *testing.T) {
	cases := []struct {
		p    PullPolicy
		want string
	}{
		{PullIfNotPresent, "IfNotPresent"},
		{PullAlways, "Always"},
		{PullNever, "Never"},
	}
	for _, c := range cases {
		if got := c.p.String(); got != c.want {
			t.Errorf("(%d).String() = %q, want %q", c.p, got, c.want)
		}
	}
}

func TestConfig_Validate(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		var c *Config
		if err := c.Validate(); err == nil {
			t.Error("expected error for nil config")
		}
	})
	t.Run("disabled_ok", func(t *testing.T) {
		c := &Config{Disabled: true}
		if err := c.Validate(); err != nil {
			t.Errorf("disabled config should validate: %v", err)
		}
	})
	t.Run("empty_image", func(t *testing.T) {
		c := &Config{}
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "image is empty") {
			t.Errorf("want image-empty error, got %v", err)
		}
	})
	t.Run("default_network", func(t *testing.T) {
		c := &Config{Image: "x:y", Network: ""}
		if err := c.Validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Network != "bridge" {
			t.Errorf("Network defaulted to %q, want bridge", c.Network)
		}
	})
	t.Run("repos_root_pairing", func(t *testing.T) {
		c := &Config{Image: "x:y", HostReposRoot: "/host", InContainerReposRoot: ""}
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "must both be set") {
			t.Errorf("want pairing error, got %v", err)
		}
	})
	t.Run("workspace_root_pairing", func(t *testing.T) {
		c := &Config{Image: "x:y", HostWorkspaceRoot: "/host", InContainerWorkspaceRoot: ""}
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "must both be set") {
			t.Errorf("want pairing error, got %v", err)
		}
	})
}

func TestConfig_TranslateRepoPath(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		in      string
		want    string
		wantErr bool
	}{
		{
			"dev_mode_no_translation",
			&Config{},
			"/abs/path",
			"/abs/path",
			false,
		},
		{
			"prod_mode_translates_subpath",
			&Config{HostReposRoot: "/host/projects", InContainerReposRoot: "/repos"},
			"/repos/myrepo",
			"/host/projects/myrepo",
			false,
		},
		{
			"prod_mode_translates_root",
			&Config{HostReposRoot: "/host/projects", InContainerReposRoot: "/repos"},
			"/repos",
			"/host/projects",
			false,
		},
		{
			"prod_mode_translates_workspace",
			&Config{
				HostReposRoot: "/host/projects", InContainerReposRoot: "/repos",
				HostWorkspaceRoot: "/host/workspaces", InContainerWorkspaceRoot: "/workspaces",
			},
			"/workspaces/wolf-git-scan-123",
			"/host/workspaces/wolf-git-scan-123",
			false,
		},
		{
			"prod_mode_unrelated_path_rejected",
			&Config{HostReposRoot: "/host/projects", InContainerReposRoot: "/repos"},
			"/elsewhere/x",
			"",
			true,
		},
		{
			"prod_mode_traversal_rejected",
			&Config{HostReposRoot: "/host/projects", InContainerReposRoot: "/repos"},
			"/repos/../etc",
			"",
			true,
		},
		{
			"prod_mode_trailing_slash_in_root",
			&Config{HostReposRoot: "/host/projects", InContainerReposRoot: "/repos/"},
			"/repos/myrepo",
			"/host/projects/myrepo",
			false,
		},
		{
			"nil_config_safe",
			nil,
			"/x",
			"/x",
			false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.cfg.TranslateRepoPath(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("TranslateRepoPath(%q) expected error, got nil", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("TranslateRepoPath(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("TranslateRepoPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.Image == "" {
		t.Error("default Image is empty")
	}
	if c.Network != "bridge" {
		t.Errorf("default Network = %q, want bridge", c.Network)
	}
	if c.PullPolicy != PullIfNotPresent {
		t.Errorf("default PullPolicy = %v, want IfNotPresent", c.PullPolicy)
	}
}
