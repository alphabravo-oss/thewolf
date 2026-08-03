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
	// Root wildcard, evaluated before every named key. It exists because
	// OpenCode treats an *unset* permission key as allow: an enumerated
	// blocklist silently permits every key the CLI grows after this file was
	// written, and silently permitted the ones it already had (webfetch and
	// websearch are direct network egress that never touches the bash
	// allowlist). Deny the whole surface, then re-allow only the keys a run
	// provably needs — task, skill, lsp, question, doom_loop, webfetch, and
	// websearch stay denied in both documents by never being named here.
	Any               string            `json:"*"`
	Read              string            `json:"read"`
	Glob              string            `json:"glob"`
	Grep              string            `json:"grep"`
	List              string            `json:"list"`
	TodoWrite         string            `json:"todowrite,omitempty"`
	Edit              any               `json:"edit"`
	Bash              map[string]string `json:"bash"`
	ExternalDirectory map[string]string `json:"external_directory"`
}

// confineToWorktree denies OpenCode's file tools any path outside the working
// tree. It does not constrain bash: the allowlisted inspection commands (cat,
// grep, find) still accept absolute paths, so this narrows the file-tool
// surface rather than sealing the container.
var confineToWorktree = map[string]string{"*": "deny"}

// readOnlyTools are the inspection keys both documents re-allow above the root
// deny. They are the agent's only way to see the repository once the file
// tools are the sole read path.
var readOnlyTools = permission{
	Any:  "deny",
	Read: "allow",
	Glob: "allow",
	Grep: "allow",
	List: "allow",
}

// Triage returns the read-only permission document for the plan run. Edit is
// denied outright and bash is an allowlist of inspection commands. todowrite
// stays denied here: triage reads and reports, it does not track work.
func Triage() ([]byte, error) {
	rules := readOnlyTools
	rules.Edit = "deny"
	rules.Bash = map[string]string{
		"*":          "deny",
		"git log *":  "allow",
		"git diff *": "allow",
		"git show *": "allow",
		"grep *":     "allow",
		"cat *":      "allow",
		"ls *":       "allow",
		"find *":     "allow",
	}
	rules.ExternalDirectory = confineToWorktree
	return json.MarshalIndent(document{Schema: schemaURL, Permission: rules}, "", "  ")
}

// Execute returns the scoped-write permission document for the fix run. The
// deny entries are the hard deny list: they are injected in gated and yolo
// mode alike. .github/** is denied because an agent that can edit CI can
// exfiltrate on its next run.
func Execute() ([]byte, error) {
	rules := readOnlyTools
	// The agent tracks multi-step fix work across turns; triage does not.
	rules.TodoWrite = "allow"
	rules.Edit = map[string]string{
		"*":          "allow",
		".github/**": "deny",
		"**/*.pem":   "deny",
		"**/*.key":   "deny",
		// OpenCode ranks project-level config above the custom config Wolf
		// passes by environment variable, and the scanned repository is
		// untrusted input — an agent that can author one of these outranks
		// the very document denying it.
		"opencode.json":  "deny",
		"opencode.jsonc": "deny",
		".opencode/**":   "deny",
	}
	rules.Bash = map[string]string{
		// Default-deny: an unlisted command is refused, not allowed.
		// Under --auto an "ask" would degrade to allow, so the
		// fallback must be deny.
		"*": "deny",
		// Scoped subcommands, never the bare binary: "git *" would
		// permit push/remote add, "npm *" would permit install
		// (network fetch plus arbitrary postinstall script
		// execution), and "go *" would permit get/run — each
		// reopens the egress the deny default exists to close. The
		// agent edits, builds, tests, and commits; it never pushes,
		// installs, or fetches. Wolf pushes the branch from the
		// host, outside this container.
		"git add *":      "allow",
		"git commit *":   "allow",
		"git checkout *": "allow",
		"git diff *":     "allow",
		"git log *":      "allow",
		"git status *":   "allow",
		"go build *":     "allow",
		"go test *":      "allow",
		"go vet *":       "allow",
		"gofmt *":        "allow",
		"npm test *":     "allow",
		"npm run *":      "allow",
		"make *":         "allow",
		"pytest *":       "allow",
		// Redundant under *: deny, but kept to document intent if the
		// allowlist is ever widened.
		"rm -rf *": "deny",
		"curl *":   "deny",
		"wget *":   "deny",
		"sudo *":   "deny",
	}
	rules.ExternalDirectory = confineToWorktree
	return json.MarshalIndent(document{Schema: schemaURL, Permission: rules}, "", "  ")
}
