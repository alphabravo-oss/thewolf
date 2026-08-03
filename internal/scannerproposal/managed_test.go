package scannerproposal

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannergit"
	"github.com/alphabravocompany/thewolf/internal/scannerproposalworker"
)

type generatorFunc func(context.Context, scannerproposalworker.Request) (GeneratedDefinition, error)

func (function generatorFunc) Generate(
	ctx context.Context,
	request scannerproposalworker.Request,
) (GeneratedDefinition, error) {
	return function(ctx, request)
}

type recordingProvider struct {
	proposal scannergit.Proposal
	status   scannergit.CommitStatus
	commit   string
	creates  int
}

func (p *recordingProvider) CreateProposal(
	_ context.Context,
	proposal scannergit.Proposal,
) (scannergit.ProposalResult, error) {
	p.creates++
	p.proposal = proposal
	return scannergit.ProposalResult{
		Branch: proposal.Branch, Commit: p.commit,
		PullRequest: 42, URL: "https://github.example/acme/definitions/pull/42",
	}, nil
}

func (p *recordingProvider) SetCommitStatus(
	_ context.Context,
	commit string,
	status scannergit.CommitStatus,
) error {
	p.commit = commit
	p.status = status
	return nil
}

func TestManagedProposalBindsGeneratedTreeToCandidateAndPolicy(t *testing.T) {
	t.Parallel()
	const commit = "2222222222222222222222222222222222222222"
	provider := &recordingProvider{commit: commit}
	generator := generatorFunc(func(
		_ context.Context,
		request scannerproposalworker.Request,
	) (GeneratedDefinition, error) {
		if request.CandidateID != "candidate-1" {
			t.Fatalf("generator request = %#v", request)
		}
		return GeneratedDefinition{
			Files: []scannergit.File{{
				Path: "scanners/scanner-lock.yaml", Content: []byte("lock"), Mode: "100644",
			}},
			BaseLockDigest:   digest("b"),
			LockDigest:       digest("a"),
			LockURI:          "oci://registry.example/locks@" + digest("a"),
			DefinitionDigest: digest("c"),
			DiffDigest: generatedFilesDigest([]scannergit.File{{
				Path: "scanners/scanner-lock.yaml", Content: []byte("lock"), Mode: "100644",
			}}),
			RiskSummary: json.RawMessage(`{"highest_risk":"low"}`),
			Changes: []Change{{
				Kind: ChangeTool, Name: "semgrep", From: "1.0.0", To: "1.0.1",
				Digest: digest("d"), Risk: "low",
			}},
			Gates: []GatePlan{{Name: "lock", Status: "pending"}},
			Validation: []Validation{
				{Name: "manifest", Status: "passed", Command: "validate manifest"},
				{Name: "docs", Status: "passed", Command: "validate docs"},
				{Name: "parity", Status: "passed", Command: "validate parity"},
				{Name: "lock", Status: "passed", Command: "validate lock"},
			},
		}, nil
	})
	managed := Managed{
		Generator: generator, Git: provider, BaseBranch: "definitions",
		BranchPrefix: "automation/scanners", Labels: []string{"scanner-release"},
	}
	result, err := managed.Propose(context.Background(), scannerproposalworker.Request{
		CandidateID:      "candidate-1",
		DefinitionCommit: "1111111111111111111111111111111111111111",
		Selection:        json.RawMessage(`{"mode":"complete"}`),
		PolicyID:         "policy-1", PolicyRevision: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProposedCommit != commit ||
		provider.proposal.ExpectedBaseCommit != "1111111111111111111111111111111111111111" ||
		provider.proposal.Branch != "automation/scanners/candidate-1" ||
		provider.creates != 1 ||
		provider.status.State != "pending" ||
		provider.status.Context != "wolf/scanner-release" {
		t.Fatalf("managed result=%#v proposal=%#v status=%#v", result, provider.proposal, provider.status)
	}
}

func TestManagedProposalValidatesBeforeAnyProviderWrite(t *testing.T) {
	t.Parallel()
	provider := &recordingProvider{}
	managed := Managed{
		Git: provider,
		Generator: generatorFunc(func(
			context.Context,
			scannerproposalworker.Request,
		) (GeneratedDefinition, error) {
			return GeneratedDefinition{
				BaseLockDigest: digest("a"),
				LockDigest:     digest("a"),
			}, nil
		}),
	}
	if _, err := managed.Propose(context.Background(), scannerproposalworker.Request{
		CandidateID:      "candidate-atomic",
		DefinitionCommit: strings.Repeat("1", 40),
	}); err == nil {
		t.Fatalf("invalid generated definition error = %v", err)
	}
	if provider.creates != 0 || provider.proposal.Branch != "" {
		t.Fatalf("invalid proposal reached provider: %#v", provider)
	}
}

func TestManagedProposalGeneratorFailureIsAtomic(t *testing.T) {
	t.Parallel()
	provider := &recordingProvider{}
	managed := Managed{
		Git: provider,
		Generator: generatorFunc(func(
			context.Context,
			scannerproposalworker.Request,
		) (GeneratedDefinition, error) {
			return GeneratedDefinition{}, errors.New("manifest validation failed")
		}),
	}
	if _, err := managed.Propose(context.Background(), scannerproposalworker.Request{
		CandidateID:      "candidate-invalid",
		DefinitionCommit: strings.Repeat("2", 40),
	}); err == nil || !strings.Contains(err.Error(), "manifest validation failed") {
		t.Fatalf("generator error = %v", err)
	}
	if provider.creates != 0 {
		t.Fatalf("generator failure wrote %d proposals", provider.creates)
	}
}

func TestManagedPullRequestBodyIsDeterministicAndComplete(t *testing.T) {
	t.Parallel()
	generated := completeGeneratedDefinitionFixture()
	request := scannerproposalworker.Request{
		CandidateID:      "candidate-body",
		DefinitionCommit: strings.Repeat("1", 40),
		PolicyID:         "enterprise-policy",
		PolicyRevision:   12,
	}
	first := managedPullRequestBody(request, generated)
	second := managedPullRequestBody(request, generated)
	if first != second {
		t.Fatal("managed pull request body is not deterministic")
	}
	for _, required := range []string{
		"Exact immutable identities",
		"Risk assessment",
		"Tool, base-image, and toolchain changes",
		"Gate plan",
		"Proposal-generation validation",
		"Evidence index",
		"Automation safety",
		generated.BaseLockDigest,
		generated.LockDigest,
		generated.DefinitionDigest,
		generated.DiffDigest,
		"https://evidence.example/source/semgrep",
		"durable build evidence pending",
	} {
		if !strings.Contains(first, required) {
			t.Fatalf("pull request body is missing %q:\n%s", required, first)
		}
	}
}

func TestManagedProposalRejectsUnsafeCandidateBranchComponent(t *testing.T) {
	t.Parallel()
	managed := Managed{Generator: generatorFunc(func(
		context.Context,
		scannerproposalworker.Request,
	) (GeneratedDefinition, error) {
		t.Fatal("generator called for unsafe candidate ID")
		return GeneratedDefinition{}, nil
	}), Git: &recordingProvider{}}
	if _, err := managed.Propose(context.Background(), scannerproposalworker.Request{
		CandidateID: "../escape",
	}); err == nil {
		t.Fatal("unsafe candidate ID was accepted")
	}
}

func completeGeneratedDefinitionFixture() GeneratedDefinition {
	files := []scannergit.File{{
		Path: "scanners/scanner-lock.yaml", Content: []byte("lock"), Mode: "100644",
	}}
	return GeneratedDefinition{
		Files: files, BaseLockDigest: digest("b"), LockDigest: digest("a"),
		LockURI:          "git:scanners/scanner-lock.yaml@" + digest("a"),
		DefinitionDigest: digest("c"), DiffDigest: generatedFilesDigest(files),
		RiskSummary: json.RawMessage(`{"highest_risk":"low","reasons":["patch update"]}`),
		Changes: []Change{{
			Kind: ChangeTool, Name: "semgrep", From: "1.0.0", To: "1.0.1",
			Digest: digest("d"), Risk: "low",
			EvidenceURL: "https://evidence.example/source/semgrep",
		}},
		Gates: []GatePlan{
			{Name: "lock", Status: "pending"},
			{Name: "signature", Status: "pending"},
		},
		Validation: []Validation{
			{Name: "manifest", Status: "passed", Command: "go run ./cmd/scannertools validate"},
			{Name: "docs", Status: "passed", Command: "go run ./cmd/scannertools docs --check"},
			{Name: "parity", Status: "passed", Command: "internal parity validation"},
			{Name: "lock", Status: "passed", Command: "go run ./cmd/scannertools lock --check --require-resolved"},
		},
	}
}

func digest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
