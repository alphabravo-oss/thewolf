// Package scannerproposal composes deterministic definition generation with a
// conflict-safe Git provider. It is the managed proposal implementation used
// when operators choose Git-backed release definitions; the durable worker
// contract remains usable with a patch-only generator for offline installs.
package scannerproposal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/scannergit"
	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerproposalworker"
)

var (
	candidateBranchComponent = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	proposalDigestPattern    = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	proposalNamePattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}$`)
)

const (
	ChangeTool      = "tool"
	ChangeBaseImage = "base_image"
	ChangeToolchain = "toolchain"
)

// Change describes one exact release-lock transition. CheckoutGenerator
// derives these values from the base and proposed canonical locks instead of
// trusting a selection payload to describe the resulting definition.
type Change struct {
	Kind        string
	Name        string
	From        string
	To          string
	Digest      string
	Risk        string
	EvidenceURL string
}

// GatePlan describes evidence that a later durable build must attach. Proposal
// generation checks are kept separate in Validation so a PR never implies that
// an image gate has already passed.
type GatePlan struct {
	Name        string
	Status      string
	EvidenceURL string
}

type EvidenceLink struct {
	Label string
	URL   string
}

type Validation struct {
	Name    string
	Status  string
	Command string
}

// GeneratedDefinition is the fully validated output of an ephemeral
// definition checkout. Files are complete contents, not patches, so the Git
// provider can construct one deterministic tree and detect a no-op.
type GeneratedDefinition struct {
	Files              []scannergit.File
	BaseLockDigest     string
	LockDigest         string
	LockURI            string
	DefinitionDigest   string
	DiffDigest         string
	RiskSummary        json.RawMessage
	Changes            []Change
	Gates              []GatePlan
	Evidence           []EvidenceLink
	Validation         []Validation
	Images             []scannerpipeline.Image
	ExpectedBranchHead string
}

type Generator interface {
	Generate(context.Context, scannerproposalworker.Request) (GeneratedDefinition, error)
}

type Managed struct {
	Generator    Generator
	Git          scannergit.Provider
	BaseBranch   string
	BranchPrefix string
	Labels       []string
	// RequireStatus makes a status API outage fail proposal completion.
	// Keep false unless the provider and generator can safely replay an
	// already-created branch/PR using ExpectedBranchHead.
	RequireStatus bool
}

var _ scannerproposalworker.Proposer = Managed{}

func (m Managed) Propose(
	ctx context.Context,
	request scannerproposalworker.Request,
) (scannerproposalworker.Result, error) {
	if m.Generator == nil || m.Git == nil {
		return scannerproposalworker.Result{}, errors.New("scanner proposal generator and Git provider are required")
	}
	if !candidateBranchComponent.MatchString(request.CandidateID) {
		return scannerproposalworker.Result{}, errors.New("scanner candidate ID is not safe for a proposal branch")
	}
	if !fullGitCommitPattern.MatchString(request.DefinitionCommit) {
		return scannerproposalworker.Result{}, errors.New("scanner definition commit must be a full lowercase Git SHA-1")
	}
	if m.BaseBranch == "" {
		m.BaseBranch = "main"
	}
	if m.BranchPrefix == "" {
		m.BranchPrefix = "wolf/scanner-release"
	}
	m.BranchPrefix = strings.TrimSuffix(m.BranchPrefix, "/")
	generated, err := m.Generator.Generate(ctx, request)
	if err != nil {
		return scannerproposalworker.Result{}, fmt.Errorf("generate scanner definition: %w", err)
	}
	generated, err = normalizeGeneratedDefinition(generated)
	if err != nil {
		return scannerproposalworker.Result{}, fmt.Errorf("validate generated scanner definition: %w", err)
	}
	branch := m.BranchPrefix + "/" + request.CandidateID
	gitProposal := scannergit.Proposal{
		BaseBranch:         m.BaseBranch,
		ExpectedBaseCommit: request.DefinitionCommit,
		Branch:             branch,
		ExpectedBranchHead: generated.ExpectedBranchHead,
		CommitMessage:      "chore(scanners): propose candidate " + request.CandidateID,
		Title:              "Scanner release candidate " + request.CandidateID,
		Body:               managedPullRequestBody(request, generated),
		Files:              generated.Files,
		Labels:             append([]string(nil), m.Labels...),
	}
	if err := scannergit.ValidateProposal(gitProposal); err != nil {
		return scannerproposalworker.Result{}, fmt.Errorf("validate Git proposal: %w", err)
	}
	proposal, err := m.Git.CreateProposal(ctx, gitProposal)
	if err != nil {
		return scannerproposalworker.Result{}, fmt.Errorf("publish scanner definition proposal: %w", err)
	}
	result := scannerproposalworker.Result{
		ProposedCommit: proposal.Commit,
		ProposalURL:    proposal.URL,
		LockDigest:     generated.LockDigest,
		LockURI:        generated.LockURI,
		RiskSummary:    append(json.RawMessage(nil), generated.RiskSummary...),
		Images:         append([]scannerpipeline.Image(nil), generated.Images...),
	}
	if err := m.Git.SetCommitStatus(ctx, proposal.Commit, scannergit.CommitStatus{
		State: "pending", Context: "wolf/scanner-release",
		Description: "Awaiting durable scanner release evidence",
	}); err != nil && m.RequireStatus {
		return scannerproposalworker.Result{}, fmt.Errorf("record scanner proposal status: %w", err)
	}
	return result, nil
}

func managedPullRequestBody(
	request scannerproposalworker.Request,
	generated GeneratedDefinition,
) string {
	var body strings.Builder
	body.WriteString("## Wolf scanner release candidate\n\n")
	fmt.Fprintf(&body, "- Candidate: `%s`\n", markdownCode(request.CandidateID))
	fmt.Fprintf(&body, "- Base definition commit: `%s`\n", markdownCode(request.DefinitionCommit))
	fmt.Fprintf(&body, "- Policy snapshot: `%s` revision `%d`\n", markdownCode(request.PolicyID), request.PolicyRevision)
	fmt.Fprintf(&body, "- Lock artifact: `%s`\n\n", markdownCode(generated.LockURI))

	body.WriteString("### Exact immutable identities\n\n")
	body.WriteString("| Identity | Digest |\n| --- | --- |\n")
	writeDigestRow(&body, "Base release lock", generated.BaseLockDigest)
	writeDigestRow(&body, "Proposed release lock", generated.LockDigest)
	writeDigestRow(&body, "Proposed definition", generated.DefinitionDigest)
	writeDigestRow(&body, "Generated file set", generated.DiffDigest)

	body.WriteString("\n### Risk assessment\n\n")
	body.WriteString("The canonical policy input used by this proposal is:\n\n")
	for _, line := range strings.Split(string(generated.RiskSummary), "\n") {
		body.WriteString("    ")
		body.WriteString(line)
		body.WriteByte('\n')
	}

	body.WriteString("\n### Tool, base-image, and toolchain changes\n\n")
	body.WriteString("| Type | Component | From | To | Exact digest | Risk | Source evidence |\n")
	body.WriteString("| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, change := range generated.Changes {
		fmt.Fprintf(
			&body, "| %s | %s | %s | %s | %s | %s | %s |\n",
			markdownCell(change.Kind), markdownCell(change.Name),
			markdownCell(change.From), markdownCell(change.To),
			markdownCell(valueOrPending(change.Digest, "not supplied by upstream")),
			markdownCell(valueOrPending(change.Risk, "see canonical risk assessment")),
			evidenceCell(change.EvidenceURL, "source evidence will be attached"),
		)
	}

	body.WriteString("\n### Gate plan\n\n")
	body.WriteString("| Required gate | Initial status | Evidence |\n")
	body.WriteString("| --- | --- | --- |\n")
	for _, gate := range generated.Gates {
		fmt.Fprintf(
			&body, "| %s | %s | %s |\n",
			markdownCell(gate.Name), markdownCell(gate.Status),
			evidenceCell(gate.EvidenceURL, "durable build evidence pending"),
		)
	}

	body.WriteString("\n### Proposal-generation validation\n\n")
	body.WriteString("| Check | Status | Reproduction command |\n")
	body.WriteString("| --- | --- | --- |\n")
	for _, check := range generated.Validation {
		fmt.Fprintf(
			&body, "| %s | %s | `%s` |\n",
			markdownCell(check.Name), markdownCell(check.Status),
			markdownCode(check.Command),
		)
	}

	body.WriteString("\n### Evidence index\n\n")
	if len(generated.Evidence) == 0 {
		body.WriteString("- _No durable image evidence exists yet; gate workers will attach content-addressed links here._\n")
	} else {
		for _, evidence := range generated.Evidence {
			fmt.Fprintf(&body, "- %s: [%s](%s)\n",
				markdownCell(evidence.Label), markdownCell(evidence.Label), evidence.URL)
		}
	}

	body.WriteString("\n### Automation safety\n\n")
	body.WriteString(
		"This proposal was regenerated and validated in an ephemeral checkout at the exact base commit. " +
			"Do not force-push this branch. Exact-head conflict checks preserve human edits and stop automation for review.\n",
	)
	return body.String()
}

func normalizeGeneratedDefinition(generated GeneratedDefinition) (GeneratedDefinition, error) {
	for _, digest := range []struct {
		name  string
		value string
	}{
		{"base lock", generated.BaseLockDigest},
		{"proposed lock", generated.LockDigest},
		{"definition", generated.DefinitionDigest},
		{"generated file set", generated.DiffDigest},
	} {
		if !proposalDigestPattern.MatchString(digest.value) {
			return GeneratedDefinition{}, fmt.Errorf("%s digest is invalid", digest.name)
		}
	}
	if generated.BaseLockDigest == generated.LockDigest {
		return GeneratedDefinition{}, errors.New("proposal does not change the release lock")
	}
	if !safeLockURI(generated.LockURI) ||
		!strings.Contains(generated.LockURI, generated.LockDigest) {
		return GeneratedDefinition{}, errors.New("lock URI is not bound to the proposed lock digest")
	}
	risk, err := canonicalJSONObject(generated.RiskSummary)
	if err != nil {
		return GeneratedDefinition{}, fmt.Errorf("risk summary: %w", err)
	}
	generated.RiskSummary = risk
	if len(generated.Files) == 0 {
		return GeneratedDefinition{}, errors.New("proposal file set is empty")
	}
	sort.Slice(generated.Files, func(i, j int) bool {
		return generated.Files[i].Path < generated.Files[j].Path
	})
	if got := generatedFilesDigest(generated.Files); got != generated.DiffDigest {
		return GeneratedDefinition{}, fmt.Errorf(
			"generated file-set digest is %s, calculated %s",
			generated.DiffDigest, got,
		)
	}
	if len(generated.Changes) == 0 {
		return GeneratedDefinition{}, errors.New("proposal has no lock-derived component changes")
	}
	sort.Slice(generated.Changes, func(i, j int) bool {
		left, right := generated.Changes[i], generated.Changes[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.Name < right.Name
	})
	seenChanges := make(map[string]bool, len(generated.Changes))
	for index, change := range generated.Changes {
		changeKey := change.Kind + ":" + change.Name
		if (change.Kind != ChangeTool && change.Kind != ChangeBaseImage &&
			change.Kind != ChangeToolchain) ||
			!proposalNamePattern.MatchString(change.Name) ||
			!boundedText(change.From, 1024) || !boundedText(change.To, 1024) ||
			!boundedText(change.Risk, 128) || seenChanges[changeKey] {
			return GeneratedDefinition{}, fmt.Errorf("component change %d is invalid", index)
		}
		seenChanges[changeKey] = true
		if change.Digest != "" && !proposalDigestPattern.MatchString(change.Digest) {
			return GeneratedDefinition{}, fmt.Errorf(
				"component change %s has an invalid digest", change.Name,
			)
		}
		if !safeEvidenceURL(change.EvidenceURL) {
			return GeneratedDefinition{}, fmt.Errorf(
				"component change %s has an unsafe evidence URL", change.Name,
			)
		}
	}
	if len(generated.Gates) == 0 {
		return GeneratedDefinition{}, errors.New("proposal gate plan is empty")
	}
	sort.Slice(generated.Gates, func(i, j int) bool {
		return generated.Gates[i].Name < generated.Gates[j].Name
	})
	seenGates := make(map[string]bool, len(generated.Gates))
	for _, gate := range generated.Gates {
		if !proposalNamePattern.MatchString(gate.Name) || gate.Status != "pending" ||
			seenGates[gate.Name] || !safeEvidenceURL(gate.EvidenceURL) {
			return GeneratedDefinition{}, fmt.Errorf("proposal gate %q is invalid", gate.Name)
		}
		seenGates[gate.Name] = true
	}
	sort.Slice(generated.Evidence, func(i, j int) bool {
		return generated.Evidence[i].Label < generated.Evidence[j].Label
	})
	seenEvidence := make(map[string]bool, len(generated.Evidence))
	for index, evidence := range generated.Evidence {
		if !boundedText(evidence.Label, 128) || strings.TrimSpace(evidence.Label) == "" ||
			!safeEvidenceURL(evidence.URL) || evidence.URL == "" ||
			seenEvidence[evidence.Label] {
			return GeneratedDefinition{}, fmt.Errorf("proposal evidence link %d is invalid", index)
		}
		seenEvidence[evidence.Label] = true
	}
	requiredChecks := map[string]bool{
		"manifest": false, "docs": false, "parity": false, "lock": false,
	}
	sort.Slice(generated.Validation, func(i, j int) bool {
		return generated.Validation[i].Name < generated.Validation[j].Name
	})
	for _, validation := range generated.Validation {
		if _, ok := requiredChecks[validation.Name]; !ok ||
			requiredChecks[validation.Name] || validation.Status != "passed" ||
			strings.TrimSpace(validation.Command) == "" ||
			!boundedText(validation.Command, 1024) {
			return GeneratedDefinition{}, fmt.Errorf(
				"proposal validation result %q is invalid", validation.Name,
			)
		}
		requiredChecks[validation.Name] = true
	}
	for name, passed := range requiredChecks {
		if !passed {
			return GeneratedDefinition{}, fmt.Errorf(
				"proposal validation result %q is missing", name,
			)
		}
	}
	return generated, nil
}

func canonicalJSONObject(value json.RawMessage) (json.RawMessage, error) {
	if len(value) == 0 || len(value) > 32<<10 {
		return nil, errors.New("must be a bounded JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, errors.New("must be a JSON object")
	}
	if object == nil {
		return nil, errors.New("must be a JSON object")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("must contain exactly one JSON object")
	}
	encoded, err := json.MarshalIndent(object, "", "  ")
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func generatedFilesDigest(files []scannergit.File) string {
	hash := sha256.New()
	for _, file := range files {
		fmt.Fprintf(hash, "%d:%s\n%d:%s\n%t\n%d\n",
			len(file.Path), file.Path, len(file.Mode), file.Mode, file.Delete, len(file.Content))
		_, _ = hash.Write(file.Content)
		_, _ = hash.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func boundedText(value string, maximum int) bool {
	return len(value) <= maximum && !strings.ContainsAny(value, "\x00\r\n")
}

func safeEvidenceURL(value string) bool {
	if value == "" {
		return true
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" &&
		parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func safeLockURI(value string) bool {
	if value == "" || len(value) > 2048 || strings.ContainsAny(value, "\x00\r\n") {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme != "" && parsed.User == nil
}

func writeDigestRow(body *strings.Builder, name, digest string) {
	fmt.Fprintf(body, "| %s | `%s` |\n", markdownCell(name), markdownCode(digest))
}

func valueOrPending(value, pending string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return "pending: " + pending
}

func evidenceCell(value, pending string) string {
	if value == "" {
		return "_" + markdownCell(pending) + "_"
	}
	return "[evidence](" + value + ")"
}

func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func markdownCode(value string) string {
	value = strings.ReplaceAll(value, "`", "'")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.ReplaceAll(value, "\n", " ")
}
