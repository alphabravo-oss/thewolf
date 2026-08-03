package scannergit

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerdiscovery"
)

const (
	defaultGitHubBaseURL = "https://api.github.com"
	maxGitHubResponse    = int64(2 << 20)
	maxProposalFiles     = 1024
	maxProposalFileBytes = 4 << 20
	maxProposalBytes     = 32 << 20
)

var (
	gitHubNamePattern   = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	gitObjectPattern    = regexp.MustCompile(`^[a-f0-9]{40}$`)
	gitStatusContext    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._:/-]{0,99}$`)
	gitHubStatusStates  = map[string]bool{"error": true, "failure": true, "pending": true, "success": true}
	gitHubAllowedModes  = map[string]bool{"100644": true, "100755": true}
	gitHubHeaderTimeout = 30 * time.Second
)

type GitHubConfig struct {
	BaseURL    string
	Owner      string
	Repository string
	Token      string
	UserAgent  string
	HTTPClient *http.Client
}

// GitHubProvider writes a proposal through GitHub's Git Data API. It never
// force-pushes. An existing proposal branch can be advanced only when the
// caller supplies the exact current head, preventing automation from
// overwriting human changes made after the last observation.
type GitHubProvider struct {
	baseURL    *url.URL
	owner      string
	repository string
	token      string
	userAgent  string
	client     *http.Client
}

var _ Provider = (*GitHubProvider)(nil)

func NewGitHubProvider(config GitHubConfig) (*GitHubProvider, error) {
	if config.BaseURL == "" {
		config.BaseURL = defaultGitHubBaseURL
	}
	base, err := url.Parse(config.BaseURL)
	if err != nil || base.Host == "" || base.User != nil ||
		base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("%w: GitHub API base URL is invalid", ErrValidation)
	}
	if base.Scheme != "https" && !(base.Scheme == "http" && loopbackHost(base.Hostname())) {
		return nil, fmt.Errorf("%w: GitHub API must use HTTPS", ErrValidation)
	}
	if !gitHubNamePattern.MatchString(config.Owner) ||
		!gitHubNamePattern.MatchString(config.Repository) {
		return nil, fmt.Errorf("%w: GitHub owner and repository are invalid", ErrValidation)
	}
	if strings.TrimSpace(config.Token) == "" {
		return nil, fmt.Errorf("%w: GitHub credential is required", ErrValidation)
	}
	if config.UserAgent == "" {
		config.UserAgent = "wolf-scanner-release"
	}
	if strings.ContainsAny(config.UserAgent, "\r\n") {
		return nil, fmt.Errorf("%w: GitHub user agent is invalid", ErrValidation)
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: gitHubHeaderTimeout}
	}
	base.Path = strings.TrimSuffix(base.Path, "/")
	return &GitHubProvider{
		baseURL: base, owner: config.Owner, repository: config.Repository,
		token: config.Token, userAgent: config.UserAgent, client: config.HTTPClient,
	}, nil
}

func (p *GitHubProvider) CreateProposal(
	ctx context.Context,
	proposal Proposal,
) (ProposalResult, error) {
	files, err := validateProposal(proposal)
	if err != nil {
		return ProposalResult{}, err
	}
	baseHead, found, err := p.getRef(ctx, proposal.BaseBranch)
	if err != nil {
		return ProposalResult{}, err
	}
	if !found || baseHead != proposal.ExpectedBaseCommit {
		return ProposalResult{}, fmt.Errorf(
			"%w: base branch %q moved from %s to %s",
			ErrConflict, proposal.BaseBranch, proposal.ExpectedBaseCommit, baseHead,
		)
	}

	branchHead, branchExists, err := p.getRef(ctx, proposal.Branch)
	if err != nil {
		return ProposalResult{}, err
	}
	if branchExists && proposal.ExpectedBranchHead != "" && branchHead != proposal.ExpectedBranchHead {
		return ProposalResult{}, fmt.Errorf(
			"%w: proposal branch %q is at %s, expected %s",
			ErrConflict, proposal.Branch, branchHead, proposal.ExpectedBranchHead,
		)
	}
	if !branchExists && proposal.ExpectedBranchHead != "" {
		return ProposalResult{}, fmt.Errorf(
			"%w: proposal branch %q was deleted", ErrConflict, proposal.Branch,
		)
	}

	parentCommit := baseHead
	commit := baseHead
	changed := false
	if branchExists && proposal.ExpectedBranchHead == "" {
		// The external Git write may have succeeded immediately before a worker
		// crash. Adopt only the exact deterministic tree whose sole parent is
		// the still-current base. Any human or unrelated branch content fails
		// closed without moving the ref.
		identity, identityErr := p.getCommitIdentity(ctx, branchHead)
		if identityErr != nil {
			return ProposalResult{}, identityErr
		}
		if len(identity.Parents) != 1 || identity.Parents[0] != baseHead {
			return ProposalResult{}, fmt.Errorf(
				"%w: proposal branch %q is not an adoptable child of %s",
				ErrConflict, proposal.Branch, baseHead,
			)
		}
		baseTree, treeErr := p.getCommitTree(ctx, baseHead)
		if treeErr != nil {
			return ProposalResult{}, treeErr
		}
		desiredTree, treeErr := p.createFileTree(ctx, baseTree, files)
		if treeErr != nil {
			return ProposalResult{}, treeErr
		}
		if identity.Tree != desiredTree {
			return ProposalResult{}, fmt.Errorf(
				"%w: proposal branch %q content differs from the deterministic retry",
				ErrConflict, proposal.Branch,
			)
		}
		commit = branchHead
	} else {
		if branchExists {
			parentCommit = branchHead
			commit = branchHead
		}
		parentTree, treeErr := p.getCommitTree(ctx, parentCommit)
		if treeErr != nil {
			return ProposalResult{}, treeErr
		}
		tree, treeErr := p.createFileTree(ctx, parentTree, files)
		if treeErr != nil {
			return ProposalResult{}, treeErr
		}
		changed = tree != parentTree
		if !branchExists && !changed {
			return ProposalResult{}, fmt.Errorf("%w: generated proposal has no definition changes", ErrValidation)
		}
		if changed {
			commit, err = p.createCommit(ctx, proposal.CommitMessage, tree, parentCommit)
			if err != nil {
				return ProposalResult{}, err
			}
			if branchExists {
				if err := p.updateRef(ctx, proposal.Branch, commit); err != nil {
					return ProposalResult{}, err
				}
			} else if err := p.createRef(ctx, proposal.Branch, commit); err != nil {
				return ProposalResult{}, err
			}
		}
	}

	result, err := p.findPullRequest(ctx, proposal.Branch, proposal.BaseBranch)
	if err != nil {
		return ProposalResult{}, err
	}
	created := false
	if result.PullRequest == 0 {
		result, err = p.createPullRequest(ctx, proposal)
		if err != nil {
			return ProposalResult{}, err
		}
		created = true
	}
	if len(proposal.Labels) != 0 {
		if err := p.setLabels(ctx, result.PullRequest, proposal.Labels); err != nil {
			return ProposalResult{}, err
		}
	}
	result.Branch = proposal.Branch
	result.Commit = commit
	result.Created = created
	return result, nil
}

func (p *GitHubProvider) createFileTree(
	ctx context.Context,
	baseTree string,
	files []File,
) (string, error) {
	entries := make([]gitHubTreeEntry, 0, len(files))
	for _, file := range files {
		if file.Delete {
			entries = append(entries, gitHubTreeEntry{
				Path: file.Path, Mode: file.Mode, Type: "blob",
			})
			continue
		}
		blob, blobErr := p.createBlob(ctx, file.Content)
		if blobErr != nil {
			return "", blobErr
		}
		entries = append(entries, gitHubTreeEntry{
			Path: file.Path, Mode: file.Mode, Type: "blob", SHA: &blob,
		})
	}
	return p.createTree(ctx, baseTree, entries)
}

func (p *GitHubProvider) SetCommitStatus(
	ctx context.Context,
	commit string,
	status CommitStatus,
) error {
	status.State = strings.ToLower(strings.TrimSpace(status.State))
	if !gitObjectPattern.MatchString(commit) ||
		!gitHubStatusStates[status.State] ||
		!gitStatusContext.MatchString(status.Context) ||
		len(status.Description) > 140 {
		return fmt.Errorf("%w: invalid GitHub commit status", ErrValidation)
	}
	if status.TargetURL != "" {
		target, err := url.Parse(status.TargetURL)
		if err != nil || target.Scheme != "https" || target.Host == "" || target.User != nil {
			return fmt.Errorf("%w: commit status target URL must be credential-free HTTPS", ErrValidation)
		}
	}
	return p.request(ctx, http.MethodPost, "statuses/"+commit, map[string]string{
		"state": status.State, "context": status.Context,
		"description": status.Description, "target_url": status.TargetURL,
	}, nil, http.StatusCreated)
}

type gitHubRef struct {
	Object struct {
		SHA string `json:"sha"`
	} `json:"object"`
}

func (p *GitHubProvider) getRef(ctx context.Context, branch string) (string, bool, error) {
	var result gitHubRef
	err := p.request(
		ctx, http.MethodGet, "git/ref/heads/"+branch,
		nil, &result, http.StatusOK,
	)
	if errors.Is(err, errGitHubNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !gitObjectPattern.MatchString(result.Object.SHA) {
		return "", false, errors.New("GitHub returned an invalid ref object")
	}
	return result.Object.SHA, true, nil
}

type gitCommitIdentity struct {
	Tree    string
	Parents []string
}

func (p *GitHubProvider) getCommitIdentity(ctx context.Context, commit string) (gitCommitIdentity, error) {
	var result struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
		Parents []struct {
			SHA string `json:"sha"`
		} `json:"parents"`
	}
	if err := p.request(ctx, http.MethodGet, "git/commits/"+commit, nil, &result, http.StatusOK); err != nil {
		return gitCommitIdentity{}, err
	}
	if !gitObjectPattern.MatchString(result.Tree.SHA) {
		return gitCommitIdentity{}, errors.New("GitHub returned an invalid commit tree")
	}
	identity := gitCommitIdentity{Tree: result.Tree.SHA, Parents: make([]string, len(result.Parents))}
	for index, parent := range result.Parents {
		if !gitObjectPattern.MatchString(parent.SHA) {
			return gitCommitIdentity{}, errors.New("GitHub returned an invalid commit parent")
		}
		identity.Parents[index] = parent.SHA
	}
	return identity, nil
}

func (p *GitHubProvider) getCommitTree(ctx context.Context, commit string) (string, error) {
	identity, err := p.getCommitIdentity(ctx, commit)
	if err != nil {
		return "", err
	}
	return identity.Tree, nil
}

func (p *GitHubProvider) createBlob(ctx context.Context, content []byte) (string, error) {
	var result struct {
		SHA string `json:"sha"`
	}
	if err := p.request(ctx, http.MethodPost, "git/blobs", map[string]string{
		"content": base64.StdEncoding.EncodeToString(content), "encoding": "base64",
	}, &result, http.StatusCreated); err != nil {
		return "", err
	}
	if !gitObjectPattern.MatchString(result.SHA) {
		return "", errors.New("GitHub returned an invalid blob object")
	}
	return result.SHA, nil
}

type gitHubTreeEntry struct {
	Path string  `json:"path"`
	Mode string  `json:"mode"`
	Type string  `json:"type"`
	SHA  *string `json:"sha"`
}

func (p *GitHubProvider) createTree(
	ctx context.Context,
	base string,
	entries []gitHubTreeEntry,
) (string, error) {
	var result struct {
		SHA string `json:"sha"`
	}
	if err := p.request(ctx, http.MethodPost, "git/trees", map[string]any{
		"base_tree": base, "tree": entries,
	}, &result, http.StatusCreated); err != nil {
		return "", err
	}
	if !gitObjectPattern.MatchString(result.SHA) {
		return "", errors.New("GitHub returned an invalid tree object")
	}
	return result.SHA, nil
}

func (p *GitHubProvider) createCommit(
	ctx context.Context,
	message, tree, parent string,
) (string, error) {
	var result struct {
		SHA string `json:"sha"`
	}
	if err := p.request(ctx, http.MethodPost, "git/commits", map[string]any{
		"message": message, "tree": tree, "parents": []string{parent},
	}, &result, http.StatusCreated); err != nil {
		return "", err
	}
	if !gitObjectPattern.MatchString(result.SHA) {
		return "", errors.New("GitHub returned an invalid commit object")
	}
	return result.SHA, nil
}

func (p *GitHubProvider) createRef(ctx context.Context, branch, commit string) error {
	return p.request(ctx, http.MethodPost, "git/refs", map[string]string{
		"ref": "refs/heads/" + branch, "sha": commit,
	}, nil, http.StatusCreated)
}

func (p *GitHubProvider) updateRef(ctx context.Context, branch, commit string) error {
	return p.request(
		ctx, http.MethodPatch, "git/refs/heads/"+branch,
		map[string]any{"sha": commit, "force": false}, nil, http.StatusOK,
	)
}

func (p *GitHubProvider) findPullRequest(
	ctx context.Context,
	branch, base string,
) (ProposalResult, error) {
	query := url.Values{
		"state": {"open"}, "base": {base}, "head": {p.owner + ":" + branch},
	}
	var pulls []struct {
		Number  int64  `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if err := p.request(
		ctx, http.MethodGet, "pulls?"+query.Encode(), nil, &pulls, http.StatusOK,
	); err != nil {
		return ProposalResult{}, err
	}
	if len(pulls) == 0 {
		return ProposalResult{}, nil
	}
	if pulls[0].Number <= 0 || !safeGitHubWebURL(pulls[0].HTMLURL) {
		return ProposalResult{}, errors.New("GitHub returned an invalid pull request")
	}
	return ProposalResult{PullRequest: pulls[0].Number, URL: pulls[0].HTMLURL}, nil
}

func (p *GitHubProvider) createPullRequest(
	ctx context.Context,
	proposal Proposal,
) (ProposalResult, error) {
	var pull struct {
		Number  int64  `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if err := p.request(ctx, http.MethodPost, "pulls", map[string]any{
		"title": proposal.Title, "body": proposal.Body,
		"head": proposal.Branch, "base": proposal.BaseBranch,
		"maintainer_can_modify": false,
	}, &pull, http.StatusCreated); err != nil {
		return ProposalResult{}, err
	}
	if pull.Number <= 0 || !safeGitHubWebURL(pull.HTMLURL) {
		return ProposalResult{}, errors.New("GitHub returned an invalid pull request")
	}
	return ProposalResult{PullRequest: pull.Number, URL: pull.HTMLURL}, nil
}

func (p *GitHubProvider) setLabels(
	ctx context.Context,
	pullRequest int64,
	labels []string,
) error {
	return p.request(
		ctx, http.MethodPost, fmt.Sprintf("issues/%d/labels", pullRequest),
		map[string]any{"labels": labels}, nil, http.StatusOK,
	)
}

var errGitHubNotFound = errors.New("GitHub resource not found")

func (p *GitHubProvider) request(
	ctx context.Context,
	method, relative string,
	body, destination any,
	expectedStatus int,
) error {
	endpoint := *p.baseURL
	query := ""
	if index := strings.IndexByte(relative, '?'); index >= 0 {
		query = relative[index+1:]
		relative = relative[:index]
	}
	endpoint.Path = path.Join(endpoint.Path, "repos", p.owner, p.repository, relative)
	endpoint.RawQuery = query
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+p.token)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", p.userAgent)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := p.client.Do(request)
	if err != nil {
		return fmt.Errorf("GitHub request failed: %w", err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maxGitHubResponse+1)
	value, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read GitHub response: %w", err)
	}
	if int64(len(value)) > maxGitHubResponse {
		return errors.New("GitHub response exceeds size limit")
	}
	if response.StatusCode == http.StatusNotFound {
		return errGitHubNotFound
	}
	if response.StatusCode != expectedStatus {
		return fmt.Errorf(
			"GitHub API returned %d: %s",
			response.StatusCode, scannerdiscovery.RedactText(string(value)),
		)
	}
	if destination == nil || len(bytes.TrimSpace(value)) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("GitHub returned multiple JSON values")
	}
	return nil
}

func validateProposal(proposal Proposal) ([]File, error) {
	switch {
	case !validBranch(proposal.BaseBranch), !validBranch(proposal.Branch):
		return nil, fmt.Errorf("%w: invalid Git branch", ErrValidation)
	case proposal.BaseBranch == proposal.Branch:
		return nil, fmt.Errorf("%w: base and proposal branches must differ", ErrValidation)
	case !gitObjectPattern.MatchString(proposal.ExpectedBaseCommit):
		return nil, fmt.Errorf("%w: expected base commit must be a full Git SHA-1", ErrValidation)
	case proposal.ExpectedBranchHead != "" && !gitObjectPattern.MatchString(proposal.ExpectedBranchHead):
		return nil, fmt.Errorf("%w: expected branch head must be a full Git SHA-1", ErrValidation)
	case strings.TrimSpace(proposal.CommitMessage) == "", len(proposal.CommitMessage) > 4096:
		return nil, fmt.Errorf("%w: commit message is required and bounded", ErrValidation)
	case strings.TrimSpace(proposal.Title) == "", len(proposal.Title) > 256:
		return nil, fmt.Errorf("%w: pull request title is required and bounded", ErrValidation)
	case len(proposal.Body) > 64<<10:
		return nil, fmt.Errorf("%w: pull request body exceeds 64 KiB", ErrValidation)
	case len(proposal.Files) == 0 || len(proposal.Files) > maxProposalFiles:
		return nil, fmt.Errorf("%w: proposal must contain 1 through %d files", ErrValidation, maxProposalFiles)
	}
	files := append([]File(nil), proposal.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	seen := make(map[string]bool, len(files))
	total := 0
	for index := range files {
		file := &files[index]
		if file.Mode == "" {
			file.Mode = "100644"
		}
		clean := path.Clean(file.Path)
		if clean != file.Path || strings.HasPrefix(clean, "/") ||
			clean == "." || clean == ".." || strings.HasPrefix(clean, "../") ||
			strings.ContainsAny(clean, "\\\x00") ||
			clean == ".git" || strings.HasPrefix(clean, ".git/") {
			return nil, fmt.Errorf("%w: unsafe proposal file path %q", ErrValidation, file.Path)
		}
		if seen[clean] {
			return nil, fmt.Errorf("%w: duplicate proposal file %q", ErrValidation, clean)
		}
		seen[clean] = true
		if !gitHubAllowedModes[file.Mode] {
			return nil, fmt.Errorf("%w: unsupported file mode %q", ErrValidation, file.Mode)
		}
		if file.Delete && len(file.Content) != 0 {
			return nil, fmt.Errorf(
				"%w: deleted proposal file %q must not contain content",
				ErrValidation, file.Path,
			)
		}
		if len(file.Content) > maxProposalFileBytes {
			return nil, fmt.Errorf("%w: proposal file %q exceeds size limit", ErrValidation, file.Path)
		}
		total += len(file.Content)
		if total > maxProposalBytes {
			return nil, fmt.Errorf("%w: proposal contents exceed size limit", ErrValidation)
		}
	}
	labels := make(map[string]bool, len(proposal.Labels))
	for _, label := range proposal.Labels {
		if strings.TrimSpace(label) == "" || len(label) > 50 || strings.ContainsAny(label, "\r\n") {
			return nil, fmt.Errorf("%w: invalid pull request label", ErrValidation)
		}
		if labels[label] {
			return nil, fmt.Errorf("%w: duplicate pull request label %q", ErrValidation, label)
		}
		labels[label] = true
	}
	return files, nil
}

// ValidateProposal applies the same bounded path, content, branch, commit,
// label, and body checks used by CreateProposal without performing any Git
// provider writes. Managed generators call this at their failure-atomic write
// boundary so an invalid proposal never reaches even a test or alternate
// provider implementation.
func ValidateProposal(proposal Proposal) error {
	_, err := validateProposal(proposal)
	return err
}

func validBranch(value string) bool {
	if value == "" || len(value) > 200 || strings.HasPrefix(value, "/") ||
		strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") ||
		strings.Contains(value, "..") || strings.Contains(value, "@{") ||
		strings.ContainsAny(value, " ~^:?*[\\\x00\r\n") {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." ||
			strings.HasSuffix(component, ".lock") {
			return false
		}
	}
	return true
}

func safeGitHubWebURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" &&
		parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
