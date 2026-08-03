// Package permission builds the opencode.json document Wolf writes for each
// run. OpenCode evaluates rules deny-wins, last-match-wins, and `--auto`
// auto-approves anything not explicitly denied — so every rule that must hold
// under yolo mode is a "deny", never an "ask".
package permission

import "encoding/json"

const schemaURL = "https://opencode.ai/config.json"

type document struct {
	Schema     string     `json:"$schema"`
	Permission permission `json:"permission"`
}

type permission struct {
	Edit              any               `json:"edit"`
	Bash              map[string]string `json:"bash"`
	ExternalDirectory map[string]string `json:"external_directory"`
}

// confineToWorktree denies the agent any path outside its working tree.
var confineToWorktree = map[string]string{"*": "deny"}

// Triage returns the read-only permission document for the plan run. Edit is
// denied outright and bash is an allowlist of inspection commands.
func Triage() ([]byte, error) {
	return json.MarshalIndent(document{
		Schema: schemaURL,
		Permission: permission{
			Edit: "deny",
			Bash: map[string]string{
				"*":          "deny",
				"git log *":  "allow",
				"git diff *": "allow",
				"git show *": "allow",
				"grep *":     "allow",
				"cat *":      "allow",
				"ls *":       "allow",
				"find *":     "allow",
			},
			ExternalDirectory: confineToWorktree,
		},
	}, "", "  ")
}

// Execute returns the scoped-write permission document for the fix run. The
// deny entries are the hard deny list: they are injected in gated and yolo
// mode alike. .github/** is denied because an agent that can edit CI can
// exfiltrate on its next run.
func Execute() ([]byte, error) {
	return json.MarshalIndent(document{
		Schema: schemaURL,
		Permission: permission{
			Edit: map[string]string{
				"*":          "allow",
				".github/**": "deny",
				"**/*.pem":   "deny",
				"**/*.key":   "deny",
			},
			Bash: map[string]string{
				// Default-deny: an unlisted command is refused, not allowed.
				// Under --auto an "ask" would degrade to allow, so the
				// fallback must be deny.
				"*":        "deny",
				"git *":    "allow",
				"npm *":    "allow",
				"go *":     "allow",
				"make *":   "allow",
				"pytest *": "allow",
				// Redundant under *: deny, but kept to document intent if the
				// allowlist is ever widened.
				"rm -rf *": "deny",
				"curl *":   "deny",
				"wget *":   "deny",
				"sudo *":   "deny",
			},
			ExternalDirectory: confineToWorktree,
		},
	}, "", "  ")
}
