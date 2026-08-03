package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/thewolf/internal/scannergit"
	"github.com/alphabravocompany/thewolf/internal/scannerproposal"
	"github.com/alphabravocompany/thewolf/internal/scannerproposalworker"
)

const maximumScannerProposalRequest = int64(4 << 20)

func newScannerProposalExecutorCmd() *cobra.Command {
	var (
		repositoryPath string
		repositoryURL  string
		githubAPI      string
		githubOwner    string
		githubRepo     string
		credentialFile string
		baseBranch     string
		branchPrefix   string
		lockURIPrefix  string
		tempRoot       string
		gitPath        string
		goPath         string
		labels         []string
		requireStatus  bool
	)
	command := &cobra.Command{
		Use:    "scanner-proposal-executor",
		Short:  "Run the managed scanner definition proposal JSON protocol",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			request, err := readScannerProposalRequest(cmd.InOrStdin())
			if err != nil {
				return err
			}
			credential, err := readScannerProposalCredential(
				credentialFile, os.Getenv("WOLF_SCANNER_PROPOSAL_GITHUB_TOKEN"),
			)
			if err != nil {
				return err
			}
			provider, err := scannergit.NewGitHubProvider(scannergit.GitHubConfig{
				BaseURL: githubAPI, Owner: githubOwner, Repository: githubRepo,
				Token: credential, UserAgent: "wolf-scanner-proposal-executor",
			})
			if err != nil {
				return fmt.Errorf("configure managed scanner Git provider: %w", err)
			}
			if repositoryPath == "" && repositoryURL == "" {
				repositoryURL = "https://github.com/" + githubOwner + "/" + githubRepo + ".git"
			}
			if lockURIPrefix == "" {
				lockURIPrefix = "git://github.com/" + githubOwner + "/" + githubRepo +
					"/scanners/scanner-lock.yaml@"
			}
			managed := scannerproposal.Managed{
				Generator: scannerproposal.CheckoutGenerator{
					RepositoryPath: repositoryPath, RepositoryURL: repositoryURL,
					GitCredential: credential, TempRoot: tempRoot,
					GitPath: gitPath, GoPath: goPath, LockURIPrefix: lockURIPrefix,
					Editor: scannerproposal.SelectedUpdateEditor{GoPath: goPath},
				},
				Git: provider, BaseBranch: baseBranch, BranchPrefix: branchPrefix,
				Labels: append([]string(nil), labels...), RequireStatus: requireStatus,
			}
			result, err := managed.Propose(cmd.Context(), request)
			if err != nil {
				return err
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetEscapeHTML(true)
			if err := encoder.Encode(result); err != nil {
				return fmt.Errorf("encode scanner proposal result: %w", err)
			}
			return nil
		},
	}
	flags := command.Flags()
	flags.StringVar(&repositoryPath, "repository-path", os.Getenv("WOLF_SCANNER_PROPOSAL_REPOSITORY_PATH"), "read-only local scanner definition Git object store")
	flags.StringVar(&repositoryURL, "repository-url", os.Getenv("WOLF_SCANNER_PROPOSAL_REPOSITORY_URL"), "credential-free HTTPS scanner definition Git URL")
	flags.StringVar(&githubAPI, "github-api", envOr("WOLF_SCANNER_PROPOSAL_GITHUB_API", "https://api.github.com"), "GitHub API base URL")
	flags.StringVar(&githubOwner, "github-owner", os.Getenv("WOLF_SCANNER_PROPOSAL_GITHUB_OWNER"), "GitHub repository owner")
	flags.StringVar(&githubRepo, "github-repository", os.Getenv("WOLF_SCANNER_PROPOSAL_GITHUB_REPOSITORY"), "GitHub repository name")
	flags.StringVar(&credentialFile, "github-token-file", os.Getenv("WOLF_SCANNER_PROPOSAL_GITHUB_TOKEN_FILE"), "mounted GitHub token file")
	flags.StringVar(&baseBranch, "base-branch", envOr("WOLF_SCANNER_PROPOSAL_BASE_BRANCH", "main"), "proposal base branch")
	flags.StringVar(&branchPrefix, "branch-prefix", envOr("WOLF_SCANNER_PROPOSAL_BRANCH_PREFIX", "wolf/scanner-release"), "managed proposal branch prefix")
	flags.StringVar(&lockURIPrefix, "lock-uri-prefix", os.Getenv("WOLF_SCANNER_PROPOSAL_LOCK_URI_PREFIX"), "immutable lock URI prefix ending before sha256 digest")
	flags.StringVar(&tempRoot, "temp-root", os.Getenv("WOLF_SCANNER_PROPOSAL_TEMP_ROOT"), "ephemeral proposal checkout parent")
	flags.StringVar(&gitPath, "git-path", envOr("WOLF_SCANNER_PROPOSAL_GIT_PATH", "git"), "Git executable")
	flags.StringVar(&goPath, "go-path", envOr("WOLF_SCANNER_PROPOSAL_GO_PATH", "go"), "Go executable")
	flags.StringSliceVar(&labels, "label", splitNonemptyCSV(os.Getenv("WOLF_SCANNER_PROPOSAL_LABELS")), "GitHub pull-request label (repeatable)")
	flags.BoolVar(&requireStatus, "require-status", strings.EqualFold(os.Getenv("WOLF_SCANNER_PROPOSAL_REQUIRE_STATUS"), "true"), "fail if the pending GitHub status cannot be recorded")
	return command
}

func readScannerProposalRequest(reader io.Reader) (scannerproposalworker.Request, error) {
	value, err := io.ReadAll(io.LimitReader(reader, maximumScannerProposalRequest+1))
	if err != nil {
		return scannerproposalworker.Request{}, fmt.Errorf("read scanner proposal request: %w", err)
	}
	if int64(len(value)) > maximumScannerProposalRequest {
		return scannerproposalworker.Request{}, fmt.Errorf("scanner proposal request exceeds %d bytes", maximumScannerProposalRequest)
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var request scannerproposalworker.Request
	if err := decoder.Decode(&request); err != nil {
		return scannerproposalworker.Request{}, fmt.Errorf("decode scanner proposal request: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return scannerproposalworker.Request{}, errors.New("scanner proposal request must contain exactly one JSON object")
	}
	return request, nil
}

func readScannerProposalCredential(path, environmentValue string) (string, error) {
	if path != "" && environmentValue != "" {
		return "", errors.New("configure either scanner proposal GitHub token file or environment token, not both")
	}
	value := []byte(environmentValue)
	if path != "" {
		file, err := os.Open(path) // #nosec G304 -- path is explicit administrator/orchestrator configuration.
		if err != nil {
			return "", fmt.Errorf("open scanner proposal GitHub token file: %w", err)
		}
		defer file.Close()
		value, err = io.ReadAll(io.LimitReader(file, 64<<10+1))
		if err != nil {
			return "", fmt.Errorf("read scanner proposal GitHub token file: %w", err)
		}
		if len(value) > 64<<10 {
			return "", errors.New("scanner proposal GitHub token exceeds 64 KiB")
		}
	}
	credential := strings.TrimSpace(string(value))
	if credential == "" || strings.ContainsAny(credential, "\x00\r\n") {
		return "", errors.New("scanner proposal GitHub credential is required and must be one line")
	}
	return credential, nil
}

func splitNonemptyCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
