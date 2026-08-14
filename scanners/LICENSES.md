# Bundled tool licenses

`wolf-scanners` distributes the binaries listed below, each with its own
license. Operators are responsible for confirming their use complies with
each tool's terms.

The release gate contains an explicit allowlist for the GPL-1.0, GPL-2.0,
GPL-3.0, LGPL-2.0, LGPL-2.1, LGPL-3.0 (including the specific autoconf and
bison exception identifiers present in the Debian packages), and Sleepycat
license families used by the scanner and fixer images. These unmodified,
standalone programs and libraries are intentionally redistributed with this
public build source and their notices. This is an affirmative open-source
distribution policy, not a vulnerability exception. Operators remain
responsible for the corresponding notice and source-availability obligations;
unknown, proprietary, and every unlisted license still fail the image gate.

| Tool | License | Notes |
|---|---|---|
| semgrep | LGPL-2.1 | Free for SaaS/CLI use; semgrep registry rules may have separate terms. |
| bandit | Apache-2.0 | — |
| ruff | MIT | — |
| mypy | MIT | — |
| pip-audit | Apache-2.0 | — |
| radon | MIT | — |
| vulture | MIT | — |
| detect-secrets | Apache-2.0 | — |
| checkov | Apache-2.0 | — |
| sqlfluff | MIT | — |
| gosec | Apache-2.0 | — |
| staticcheck | MIT | — |
| govulncheck | BSD-3-Clause | Queries vuln.go.dev at runtime. |
| eslint | MIT | — |
| @stoplight/spectral-cli | Apache-2.0 | — |
| brakeman | MIT | — |
| rubocop | MIT | — |
| phpstan | MIT | — |
| cppcheck | GPL-3.0 | Distributing the binary in a closed-source container is OK; redistribution of modified cppcheck source would not be. |
| shellcheck | GPL-3.0 | Same as cppcheck. |
| infer | MIT | — |
| pmd | BSD-style | — |
| trivy | Apache-2.0 | Vuln DB has separate terms (aquasec). |
| gitleaks | MIT | — |
| trufflehog | AGPL-3.0 | **Notable** — if wolf-scanners is offered as a hosted service, operators must publish the wolf-scanners build sources. We comply by keeping `scanners/` open in the wolf repo. |
| hadolint | GPL-3.0 | — |
| dockle | Apache-2.0 | — |
| tflint | MPL-2.0 | — |
| kubescape | Apache-2.0 | — |
| kube-linter | Apache-2.0 | — |
| syft | Apache-2.0 | — |
| grype | Apache-2.0 | — |
| osv-scanner | Apache-2.0 | Queries osv.dev at runtime. |
| vale | MIT | — |
| nuclei | MIT | Templates may have own licenses. |
| scorecard | Apache-2.0 | OpenSSF project; some checks call GitHub API and may need GITHUB_AUTH_TOKEN. |
| renovate | AGPL-3.0 | Mend Renovate. Wolf runs it in `--dry-run=full` (detect-only) so we never open PRs. AGPL applies the same way as trufflehog above: redistributing wolf-scanners as a hosted service requires source disclosure (we keep `scanners/` open in the wolf repo). |
| kics | Apache-2.0 | Checkmarx KICS — multi-format IaC scanner. |
| conftest | Apache-2.0 | OPA / Open Policy Agent project. |
| pluto | Apache-2.0 | Fairwinds. |
| detekt | Apache-2.0 | — |
| bearer | ELv2 | Elastic License v2 — free for use including SaaS, but restrictions on offering Bearer-as-a-service. Wolf's use case (running it against an operator's own code) is permitted. |
| markdownlint-cli | MIT | David Anson. |
| yamllint | GPL-3.0 | Adrien Vergé. Same shape as cppcheck/shellcheck — bundling the binary in a container is fine. |
| gokart | Apache-2.0 | Praetorian Inc. |
| rust toolchain + clippy | Apache-2.0 / MIT | — |
| cargo-audit | Apache-2.0 / MIT | RustSec. Fetches the advisory database at scan time. |
| cargo-deny | MIT / Apache-2.0 | Embark Studios. Advisory check by default; full policy when deny.toml is present. |
| zizmor | MIT | GitHub Actions / Dependabot / pre-commit static analysis. |
| poutine | Apache-2.0 | BoostSecurity. CI pipeline supply-chain scanner. |
| **CodeQL** | **GitHub CodeQL Terms** | **Free for analysis of open-source code only.** Commercial / private-repo analysis requires a GitHub Enterprise Advanced Security license. Operators of wolf in commercial settings must confirm their CodeQL license; if you don't have one, set `disabled_tools: [codeql]` in `wolf.yaml`. |

If you spot an error here, open an issue or PR.
