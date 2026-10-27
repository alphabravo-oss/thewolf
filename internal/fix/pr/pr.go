package pr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	gitpkg "github.com/alphabravocompany/thewolf/internal/gitops"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/scannerdiscovery"
	"github.com/alphabravocompany/thewolf/internal/scantarget"
)

// PRRequest holds the information needed to create a PR/MR.
type PRRequest struct {
	Token      string `json:"-"`
	Source     string
	RepoPath   string
	BranchName string
	BaseBranch string
	Category   models.Category
	Findings   []models.Finding
	JobID      string
	ScanID     string
	Evidence   map[string]string
	Validation string // "all pass" / "N/M pass"
}

// PRResult holds the outcome of PR creation.
type PRResult struct {
	URL   string
	Error string
}

// CreateGitHubPR creates a GitHub pull request using the gh CLI.
func CreateGitHubPR(ctx context.Context, req PRRequest) (*PRResult, error) {
	if strings.TrimSpace(req.Token) == "" {
		return nil, errors.New("GitHub PR creation requires the repository owner's configured token")
	}
	owner, repo, err := scantarget.ParseGitHubSource(req.Source)
	if err != nil {
		return nil, fmt.Errorf("GitHub PR source: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	configDir, err := os.MkdirTemp("", "wolf-gh-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(configDir)
	env := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "GH_") && !strings.HasPrefix(key, "GITHUB_") && !strings.HasPrefix(key, "GIT_") {
			env = append(env, entry)
		}
	}
	env = append(env, "GH_TOKEN="+req.Token, "GH_CONFIG_DIR="+configDir, "GH_PROMPT_DISABLED=1")
	run := func(args ...string) ([]byte, error) {
		cmd := plugin.CommandContext(ctx, "gh", args...) // #nosec G204 -- fixed executable, argument array, explicit repository and credential.
		cmd.Dir = configDir
		cmd.Env = env
		output, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("GitHub PR: %s: %w", scannerdiscovery.RedactText(strings.ReplaceAll(string(output), req.Token, "[REDACTED]")), err)
		}
		return output, nil
	}
	repository := "https://github.com/" + owner + "/" + repo
	output, err := run("pr", "list", "--repo", repository, "--state", "open", "--head", req.BranchName, "--base", req.BaseBranch, "--json", "url,isCrossRepository", "--limit", "100")
	if err != nil {
		return nil, err
	}
	var existing []struct {
		URL               string `json:"url"`
		IsCrossRepository bool   `json:"isCrossRepository"`
	}
	if err := json.Unmarshal(output, &existing); err != nil {
		return nil, fmt.Errorf("read existing PR: %w", err)
	}
	for _, candidate := range existing {
		if candidate.IsCrossRepository {
			continue
		}
		if !strings.HasPrefix(candidate.URL, repository+"/pull/") {
			return nil, errors.New("existing PR returned an invalid repository URL")
		}
		return &PRResult{URL: candidate.URL}, nil
	}
	title := scannerdiscovery.RedactText(fmt.Sprintf("[Wolf] Review %s remediation (%d findings)", req.Category, len(req.Findings)))
	body := strings.ReplaceAll(buildPRBody(req), req.Token, "[REDACTED]")
	output, err = run("pr", "create", "--repo", repository, "--title", title, "--body", body, "--base", req.BaseBranch, "--head", req.BranchName)
	if err != nil {
		return nil, err
	}
	url := strings.TrimSpace(string(output))
	if !strings.HasPrefix(url, repository+"/pull/") {
		return nil, errors.New("GitHub PR creation returned no valid repository PR URL")
	}
	return &PRResult{URL: url}, nil
}

// CreateGitLabMR creates a GitLab merge request using the glab CLI.
func CreateGitLabMR(ctx context.Context, req PRRequest) (*PRResult, error) {
	title := fmt.Sprintf("[Wolf] Review %s remediation (%d findings)", req.Category, len(req.Findings))
	body := buildPRBody(req)

	cmd := plugin.CommandContext(ctx, "glab", "mr", "create", // #nosec G204 -- command is a configured tool name; args from internal config, not user input
		"--title", title,
		"--description", body,
		"--source-branch", req.BranchName,
		"--target-branch", req.BaseBranch,
	)
	cmd.Dir = req.RepoPath

	output, err := cmd.CombinedOutput()
	if err != nil {
		return &PRResult{
			Error: fmt.Sprintf("glab mr create: %s: %s", string(output), err),
		}, nil
	}

	url := strings.TrimSpace(string(output))
	return &PRResult{URL: url}, nil
}

// PushBranch pushes the fix branch to the remote.
func PushBranch(ctx context.Context, repoPath, branchName string) error {
	cmd := gitpkg.Command(ctx, nil, "push", "-u", "origin", branchName) // #nosec G204 -- command is a configured tool name; args sourced from internal config
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push: %s: %w", string(output), err)
	}
	return nil
}

func buildPRBody(req PRRequest) string {
	var sb strings.Builder
	sb.WriteString("## Wolf Automated Fix\n\n")
	sb.WriteString(fmt.Sprintf("**Category:** %s\n", scannerdiscovery.RedactText(string(req.Category))))
	sb.WriteString(fmt.Sprintf("**Findings Addressed:** %d\n", len(req.Findings)))
	sb.WriteString(fmt.Sprintf("**Validation:** %s\n\n", scannerdiscovery.RedactText(req.Validation)))

	if req.JobID != "" {
		fmt.Fprintf(&sb, "**Fix job:** %s\n", scannerdiscovery.RedactText(req.JobID))
	}
	if req.ScanID != "" {
		fmt.Fprintf(&sb, "**Source scan:** %s\n", scannerdiscovery.RedactText(req.ScanID))
	}
	sb.WriteString("\nValidation below records targeted per-finding checks. It does not imply a full repository test run.\n\n")
	sb.WriteString("### Findings Addressed\n\n")
	for index, f := range req.Findings {
		var entry strings.Builder
		entry.WriteString(fmt.Sprintf("- **[%s]** %s (`%s:%d`)\n",
			strings.ToUpper(string(f.Severity)), scannerdiscovery.RedactText(f.Title), scannerdiscovery.RedactText(f.FilePath), f.LineStart))
		if f.ID != "" {
			fmt.Fprintf(&entry, "  - Finding: %s; tool/rule: %s / %s\n", scannerdiscovery.RedactText(f.ID), scannerdiscovery.RedactText(f.ToolName), scannerdiscovery.RedactText(f.RuleID))
		}
		evidence := req.Evidence[f.ID]
		if evidence == "" {
			evidence = "targeted verification evidence not recorded"
		}
		fmt.Fprintf(&entry, "  - Verification: %s\n", scannerdiscovery.RedactText(evidence))
		if sb.Len()+entry.Len() > 60000 {
			fmt.Fprintf(&sb, "\n%d additional finding(s) are recorded in the Wolf fix job report.\n", len(req.Findings)-index)
			break
		}
		sb.WriteString(entry.String())
	}

	sb.WriteString("\n---\n")
	sb.WriteString("*Generated by [The Wolf](https://github.com/alphabravocompany/thewolf) — AI-Powered Code Analysis & Fix Engine*\n")
	return sb.String()
}
