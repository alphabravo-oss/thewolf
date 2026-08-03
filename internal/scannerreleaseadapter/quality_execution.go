package scannerreleaseadapter

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alphabravocompany/thewolf/internal/plugin"
	plugincontainer "github.com/alphabravocompany/thewolf/internal/plugin/container"
	scanrunner "github.com/alphabravocompany/thewolf/internal/scan/runner"
	"github.com/alphabravocompany/thewolf/internal/scannerquality"
	"github.com/alphabravocompany/thewolf/internal/scannerreleasebackend"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworkspace"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
	_ "github.com/alphabravocompany/thewolf/plugins" // trusted, compiled parser/plugin registry
)

const qualityEvidenceMediaType = "application/vnd.wolf.scanner-quality-evidence.v2+json"

func executeMeasuredQualityComparison(
	ctx context.Context,
	invocation scannerreleasebackend.Invocation,
	imageKey, candidateReference, candidateDigest string,
	lock *scannerlock.Lock,
) (ActionResult, error) {
	now := time.Now().UTC()
	policy, corpus, database, err := scannerquality.LoadExecutionInputs(
		ctx, invocation.Request.Workspace, now,
	)
	if err != nil {
		return ActionResult{}, fmt.Errorf("load measured quality definitions: %w", err)
	}
	goldens, goldenDigest, err := scannerquality.LoadGoldenExpectations(invocation.Request.Workspace)
	if err != nil {
		return ActionResult{}, fmt.Errorf("load reviewed quality expectations: %w", err)
	}
	execution, err := scannerreleaseworkspace.ReadContext(invocation.Request.Workspace)
	if err != nil || execution.Stable == nil {
		return ActionResult{}, errors.New("measured quality comparison requires an immutable stable release baseline")
	}
	stableImage, stableTools, err := stableQualityScope(*execution.Stable, imageKey, lock)
	if err != nil {
		return ActionResult{}, err
	}
	tools, err := lockedQualityScope(lock, imageKey)
	if err != nil || !sameStrings(tools, stableToolKeys(stableTools)) {
		return ActionResult{}, errors.New("stable and candidate quality tool scopes do not match exactly")
	}
	definition, err := manifest.LoadFile(filepath.Join(invocation.Request.Workspace, "scanners", "tools.yaml"))
	if err != nil {
		return ActionResult{}, fmt.Errorf("load quality runtime routing: %w", err)
	}
	engineDirectory := strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_ENGINE_CREDENTIAL_DIR"))
	engine, err := readEngineConfig(engineDirectory)
	if err != nil {
		return ActionResult{}, err
	}
	dockerEnvironment, err := safeEnvironment(dockerPath)
	if err != nil {
		return ActionResult{}, err
	}
	networkEvidence, err := inspectQualityNetwork(ctx, dockerEnvironment, engine)
	if err != nil {
		return ActionResult{}, fmt.Errorf("validate measured quality network: %w", err)
	}
	corpusDirectory, err := os.MkdirTemp(
		strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_SCRATCH_DIR")), "quality-corpus-",
	)
	if err != nil {
		return ActionResult{}, err
	}
	defer os.RemoveAll(corpusDirectory)
	if err := scannerquality.MaterializeExecutableCorpus(corpusDirectory, corpus); err != nil {
		return ActionResult{}, err
	}
	operationSuffix := strings.TrimPrefix(invocation.OperationID, "sha256:")[:32]
	corpusVolume, corpusCleanup, err := materializeQualityVolume(
		ctx, dockerEnvironment, invocation.OperationID, "corpus-"+operationSuffix,
		corpusDirectory, "/scan",
	)
	if err != nil {
		return ActionResult{}, err
	}
	defer corpusCleanup()
	trivyCache, trivyDatabase, err := materializeLockedTrivyDatabases(ctx, invocation)
	if err != nil {
		return ActionResult{}, fmt.Errorf("materialize measured quality vulnerability databases: %w", err)
	}
	if trivyDatabase.Identity != database.Repository+"@"+database.Digest ||
		trivyDatabase.RecordedAt != database.RecordedAt {
		return ActionResult{}, errors.New("measured quality database materialization does not match its validated lock")
	}
	qualityDatabaseRoot, err := prepareQualityDatabaseRoot(trivyCache)
	if err != nil {
		return ActionResult{}, err
	}
	databaseVolume, databaseCleanup, err := materializeQualityVolume(
		ctx, dockerEnvironment, invocation.OperationID, "database-"+operationSuffix,
		qualityDatabaseRoot, "/var/lib/wolf-db",
	)
	if err != nil {
		return ActionResult{}, err
	}
	defer databaseCleanup()

	network := engine.QualityNetwork
	if network == "" {
		network = "none"
	}
	candidateConfig, err := qualityContainerConfig(
		candidateReference, candidateDigest, tools, nil, lock, definition,
		corpusVolume, databaseVolume, network, dockerEnvironment,
	)
	if err != nil {
		return ActionResult{}, err
	}
	stableReference := stableImage.Repository + "@" + stableImage.Digest
	stableConfig, err := qualityContainerConfig(
		stableReference, stableImage.Digest, tools, stableTools, lock, definition,
		corpusVolume, databaseVolume, network, dockerEnvironment,
	)
	if err != nil {
		return ActionResult{}, err
	}
	if err := plugincontainer.EnsureAllImages(ctx, candidateConfig); err != nil {
		return ActionResult{}, fmt.Errorf("prepare candidate quality images: %w", err)
	}
	if err := plugincontainer.EnsureAllImages(ctx, stableConfig); err != nil {
		return ActionResult{}, fmt.Errorf("prepare stable quality images: %w", err)
	}

	stableEvidence := make([]scannerquality.ToolEvidence, 0, len(tools))
	candidateEvidence := make([]scannerquality.ToolEvidence, 0, len(tools))
	for _, tool := range tools {
		toolPolicy, exists := policy.Tools[tool]
		if !exists {
			return ActionResult{}, fmt.Errorf("quality policy has no exact tool scope for %q", tool)
		}
		target, err := qualityToolTarget(tool, toolPolicy, engine)
		if err != nil {
			return ActionResult{}, err
		}
		toolGoldens := qualityGoldensForTool(goldens, tool)
		baseline, err := executeQualityTool(ctx, stableConfig, tool, toolPolicy, target, toolGoldens)
		if err != nil {
			return ActionResult{}, fmt.Errorf("stable quality tool %s: %w", tool, err)
		}
		proposed, err := executeQualityTool(ctx, candidateConfig, tool, toolPolicy, target, toolGoldens)
		if err != nil {
			return ActionResult{}, fmt.Errorf("candidate quality tool %s: %w", tool, err)
		}
		stableEvidence = append(stableEvidence, baseline)
		candidateEvidence = append(candidateEvidence, proposed)
	}
	evidence := scannerquality.Evidence{
		SchemaVersion: scannerquality.EvidenceSchema,
		GoldenDigest:  goldenDigest,
		VulnerabilityDatabase: scannerquality.DBEvidence{
			Provider: database.Provider, Repository: database.Repository,
			Digest: database.Digest, RecordedAt: database.RecordedAt,
		},
		Network: networkEvidence,
		Scope:   tools, Stable: stableEvidence, Candidate: candidateEvidence,
	}
	if err := scannerquality.EvaluateEvidence(ctx, policy, database, evidence, now); err != nil {
		return ActionResult{}, fmt.Errorf("evaluate measured stable/candidate evidence: %w", err)
	}
	if err := scannerquality.EvaluateGoldenEvidence(policy, goldens, goldenDigest, evidence); err != nil {
		return ActionResult{}, fmt.Errorf("evaluate real-output expectation evidence: %w", err)
	}
	payload, err := json.Marshal(evidence)
	if err != nil {
		return ActionResult{}, err
	}
	parserFailures := evidenceParseErrors(candidateEvidence)
	findingLosses := evidenceFindingLosses(stableEvidence, candidateEvidence)
	durationRegression, resourceRegression := comparisonRegressions(stableEvidence, candidateEvidence)
	return ActionResult{
		Payload: payload, MediaType: qualityEvidenceMediaType,
		Summary: map[string]any{
			"measured": true, "image": imageKey, "image_digest": candidateDigest,
			"stable_release_id":   execution.Stable.ID,
			"stable_image_digest": stableImage.Digest,
			"tool_count":          len(tools), "parser_failures": parserFailures,
			"expected_finding_losses":       findingLosses,
			"duration_regression":           durationRegression,
			"resource_regression":           resourceRegression,
			"quality_evidence_digest":       sha256Digest(payload),
			"trivy_database_identity":       trivyDatabase.Identity,
			"trivy_java_database_identity":  trivyDatabase.JavaIdentity,
			"quality_network_mode":          networkEvidence.Mode,
			"quality_network_id":            networkEvidence.ID,
			"quality_network_policy_digest": networkEvidence.PolicyDigest,
		},
	}, nil
}

type qualityNetworkInspection struct {
	ID       string            `json:"Id"`
	Name     string            `json:"Name"`
	Driver   string            `json:"Driver"`
	Internal bool              `json:"Internal"`
	Labels   map[string]string `json:"Labels"`
}

func inspectQualityNetwork(
	ctx context.Context, environment []string, engine engineConfig,
) (scannerquality.NetworkEvidence, error) {
	if engine.QualityNetwork == "" {
		return scannerquality.NetworkEvidence{Mode: "none"}, nil
	}
	output, err := runQualityDocker(ctx, environment, "network", "inspect", engine.QualityNetwork)
	if err != nil {
		return scannerquality.NetworkEvidence{}, errors.New("controlled fixture network is unavailable")
	}
	return validateQualityNetworkInspection(
		engine.QualityNetwork, engine.QualityNetworkPolicyDigest, output,
	)
}

func validateQualityNetworkInspection(
	name, policyDigest string, raw []byte,
) (scannerquality.NetworkEvidence, error) {
	if !strings.HasPrefix(name, "wolf-quality-") || !digest(policyDigest) {
		return scannerquality.NetworkEvidence{}, errors.New("controlled fixture network request is invalid")
	}
	var networks []qualityNetworkInspection
	if err := json.Unmarshal(raw, &networks); err != nil || len(networks) != 1 {
		return scannerquality.NetworkEvidence{}, errors.New("controlled fixture network inspection is invalid")
	}
	network := networks[0]
	if network.Name != name || !network.Internal ||
		(network.Driver != "bridge" && network.Driver != "overlay") ||
		network.Labels["dev.wolf.scanner-release.quality-network"] != "true" ||
		network.Labels["dev.wolf.scanner-release.policy-digest"] != policyDigest ||
		!qualityNetworkID(network.ID) {
		return scannerquality.NetworkEvidence{}, errors.New("quality network is not an approved internal fixture network")
	}
	return scannerquality.NetworkEvidence{
		Mode: "controlled-internal", Name: network.Name,
		ID: network.ID, PolicyDigest: policyDigest,
	}, nil
}

func qualityNetworkID(value string) bool {
	if len(value) < 12 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func measuredResourceGate(
	invocation scannerreleasebackend.Invocation, imageKey, imageDigest string,
) (ActionResult, error) {
	results, err := workspaceResults(invocation)
	if err != nil {
		return ActionResult{}, err
	}
	comparison, ok := results["candidate-stable-comparison/"+imageKey]
	if !ok || !immutableStepEvidence(comparison) || comparison.Summary["measured"] != true ||
		comparison.Summary["image_digest"] != imageDigest {
		return ActionResult{}, errors.New("recorded resource gate has no exact measured comparison evidence")
	}
	payload, err := json.Marshal(struct {
		SchemaVersion    string         `json:"schema_version"`
		Image            string         `json:"image"`
		ImageDigest      string         `json:"image_digest"`
		ComparisonDigest string         `json:"comparison_digest"`
		Measurements     map[string]any `json:"measurements"`
	}{
		SchemaVersion: "wolf.scanner-recorded-resource-gate/v1",
		Image:         imageKey, ImageDigest: imageDigest, ComparisonDigest: comparison.OutputDigest,
		Measurements: comparison.Summary,
	})
	if err != nil {
		return ActionResult{}, err
	}
	summary := cloneSummary(comparison.Summary)
	summary["comparison_digest"] = comparison.OutputDigest
	return ActionResult{
		Payload: payload, MediaType: "application/vnd.wolf.scanner-recorded-resource-gate.v1+json",
		Summary: summary,
	}, nil
}

func lockedQualityScope(lock *scannerlock.Lock, imageKey string) ([]string, error) {
	var tools []string
	for name, tool := range lock.Tools {
		assigned := tool.Bucket
		if assigned == "" || tool.IntegrationTier == "upstream" {
			assigned = "default"
		}
		if assigned == imageKey {
			if tool.ParserContract.Status != "quality_policy" || tool.ParserContract.Format == "" {
				return nil, fmt.Errorf("quality tool %q has no exact parser contract", name)
			}
			tools = append(tools, name)
		}
	}
	sort.Strings(tools)
	if len(tools) == 0 {
		return nil, fmt.Errorf("scanner image %q has no locked quality tools", imageKey)
	}
	return tools, nil
}

func stableQualityScope(
	stable scannerreleaseworkspace.StableRelease, imageKey string, lock *scannerlock.Lock,
) (scannerreleaseworkspace.StableImage, []scannerreleaseworkspace.StableTool, error) {
	var image scannerreleaseworkspace.StableImage
	for _, candidate := range stable.Images {
		if candidate.Key == imageKey {
			if image.Key != "" {
				return image, nil, errors.New("stable quality image is duplicated")
			}
			image = candidate
		}
	}
	if image.Key == "" {
		return image, nil, fmt.Errorf("stable release has no scanner image %q", imageKey)
	}
	var tools []scannerreleaseworkspace.StableTool
	for _, tool := range stable.Tools {
		if tool.ImageKey != imageKey {
			continue
		}
		locked, exists := lock.Tools[tool.Key]
		if !exists || tool.ParserCompatibility != "quality_policy:"+locked.ParserContract.Format {
			return image, nil, fmt.Errorf("stable tool %q parser contract is incompatible", tool.Key)
		}
		tools = append(tools, tool)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Key < tools[j].Key })
	return image, tools, nil
}

func stableToolKeys(tools []scannerreleaseworkspace.StableTool) []string {
	result := make([]string, len(tools))
	for index := range tools {
		result[index] = tools[index].Key
	}
	return result
}

func sameStrings(left, right []string) bool {
	return strings.Join(left, "\x00") == strings.Join(right, "\x00")
}

func qualityContainerConfig(
	ownedReference, ownedDigest string,
	tools []string,
	stableTools []scannerreleaseworkspace.StableTool,
	lock *scannerlock.Lock,
	definition *manifest.Manifest,
	repoVolume, databaseVolume, network string,
	dockerEnvironment []string,
) (*plugincontainer.Config, error) {
	if !digest(ownedDigest) || !strings.Contains(ownedReference, "@"+ownedDigest) {
		return nil, errors.New("quality owned image identity is not immutable")
	}
	stableByKey := make(map[string]scannerreleaseworkspace.StableTool, len(stableTools))
	for _, tool := range stableTools {
		stableByKey[tool.Key] = tool
	}
	upstream := make(map[string]plugincontainer.ToolImageSpec)
	for _, name := range tools {
		locked := lock.Tools[name]
		if locked.IntegrationTier != "upstream" {
			continue
		}
		entry, exists := definition.Tools[name]
		if !exists {
			return nil, fmt.Errorf("quality tool %q is missing runtime routing", name)
		}
		reference := lock.UpstreamImages[name].ResolvedReference
		if stable, ok := stableByKey[name]; ok {
			reference = stable.SourceReference
		}
		if !strings.Contains(reference, "@sha256:") {
			return nil, fmt.Errorf("quality tool %q image reference is mutable", name)
		}
		upstream[name] = plugincontainer.ToolImageSpec{
			Image: reference, Entrypoint: entry.Image.Entrypoint,
		}
	}
	config := &plugincontainer.Config{
		DockerPath: dockerPath, DockerEnvironment: append([]string(nil), dockerEnvironment...),
		Image: ownedReference, UpstreamTools: upstream,
		PullPolicy: plugincontainer.PullIfNotPresent,
		Network:    network, UID: 65532, GID: 65532,
		Memory: "8g", CPUs: "4", RepoVolume: repoVolume,
		DBVolume: databaseVolume,
		ExtraEnv: map[string]string{
			"TRIVY_CACHE_DIR":           "/var/lib/wolf-db/trivy",
			"TRIVY_SKIP_DB_UPDATE":      "true",
			"TRIVY_SKIP_JAVA_DB_UPDATE": "true",
		},
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return config, nil
}

func executeQualityTool(
	ctx context.Context, base *plugincontainer.Config, tool string,
	policy scannerquality.ToolPolicy, target string, goldens []scannerquality.GoldenExpectation,
) (scannerquality.ToolEvidence, error) {
	reference := base.ImageFor(tool)
	_, digestValue, found := strings.Cut(reference, "@")
	if !found || !digest(digestValue) {
		return scannerquality.ToolEvidence{}, errors.New("tool image has no exact digest")
	}
	if policy.Strategy == "structural" {
		if len(goldens) == 0 {
			return scannerquality.ToolEvidence{}, errors.New("structural quality tool has no reviewed expectation")
		}
		canonical, err := scannerquality.CanonicalGoldenExpectations(goldens)
		if err != nil {
			return scannerquality.ToolEvidence{}, err
		}
		return scannerquality.ToolEvidence{
			Tool: tool, ExecutionMode: "structural", ImageReference: reference,
			ImageDigest: digestValue, OutputKind: "structural-manifest",
			OutputDigest: sha256Digest(canonical), OutputBytes: int64(len(canonical)),
		}, nil
	}
	config := *base
	collector := newQualityMemoryCollector(ctx, &config)
	config.OnContainerScheduled = collector.observe
	plugincontainer.SetDefault(&config)
	var outputLines []string
	var rawOutput []byte
	start := time.Now()
	result, err := scanrunner.Run(ctx, scanrunner.RunConfig{
		RepoPath: "/scan", Target: target, ScanID: "scanner-release-quality-" + tool,
		Registry: plugin.Global, Tools: []string{tool}, ToolsExplicit: true,
		Concurrency: 1, HeavyConcurrency: 1, NetworkConcurrency: 1,
		Timeout: 20 * time.Minute, ContainerCfg: &config,
		OnToolOutput: func(_ string, line string) {
			outputLines = append(outputLines, line)
		},
		OnToolRaw: func(_ string, data []byte, _ string) {
			rawOutput = append(rawOutput[:0], data...)
		},
	})
	duration := time.Since(start)
	peak, samples := collector.close()
	if err != nil {
		return scannerquality.ToolEvidence{}, err
	}
	if len(result.ToolsSkipped) != 0 || len(result.ToolsRun) != 1 || result.ToolsRun[0] != tool {
		return scannerquality.ToolEvidence{}, errors.New("quality tool did not execute exactly once")
	}
	if failure := result.ToolsFailed[tool]; failure != nil {
		return scannerquality.ToolEvidence{}, failure
	}
	for _, line := range outputLines {
		if strings.HasPrefix(line, "[SKIP] "+tool+":") {
			return scannerquality.ToolEvidence{}, fmt.Errorf("scanner skipped its mandatory quality fixture: %s", line)
		}
	}
	if samples == 0 || peak <= 0 {
		return scannerquality.ToolEvidence{}, errors.New("quality tool has no engine memory sample")
	}
	findings := make([]scannerquality.Finding, 0, len(result.Findings))
	for _, finding := range result.Findings {
		rule := finding.RuleID
		if rule == "" {
			rule = finding.Title
		}
		message := finding.Description
		if message == "" {
			message = finding.Title
		}
		findings = append(findings, scannerquality.Finding{
			Tool: tool, RuleID: rule, Severity: string(finding.Severity),
			Path: finding.FilePath, Line: finding.LineStart, Message: message,
			Fingerprint: firstString(finding.StableFingerprint, finding.Fingerprint),
		})
	}
	canonical, err := scannerquality.CanonicalFindings(findings)
	if err != nil || len(canonical) == 0 {
		return scannerquality.ToolEvidence{}, errors.New("quality normalized findings are invalid")
	}
	var normalized []scannerquality.Finding
	if err := json.Unmarshal(canonical, &normalized); err != nil {
		return scannerquality.ToolEvidence{}, err
	}
	evidence := scannerquality.ToolEvidence{
		Tool: tool, ExecutionMode: "executed", ImageReference: reference,
		ImageDigest: digestValue, OutputKind: "normalized-findings",
		OutputDigest: sha256Digest(canonical),
		DurationMS:   max(int64(1), duration.Milliseconds()), OutputBytes: int64(len(canonical)),
		PeakMemoryBytes: peak, Findings: normalized,
		ParseErrors: result.ToolParseErrors[tool],
	}
	if len(rawOutput) > 0 {
		evidence.RawOutputDigest = sha256Digest(rawOutput)
		evidence.RawOutputBytes = int64(len(rawOutput))
	}
	return evidence, nil
}

func qualityGoldensForTool(values []scannerquality.GoldenExpectation, tool string) []scannerquality.GoldenExpectation {
	var selected []scannerquality.GoldenExpectation
	for _, value := range values {
		if value.Tool == tool {
			selected = append(selected, value)
		}
	}
	return selected
}

func qualityToolTarget(
	tool string, policy scannerquality.ToolPolicy, engine engineConfig,
) (string, error) {
	if policy.Strategy == "structural" {
		return "", nil
	}
	switch tool {
	case "dockle":
		return "/scan/dockle-image.tar", nil
	case "nuclei":
		target := engine.QualityTargets[tool]
		if target == "" {
			return "", errors.New("Nuclei quality execution requires an approved internal fixture target")
		}
		return target, nil
	default:
		if target := engine.QualityTargets[tool]; target != "" {
			return "", fmt.Errorf("quality target configured for unsupported tool %q", tool)
		}
		return "", nil
	}
}

func evidenceParseErrors(values []scannerquality.ToolEvidence) int {
	total := 0
	for _, value := range values {
		total += value.ParseErrors
	}
	return total
}

func evidenceFindingLosses(stable, candidate []scannerquality.ToolEvidence) int {
	candidateByTool := make(map[string]map[string]bool, len(candidate))
	for _, value := range candidate {
		identities := make(map[string]bool, len(value.Findings))
		for _, finding := range value.Findings {
			identities[finding.RuleID+"\x00"+finding.Fingerprint] = true
		}
		candidateByTool[value.Tool] = identities
	}
	losses := 0
	for _, value := range stable {
		for _, finding := range value.Findings {
			if !candidateByTool[value.Tool][finding.RuleID+"\x00"+finding.Fingerprint] {
				losses++
			}
		}
	}
	return losses
}

type qualityMemoryCollector struct {
	ctx     context.Context
	cancel  context.CancelFunc
	config  *plugincontainer.Config
	peak    atomic.Int64
	samples atomic.Int64
	group   sync.WaitGroup
}

func newQualityMemoryCollector(ctx context.Context, config *plugincontainer.Config) *qualityMemoryCollector {
	child, cancel := context.WithCancel(ctx)
	return &qualityMemoryCollector{ctx: child, cancel: cancel, config: config}
}

func (c *qualityMemoryCollector) observe(name string) {
	c.group.Add(1)
	go func() {
		defer c.group.Done()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			commandCtx, cancel := context.WithTimeout(c.ctx, 2*time.Second)
			command := exec.CommandContext(
				commandCtx, c.config.DockerPath, "stats", "--no-stream", "--format", "{{.MemUsage}}", name,
			) // #nosec G204 -- trusted Docker path and generated container name.
			command.Env = append([]string(nil), c.config.DockerEnvironment...)
			output, err := command.Output()
			cancel()
			if err == nil {
				if value, parseErr := parseDockerMemory(output); parseErr == nil && value > 0 {
					c.samples.Add(1)
					for current := c.peak.Load(); value > current && !c.peak.CompareAndSwap(current, value); current = c.peak.Load() {
					}
				}
			}
			select {
			case <-c.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (c *qualityMemoryCollector) close() (int64, int64) {
	c.cancel()
	c.group.Wait()
	return c.peak.Load(), c.samples.Load()
}

func parseDockerMemory(value []byte) (int64, error) {
	used, _, _ := strings.Cut(strings.TrimSpace(string(value)), "/")
	used = strings.TrimSpace(used)
	units := []struct {
		name       string
		multiplier float64
	}{
		{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30},
		{"kB", 1e3}, {"MB", 1e6}, {"GB", 1e9}, {"B", 1},
	}
	for _, unit := range units {
		if strings.HasSuffix(used, unit.name) {
			number, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(used, unit.name)), 64)
			if err != nil || number < 0 {
				return 0, errors.New("invalid Docker memory value")
			}
			return int64(number * unit.multiplier), nil
		}
	}
	return 0, errors.New("unknown Docker memory unit")
}

func comparisonRegressions(stable, candidate []scannerquality.ToolEvidence) (float64, float64) {
	stableByTool := make(map[string]scannerquality.ToolEvidence, len(stable))
	for _, value := range stable {
		stableByTool[value.Tool] = value
	}
	var duration, resource float64
	for _, value := range candidate {
		baseline := stableByTool[value.Tool]
		if baseline.DurationMS > 0 {
			duration = max(duration, float64(value.DurationMS)/float64(baseline.DurationMS))
		}
		if baseline.PeakMemoryBytes > 0 {
			resource = max(resource, float64(value.PeakMemoryBytes)/float64(baseline.PeakMemoryBytes))
		}
	}
	return duration, resource
}

// prepareQualityDatabaseRoot preserves Trivy's cache layout when the
// initializer image copies data into a named volume mounted at
// /var/lib/wolf-db. The verified OCI archives materialize as db/ and java-db/;
// the runtime plugin uses /var/lib/wolf-db/trivy as its cache directory.
func prepareQualityDatabaseRoot(cache string) (string, error) {
	if !filepath.IsAbs(cache) || filepath.Base(cache) != "trivy-cache" {
		return "", errors.New("quality Trivy cache path is invalid")
	}
	info, err := os.Lstat(cache)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("quality Trivy cache must be a real directory")
	}
	root := filepath.Join(filepath.Dir(cache), "quality-database-root")
	if err := os.RemoveAll(root); err != nil {
		return "", err
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		return "", err
	}
	if err := os.Rename(cache, filepath.Join(root, "trivy")); err != nil {
		return "", fmt.Errorf("prepare quality Trivy cache layout: %w", err)
	}
	return root, nil
}

func materializeQualityVolume(
	ctx context.Context,
	environment []string,
	operationID, suffix, source, target string,
) (string, func(), error) {
	if !digest(operationID) || !filepath.IsAbs(source) || !strings.HasPrefix(target, "/") {
		return "", func() {}, errors.New("quality volume materialization request is invalid")
	}
	component := strings.TrimPrefix(operationID, "sha256:")[:32]
	volume := "wolf-quality-" + suffix
	image := "wolf-quality-materializer:" + component + "-" + sha256Digest([]byte(target))[7:19]
	initializer := "wolf-quality-init-" + component + "-" + sha256Digest([]byte(target))[7:19]
	for _, resource := range []struct{ kind, name string }{
		{"container", initializer}, {"volume", volume}, {"image", image},
	} {
		if err := reclaimQualityResource(
			ctx, environment, resource.kind, resource.name, operationID,
		); err != nil {
			return "", func() {}, err
		}
	}
	archive, err := qualityBuildContext(source, target, operationID)
	if err != nil {
		return "", func() {}, err
	}
	if err := runQualityDockerInput(ctx, environment, archive,
		"build", "--network", "none", "--pull=false", "--tag", image, "-",
	); err != nil {
		return "", func() {}, fmt.Errorf("build trusted quality volume initializer: %w", err)
	}
	if _, err := runQualityDocker(ctx, environment,
		"volume", "create", "--label", "dev.wolf.operation-id="+operationID, volume,
	); err != nil {
		return "", func() {}, err
	}
	if _, err := runQualityDocker(ctx, environment,
		"create", "--label", "dev.wolf.operation-id="+operationID,
		"--name", initializer, "--mount", "type=volume,source="+volume+",target="+target, image,
	); err != nil {
		return "", func() {}, fmt.Errorf("initialize trusted quality volume: %w", err)
	}
	if _, err := runQualityDocker(ctx, environment, "container", "rm", initializer); err != nil {
		return "", func() {}, err
	}
	cleanup := func() {
		_, _ = runQualityDocker(context.WithoutCancel(ctx), environment, "volume", "rm", volume)
		_, _ = runQualityDocker(context.WithoutCancel(ctx), environment, "image", "rm", image)
	}
	return volume, cleanup, nil
}

func reclaimQualityResource(
	ctx context.Context, environment []string, kind, name, operationID string,
) error {
	var format string
	switch kind {
	case "container":
		format = `{{ index .Config.Labels "dev.wolf.operation-id" }}`
	case "volume":
		format = `{{ index .Labels "dev.wolf.operation-id" }}`
	case "image":
		format = `{{ index .Config.Labels "dev.wolf.operation-id" }}`
	default:
		return errors.New("quality resource kind is invalid")
	}
	command := exec.CommandContext(ctx, dockerPath, kind, "inspect", "--format", format, name) // #nosec G204 -- compiled resource kind and operation-scoped name.
	command.Env = append([]string(nil), environment...)
	var stdout, stderr boundedBuffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if stdout.exceeded || stderr.exceeded {
		return errors.New("quality resource inspection exceeded its bound")
	}
	if err != nil {
		message := strings.ToLower(stderr.value.String())
		if strings.Contains(message, "no such") || strings.Contains(message, "not found") {
			return nil
		}
		return errors.New("quality resource inspection failed")
	}
	if strings.TrimSpace(stdout.value.String()) != operationID {
		return fmt.Errorf("quality %s %q is not owned by this operation", kind, name)
	}
	removeArgs := []string{kind, "rm", name}
	if kind == "container" {
		removeArgs = []string{"container", "rm", "--force", name}
	}
	if _, err := runQualityDocker(ctx, environment, removeArgs...); err != nil {
		return fmt.Errorf("reclaim quality %s: %w", kind, err)
	}
	return nil
}

func qualityBuildContext(source, target, operationID string) ([]byte, error) {
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	dockerfile := []byte("FROM scratch\nLABEL dev.wolf.operation-id=\"" + operationID + "\"\nCOPY corpus/ " + target + "/\nVOLUME [\"" + target + "\"]\nCMD [\"/does-not-run\"]\n")
	if err := writeQualityTarFile(writer, "Dockerfile", dockerfile, 0o600); err != nil {
		return nil, err
	}
	var paths []string
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("quality corpus contains a symlink")
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	for _, path := range paths {
		value, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		relative, _ := filepath.Rel(source, path)
		if err := writeQualityTarFile(writer, "corpus/"+filepath.ToSlash(relative), value, 0o600); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func writeQualityTarFile(writer *tar.Writer, name string, value []byte, mode int64) error {
	header := &tar.Header{
		Name: name, Mode: mode, Size: int64(len(value)), ModTime: time.Unix(0, 0).UTC(),
		Uid: 65532, Gid: 65532, Typeflag: tar.TypeReg,
	}
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	_, err := writer.Write(value)
	return err
}

func runQualityDockerInput(
	ctx context.Context, environment []string, input []byte, args ...string,
) error {
	command := exec.CommandContext(ctx, dockerPath, args...) // #nosec G204 -- compiled Docker command.
	command.Env = append([]string(nil), environment...)
	command.Stdin = bytes.NewReader(input)
	var stdout, stderr boundedBuffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil || stdout.exceeded || stderr.exceeded {
		return errors.New("trusted Docker materialization command failed")
	}
	return nil
}

func runQualityDocker(ctx context.Context, environment []string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, dockerPath, args...) // #nosec G204 -- compiled Docker command.
	command.Env = append([]string(nil), environment...)
	var stdout, stderr boundedBuffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil || stdout.exceeded || stderr.exceeded {
		return nil, errors.New("trusted Docker quality command failed")
	}
	return io.ReadAll(&stdout.value)
}
