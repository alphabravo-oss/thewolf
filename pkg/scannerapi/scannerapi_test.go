package scannerapi_test

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
	"github.com/alphabravocompany/thewolf/pkg/entitlement"
	"github.com/alphabravocompany/thewolf/pkg/scannerapi"
)

type grants string

func (g grants) Allows(capability string) bool { return capability == string(g) }

// licensedSARIF is what an Enterprise adapter for a customer-licensed engine
// looks like: run the customer's image, read SARIF from stdout.
type licensedSARIF struct{}

func (licensedSARIF) Name() string                     { return "example-licensed-sast" }
func (licensedSARIF) Category() scannerapi.Category    { return scannerapi.CategorySAST }
func (licensedSARIF) Languages() []scannerapi.Language { return nil }
func (licensedSARIF) CheckAvailable() bool             { return true }
func (licensedSARIF) Execute(ctx context.Context, opts scannerapi.ExecuteOpts) ([]scannerapi.Finding, error) {
	out, err := scannerapi.Run(ctx, opts, "example-licensed-sast", scannerapi.Container{Args: []string{"scan", "/scan"}})
	if err != nil {
		return nil, err
	}
	return scannerapi.FindingsFromSARIF("example-licensed-sast", out.Stdout)
}

const report = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"VendorEngine","rules":[{"id":"SQLI-1","shortDescription":{"text":"SQL injection"}}]}},
"results":[{"ruleId":"SQLI-1","level":"error","message":{"text":"Tainted SQL"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"app/db.go"},"region":{"startLine":12}}}]}]}]}`

// An out-of-tree scanner runs through the normal runner path: registered image
// and resources, declared license secret injected only as container env, SARIF
// findings attributed to the scanner.
func TestRegisteredScannerRunsThroughRunner(t *testing.T) {
	if err := scannerapi.Register(licensedSARIF{}, scannerapi.Metadata{
		Image: "registry.customer.example/vendor-engine:9.1", ResourceClass: "heavy",
		Secrets: map[string]string{"VENDOR_LICENSE": "vendor-license"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := scannerapi.Register(licensedSARIF{}, scannerapi.Metadata{}); err == nil {
		t.Fatal("a registered scanner name must not be replaceable")
	}
	cfg := &container.Config{Image: "wolf-scanners:test"}
	if got := cfg.ImageFor("example-licensed-sast"); got != "registry.customer.example/vendor-engine:9.1" {
		t.Fatalf("registered image not used: %s", got)
	}
	if !slices.Contains(cfg.AllImages(), "registry.customer.example/vendor-engine:9.1") {
		t.Fatal("registered image missing from the scanner inventory")
	}

	var sawLicense bool
	container.SetCommandFactory(func(ctx context.Context, cfg *container.Config, opts container.Options, tool string, args ...string) *exec.Cmd {
		sawLicense = opts.ExtraEnv["VENDOR_LICENSE"] == "lic-123"
		return exec.CommandContext(ctx, "printf", "%s", report)
	})
	t.Cleanup(func() { container.SetCommandFactory(nil) })

	run := func(resolve func(context.Context, string) (string, error)) *runner.RunResult {
		t.Helper()
		result, err := runner.Run(t.Context(), runner.RunConfig{
			RepoPath: t.TempDir(), Registry: plugin.Global, Tools: []string{"example-licensed-sast"},
			ContainerCfg: cfg, ResolveSecret: resolve, Concurrency: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	// Commercial scanners are Enterprise-only: without enterprise.plugins the
	// run skips the scanner with a license reason instead of executing it.
	result := run(nil)
	if !slices.Contains(result.ToolsSkipped, "example-licensed-sast") || !strings.Contains(result.ToolSkipReasons["example-licensed-sast"], "Enterprise license") {
		t.Fatalf("unlicensed commercial scanner was not gated: %+v", result)
	}
	entitlement.SetActive(grants(entitlement.Plugins))
	t.Cleanup(func() { entitlement.SetActive(nil) })

	result = run(func(_ context.Context, name string) (string, error) {
		if name != "vendor-license" {
			t.Fatalf("unexpected secret %q", name)
		}
		return "lic-123", nil
	})
	if !sawLicense {
		t.Fatal("declared license secret was not passed to the container")
	}
	if len(result.Findings) != 1 || result.Findings[0].ToolName != "example-licensed-sast" || result.Findings[0].FilePath != "app/db.go" {
		t.Fatalf("SARIF findings not attributed to the registered scanner: %+v", result.Findings)
	}

	// A missing license fails the tool with an actionable message; it never runs unlicensed.
	result = run(func(context.Context, string) (string, error) { return "", errors.New("not found") })
	if err := result.ToolsFailed["example-licensed-sast"]; err == nil || !strings.Contains(err.Error(), `custom secret named "vendor-license"`) {
		t.Fatalf("missing license secret not reported: %v", err)
	}
	result = run(nil)
	if err := result.ToolsFailed["example-licensed-sast"]; err == nil || !strings.Contains(err.Error(), "no secret resolver") {
		t.Fatalf("run without a secret store must fail closed: %v", err)
	}
}
