# Scanner plugins (`pkg/scannerapi`)

`pkg/scannerapi` is the public contract for scanners that live outside Community's `internal/` tree, chiefly the Enterprise overlay's adapters for **customer-licensed commercial engines** (for example Burp Suite DAST, Fortify, Checkmarx, HCL AppScan). A registered scanner runs through the same scheduler, container isolation, raw-output capture, normalization, corroboration, baselines and quality gates as the first-party scanners. First-party scanners stay in `plugins/` and `scanners/tools.yaml`.

## Contract

```go
import "github.com/alphabravocompany/thewolf/pkg/scannerapi"

type Plugin = models.Plugin            // Name, Category, Languages, CheckAvailable, Execute
func Register(p Plugin, meta Metadata) error
func Run(ctx, opts ExecuteOpts, tool string, c Container) (Result, error)
func FindingsFromSARIF(tool string, data []byte) ([]Finding, error)
func NotApplicable(reason string) error // input absent: skipped, never counted as coverage
func Skipped(reason string) error       // other skips (e.g. not configured)
```

- **Types are aliases** of Wolf's model (`Plugin`, `ExecuteOpts`, `Finding`, categories, severities), so there is one model.
- **`Register`** refuses a name that is already registered. A licensed adapter can never replace a first-party scanner. Call it while the process starts, before serving.
- **`Metadata`**:
  - `Image`: the customer-supplied image.
  - `ResourceClass`: `light`, `medium` or `heavy`.
  - `Timeout`, `NetworkRequired`.
  - `Secrets`: container environment name → Wolf secret name.
- **`Applicability(repoPath, target string) error`** is optional. Implement it to return `NotApplicable` when the input is absent, such as a DAST scanner without a target URL.

## Two adapter shapes

1. **Container CLI.** The customer supplies a licensed image, for example a vendor CLI or a headless DAST scanner.
   - `Execute` calls `scannerapi.Run` and parses the report. `FindingsFromSARIF` covers tools that emit SARIF; parse native formats otherwise.
   - `Run` applies the same isolation, read-only repository mount, CPU/memory limits and Go soft memory limit, and the same image-readiness check and error messages as first-party scanners.
   - A non-zero exit is not an error by itself (many tools exit non-zero when they find issues). Failing to start, or being killed, is.
2. **Vendor server API.** The engine runs on a customer server or SaaS (Fortify SSC/ScanCentral, Checkmarx One, Burp Suite DAST server, Veracode). `Execute` submits the scan, polls with `ctx` for cancellation and deadlines, and fetches results. Register without an `Image`.

## Licenses and secrets

- A scanner declares its secrets in `Metadata.Secrets`. Before each run, Wolf resolves them from the **scan owner's custom secrets** by name and passes them only as container environment (`ExecuteOpts.SecretEnv`, which `Run` adds automatically).
- If a secret is missing, the tool fails with "requires the secret …, add it as a custom secret named …". It never runs unlicensed, and a run without a secret store, such as a local CLI scan, fails closed.
- Never log, persist or put secret values in reports or raw output.

## Edition and redistribution

- Licensed adapters belong in the Enterprise overlay. Wolf enforces the `enterprise.plugins` entitlement (`pkg/entitlement.Plugins`): without it, every scanner registered through `scannerapi` is skipped with "requires an Enterprise license", and coverage shows the gap. Community stays fully usable without them.
- Commercial engines are **customer-supplied only**. Wolf does not build, publish, mirror or redistribute their images, and the scanner factory never includes them.

## Example

`pkg/scannerapi/scannerapi_test.go` registers an out-of-tree scanner and runs it through the real runner:
- the customer image, a heavy resource class, and a declared licence secret
- SARIF on stdout becomes findings attributed to that scanner
- a missing licence fails closed
