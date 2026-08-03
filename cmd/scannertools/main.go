package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerquality"
	"github.com/alphabravocompany/thewolf/internal/scannertools/docs"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
	"github.com/alphabravocompany/thewolf/internal/scannertools/ospackages"
	"github.com/alphabravocompany/thewolf/internal/scannertools/validate"
	_ "github.com/alphabravocompany/thewolf/plugins"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	root, err := manifest.FindRepoRoot("")
	if err != nil {
		return err
	}
	switch args[0] {
	case "validate":
		result, err := validate.Run(root)
		if err != nil {
			return err
		}
		fmt.Printf("scanner metadata OK: %d tools (%d default, %d bucket, %d upstream), %d version pins\n",
			result.ToolCount, result.DefaultCount, result.BucketCount, result.UpstreamCount, result.VersionVarCount)
		return nil
	case "quality":
		fs := flag.NewFlagSet("quality", flag.ContinueOnError)
		evidence := fs.String("evidence", "", "optional recorded stable/candidate evidence JSON")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return fmt.Errorf("quality does not accept positional arguments")
		}
		var coverage scannerquality.Coverage
		if strings.TrimSpace(*evidence) == "" {
			coverage, err = scannerquality.ValidateRepository(
				context.Background(), root, time.Now().UTC(),
			)
		} else {
			coverage, err = scannerquality.ValidateEvidenceFile(
				context.Background(), root, *evidence, time.Now().UTC(),
			)
		}
		if err != nil {
			return err
		}
		fmt.Printf(
			"scanner quality OK: %d tools, %d families, %d/%d hostile/valid parser adapters, "+
				"%d scanner and %d fixer platform tuples\n",
			coverage.Tools, coverage.Families, coverage.HostileTestedAdapters,
			coverage.ValidTestedAdapters,
			coverage.ScannerPlatformTuples, coverage.FixerPlatformTuples,
		)
		return nil
	case "docs":
		fs := flag.NewFlagSet("docs", flag.ContinueOnError)
		check := fs.Bool("check", false, "fail if scanners/TOOLS.md is stale")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return renderDocs(root, *check)
	case "upstream-images":
		fs := flag.NewFlagSet("upstream-images", flag.ContinueOnError)
		platforms := fs.String("platforms", "linux/amd64,linux/arm64", "comma-separated platforms to require")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return checkUpstreamImages(root, splitCSV(*platforms))
	case "bump":
		fs := flag.NewFlagSet("bump", flag.ContinueOnError)
		tool := fs.String("tool", "", "scanner tool name")
		version := fs.String("version", "", "new pinned version")
		sourceDateEpoch := fs.Int64(
			"source-date-epoch", sourceDateEpochDefault(),
			"deterministic changelog timestamp",
		)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return fmt.Errorf("bump does not accept positional arguments")
		}
		if *sourceDateEpoch < 0 {
			return fmt.Errorf("--source-date-epoch must not be negative")
		}
		return bumpToolAt(
			root, *tool, *version, time.Unix(*sourceDateEpoch, 0).UTC(), os.Stdout,
		)
	case "lock":
		return runLock(context.Background(), root, args[1:])
	case "os-packages":
		return runOSPackages(context.Background(), root, args[1:])
	case "vulnerability-dbs":
		return runVulnerabilityDBs(context.Background(), root, args[1:])
	case "propose":
		return runPropose(context.Background(), root, args[1:])
	case "reproducibility":
		return runReproducibility(root, args[1:])
	default:
		return usage()
	}
}

func usage() error {
	return fmt.Errorf("usage: scannertools validate | quality [--evidence FILE] | docs [--check] | upstream-images [--platforms linux/amd64,linux/arm64] | bump --tool NAME --version VERSION [--source-date-epoch UNIX] | lock [--check] [--refresh-images] [--require-resolved] [--json] | os-packages (--check | --refresh --snapshot YYYYMMDDTHHMMSSZ) [--json] | vulnerability-dbs (--check | --refresh [--recorded-at RFC3339]) [--json] | propose --update TOOL=VERSION --output proposal.tar [--json] | reproducibility --managed FILE --customer FILE [--output FILE]")
}

func runOSPackages(ctx context.Context, root string, args []string) error {
	fs := flag.NewFlagSet("os-packages", flag.ContinueOnError)
	check := fs.Bool("check", false, "validate the committed lock and generated files without network access")
	refresh := fs.Bool("refresh", false, "explicitly resolve package versions from configured remote repositories")
	snapshot := fs.String("snapshot", "", "Debian snapshot timestamp in YYYYMMDDTHHMMSSZ form")
	jsonOutput := fs.Bool("json", false, "print a machine-readable operation result")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("os-packages does not accept positional arguments")
	}
	if *check == *refresh {
		return fmt.Errorf("os-packages requires exactly one of --check or --refresh")
	}
	if *check {
		if *snapshot != "" {
			return fmt.Errorf("--snapshot is only valid with --refresh")
		}
		if err := ospackages.Check(root); err != nil {
			return err
		}
		lock, err := ospackages.LoadLock(filepath.Join(root, ospackages.DefaultLockPath))
		if err != nil {
			return err
		}
		return printOSPackageResult(*jsonOutput, "current", lock, root)
	}
	if *snapshot == "" {
		return fmt.Errorf("--refresh requires --snapshot YYYYMMDDTHHMMSSZ")
	}
	policy, policyData, err := ospackages.LoadPolicy(filepath.Join(root, ospackages.DefaultPolicyPath))
	if err != nil {
		return err
	}
	lock, err := ospackages.Refresh(ctx, policy, policyData, ospackages.RefreshOptions{
		Snapshot: *snapshot,
	})
	if err != nil {
		return err
	}
	bootstrapPackage, err := ospackages.FetchBootstrapCACertificates(ctx, lock, nil)
	if err != nil {
		return err
	}
	lockData, err := lock.MarshalYAML()
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(root, ospackages.DefaultLockPath), lockData, 0o644); err != nil {
		return err
	}
	generated, err := ospackages.RenderFiles(*lock)
	if err != nil {
		return err
	}
	for relative, data := range generated {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := writeFileAtomic(path, data, 0o644); err != nil {
			return err
		}
	}
	bootstrapPath := filepath.Join(root, filepath.FromSlash(ospackages.BootstrapPackagePath))
	if err := os.MkdirAll(filepath.Dir(bootstrapPath), 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(bootstrapPath, bootstrapPackage, 0o644); err != nil {
		return err
	}
	if err := removeUnexpectedOSPackageFiles(root, generated); err != nil {
		return err
	}
	if err := ospackages.Check(root); err != nil {
		return err
	}
	return printOSPackageResult(*jsonOutput, "written", lock, root)
}

func removeUnexpectedOSPackageFiles(root string, expected map[string][]byte) error {
	base := filepath.Join(root, ospackages.DefaultOutputDir)
	return filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, ok := expected[filepath.ToSlash(relative)]; ok {
			return nil
		}
		if filepath.ToSlash(relative) == ospackages.BootstrapPackagePath {
			return nil
		}
		return os.Remove(path)
	})
}

func printOSPackageResult(jsonOutput bool, status string, lock *ospackages.Lock, root string) error {
	packageCount := 0
	for _, variant := range lock.Variants {
		for _, platform := range variant.Platforms {
			packageCount += len(platform.Packages)
		}
	}
	result := struct {
		Status       string `json:"status"`
		Path         string `json:"path"`
		LockDigest   string `json:"lockDigest"`
		Snapshot     string `json:"snapshot"`
		VariantCount int    `json:"variantCount"`
		PackagePins  int    `json:"packagePins"`
	}{
		Status: status, Path: filepath.Join(root, ospackages.DefaultLockPath),
		LockDigest: lock.LockDigest, Snapshot: lock.Snapshot,
		VariantCount: len(lock.Variants), PackagePins: packageCount,
	}
	if jsonOutput {
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		fmt.Println(string(encoded))
		return nil
	}
	fmt.Printf(
		"OS package lock %s: %d variants, %d platform package pins, snapshot %s, %s\n",
		status,
		result.VariantCount,
		result.PackagePins,
		result.Snapshot,
		result.LockDigest,
	)
	return nil
}

func runLock(ctx context.Context, root string, args []string) error {
	fs := flag.NewFlagSet("lock", flag.ContinueOnError)
	check := fs.Bool("check", false, "fail if scanners/scanner-lock.yaml is stale")
	refreshImages := fs.Bool("refresh-images", false, "resolve mutable upstream image references through their registries")
	allowTagMutation := fs.Bool("accept-tag-mutation", false, "accept a mutable image tag resolving to a new digest")
	requireResolved := fs.Bool("require-resolved", false, "fail unless every upstream image has an immutable digest")
	jsonOutput := fs.Bool("json", false, "print a machine-readable operation result")
	output := fs.String("output", scannerlock.DefaultLockPath, "repository-relative lock output path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("lock does not accept positional arguments")
	}
	if *allowTagMutation && !*refreshImages {
		return fmt.Errorf("--accept-tag-mutation requires --refresh-images")
	}
	outputPath := *output
	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(root, outputPath)
	}
	var existing *scannerlock.Lock
	if loaded, err := scannerlock.LoadFile(outputPath); err == nil {
		existing = loaded
	} else if !os.IsNotExist(err) {
		// Additive lock-schema changes can make the old artifact invalid before
		// it can be regenerated. Reuse only its strictly decoded immutable
		// upstream-resolution cache; the old lock is never returned as valid
		// evidence and the generated replacement must pass full validation.
		data, readErr := os.ReadFile(outputPath)
		if readErr != nil {
			return err
		}
		existing, readErr = scannerlock.ParseGenerationCache(data)
		if readErr != nil {
			return err
		}
	} else if *check {
		return fmt.Errorf("%s is missing; run `go run ./cmd/scannertools lock`", outputPath)
	}
	generated, err := scannerlock.Generate(ctx, root, scannerlock.GenerateOptions{
		ExistingLock: existing, RefreshImages: *refreshImages,
		AllowTagMutation: *allowTagMutation,
	})
	if err != nil {
		return err
	}
	if *requireResolved {
		if err := generated.ValidateResolved(); err != nil {
			return err
		}
	}
	data, err := generated.MarshalYAML()
	if err != nil {
		return err
	}
	result := struct {
		Status           string `json:"status"`
		Path             string `json:"path"`
		LockDigest       string `json:"lockDigest"`
		DefinitionDigest string `json:"definitionDigest"`
		ToolCount        int    `json:"toolCount"`
		UpstreamImages   int    `json:"upstreamImages"`
		ResolvedImages   int    `json:"resolvedImages"`
	}{
		Path: outputPath, LockDigest: generated.LockDigest,
		DefinitionDigest: generated.Definition.Digest,
		ToolCount:        len(generated.Tools), UpstreamImages: len(generated.UpstreamImages),
	}
	for _, image := range generated.UpstreamImages {
		if image.Digest != "" {
			result.ResolvedImages++
		}
	}
	if *check {
		current, err := os.ReadFile(outputPath)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, data) {
			return fmt.Errorf("%s is stale; run `go run ./cmd/scannertools lock`", outputPath)
		}
		result.Status = "current"
	} else {
		if err := writeFileAtomic(outputPath, data, 0o644); err != nil {
			return err
		}
		result.Status = "written"
	}
	if *jsonOutput {
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		fmt.Println(string(encoded))
		return nil
	}
	if *check {
		fmt.Printf("scanner lock OK: %d tools, %d/%d upstream images resolved, %s\n",
			result.ToolCount, result.ResolvedImages, result.UpstreamImages, result.LockDigest)
	} else {
		fmt.Printf("wrote %s: %d tools, %d/%d upstream images resolved, %s\n",
			outputPath, result.ToolCount, result.ResolvedImages, result.UpstreamImages, result.LockDigest)
	}
	return nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	if err := temp.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func renderDocs(root string, check bool) error {
	m, err := manifest.LoadFile(filepath.Join(root, "scanners", "tools.yaml"))
	if err != nil {
		return err
	}
	path := filepath.Join(root, "scanners", "TOOLS.md")
	generated := docs.Markdown(m)
	if check {
		current, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, generated) {
			return fmt.Errorf("%s is stale; run `make scanners-docs`", path)
		}
		fmt.Println("scanner docs OK")
		return nil
	}
	if err := os.WriteFile(path, generated, 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", path)
	return nil
}

func checkUpstreamImages(root string, platforms []string) error {
	m, err := manifest.LoadFile(filepath.Join(root, "scanners", "tools.yaml"))
	if err != nil {
		return err
	}
	for _, name := range m.Names() {
		tool := m.Tools[name]
		if tool.IntegrationTier != manifest.TierUpstream {
			continue
		}
		required := platforms
		if len(tool.Image.Platforms) > 0 {
			required = tool.Image.Platforms
		}
		if err := inspectImage(tool.Image.PinnedReference, required); err != nil {
			return fmt.Errorf("%s (%s): %w", name, tool.Image.PinnedReference, err)
		}
		fmt.Printf("OK %s %s\n", name, tool.Image.PinnedReference)
	}
	return nil
}

func inspectImage(ref string, platforms []string) error {
	out, err := exec.Command("docker", "manifest", "inspect", "--verbose", ref).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("docker manifest inspect failed: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return err
	}
	available, err := manifestPlatforms(out)
	if err != nil {
		return err
	}
	for _, platform := range platforms {
		if _, ok := available[platform]; !ok {
			return fmt.Errorf("missing platform %s (available: %s)", platform, strings.Join(sortedKeys(available), ", "))
		}
	}
	return nil
}

func manifestPlatforms(data []byte) (map[string]struct{}, error) {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := map[string]struct{}{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, item := range x {
				walk(item)
			}
		case map[string]any:
			if platform, ok := x["Platform"].(map[string]any); ok {
				if osName, _ := platform["os"].(string); osName != "" {
					if arch, _ := platform["architecture"].(string); arch != "" {
						out[osName+"/"+arch] = struct{}{}
					}
				}
			}
			if descriptor, ok := x["Descriptor"].(map[string]any); ok {
				if platform, ok := descriptor["platform"].(map[string]any); ok {
					if osName, _ := platform["os"].(string); osName != "" {
						if arch, _ := platform["architecture"].(string); arch != "" {
							out[osName+"/"+arch] = struct{}{}
						}
					}
				}
			}
		}
	}
	walk(raw)
	if len(out) == 0 {
		return nil, fmt.Errorf("docker manifest output did not include platform metadata")
	}
	return out, nil
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
