# The Wolf

## The unified code security and remediation engine, built by AlphaBravo

> This document is a marketing content library, not a single fixed pitch. It is organized so that any section can be lifted on its own for a landing page, a one-pager, a slide, an email, or a sales conversation. Start at the top for high-level positioning, then pull from the deep dives, persona pages, use cases, and proof points as the audience requires.

---

## Table of contents

1. Positioning and elevator pitches
2. The problem we solve
3. Capabilities at a glance
4. The platform pillars (deep dives)
   - The unified scanner engine and language coverage
   - The containerized scanner backend
   - Trustworthy findings and noise reduction
   - The autonomous remediation engine
   - Auto-remediation loops
   - Quality gates and policy enforcement
   - Fleet-wide posture and trends
   - Repository sources and connectivity
   - Collections and organization
   - API, CLI, and automation-first design
   - Reporting, SARIF, and evidence
   - Identity, access control, and audit
   - Secrets and credential management
   - Optional AI assistance, off by default
   - Self-hosted, air-gapped, and Kubernetes-ready
5. Value by role
6. Use cases and scenarios
7. What makes the Wolf different
8. Architecture overview
9. Where the Wolf runs
10. Outcomes and business impact
11. Frequently asked questions
12. Glossary
13. About AlphaBravo and boilerplate

---

## 1. Positioning and elevator pitches

**One line.**
The Wolf is the single engine that scans every repository, proves every finding, and fixes what matters.

**One sentence.**
The Wolf brings dozens of best-in-class security and quality scanners, a trustworthy unified finding model, and an autonomous remediation engine into one self-hosted platform, so teams can find real problems across their whole stack and actually close them.

**Fifty words.**
The Wolf is a code security and remediation engine from AlphaBravo. It runs a deep catalog of scanners across every language and layer, normalizes the results into findings you can trust, and goes further than any scanner alone: it writes fixes and proves they work by rebuilding and rescanning.

**One hundred words.**
The Wolf, built by AlphaBravo, is a unified code security and remediation engine. Instead of stitching together a SAST tool, a dependency checker, a secrets scanner, and a container scanner, each with its own dashboard and its own idea of severity, teams run one engine that covers application code, dependencies, secrets, containers, and infrastructure as code. The Wolf deduplicates and scores findings so the signal is clear, enforces quality gates in CI, and rolls posture up across an entire fleet of repositories. Then it does what scanners do not: it produces verified fixes. It is self-hosted, automation-first, and private by default.

**The tagline shortlist.** Pick the one that fits the channel.

- Find it. Prove it. Fix it.
- One engine for every scanner, every language, every layer.
- Scanners find problems. The Wolf closes them.
- The remediation engine your scanners were missing.
- Your whole codebase, one honest answer.
- Stop counting findings. Start closing them.

---

## 2. The problem we solve

Modern software is assembled, not written. Your codebase is your code plus hundreds of dependencies, dozens of config files, container definitions, infrastructure manifests, pipelines, and the occasional secret that should never have been committed. Every one of those layers is a place a vulnerability can hide.

So teams bolt on tools. A SAST scanner here, a dependency checker there, a secrets scanner in one pipeline, a container scanner in another, an IaC linter someone configured two years ago. Each tool has its own output format, its own dashboard, its own login, and its own definition of severity. Nobody owns the whole picture.

Most organizations hit the same wall:

- **Tool sprawl.** Many vendors, many dashboards, many invoices, and no single answer to "are we secure right now?"
- **Alert fatigue.** Thousands of findings, most of them noise, drowning the handful that actually matter.
- **The remediation gap.** Scanners are very good at telling you what is wrong. They do almost nothing to fix it. That work falls back on engineers, by hand, one finding at a time.
- **Coverage gaps.** A scanner that only knows Python does nothing for the Go service beside it, the Terraform that deploys it, or the Dockerfile that ships it.
- **No fleet view.** Per-repository scanning cannot answer where risk is concentrated across hundreds of repositories or whether it is getting better or worse.
- **Compliance with no proof.** Auditors want evidence. Spreadsheets and screenshots are not evidence.

The deeper issue is that most tooling treats a single scan of a single repository as the unit of work, and then stops at the report. The Wolf treats the whole problem as one system: many scanners unified into one result you can trust, enforced in your pipeline, rolled up across your fleet, and closed with verified fixes.

---

## 3. Capabilities at a glance

- **Unified scanning.** A deep catalog of security and quality scanners across application code, dependencies, secrets, containers, infrastructure as code, and documentation, all through one engine.
- **Broad language coverage.** Go, Python, JavaScript and TypeScript, Ruby, PHP, Java and Kotlin, Rust, Swift, C and C++, shell, YAML, Dockerfiles, Terraform, Kubernetes, SQL, and more.
- **Trustworthy findings.** Deduplication, stable fingerprinting, scoring, suppressions, and baselines turn raw scanner noise into a finding count you can believe.
- **Autonomous remediation.** An engine that writes a fix on an isolated branch and proves it by rebuilding and rescanning, rather than trusting that the fix looks right.
- **Quality gates.** Pass or fail policy enforced on every scan, so risky code stops at the gate instead of shipping.
- **Fleet posture.** Risk, attention lists, and trends across every repository you own.
- **Automation-first.** Full API and command line parity, scoped access tokens, versioned endpoints, and offline API documentation.
- **Governed access.** Role-based access control, two-factor authentication, and scoped, revocable API keys.
- **Flexible sources.** Scan local paths, GitHub repositories public and private, and remote hosts over SSH.
- **Evidence built in.** Durable scan records, SARIF export, manifests, and a classified, searchable audit log.
- **Private by design.** Self-hosted, air-gap capable, with optional AI features off by default.

---

## 4. The platform pillars

Each pillar below is written to stand alone. Every one includes what it is, the key features, why it matters, and how it helps teams, so you can drop any single pillar into a page or a deck without additional context.

### The unified scanner engine and language coverage

**What it is.** The Wolf runs an entire arsenal of best-in-class open source scanners through one engine, and presents everything they find as a single, consistent result. One scan covers application code, dependencies, secrets, containers, infrastructure as code, and documentation quality, across the many languages a real codebase contains.

**Key features.**

- A deep catalog of scanners spanning static analysis, dependency and vulnerability checks, secret detection, infrastructure and container analysis, supply chain inspection, and code quality.
- Coverage across Go, Python, JavaScript and TypeScript, Ruby, PHP, Java, Kotlin, Rust, Swift, C and C++, shell, YAML, Dockerfiles, Terraform, Kubernetes, SQL, and prose.
- Automatic language and framework detection, so the right scanners run for each repository without manual setup.
- One normalized result model, so every tool speaks the same language about severity, rule, and location.
- Explicit tool selection when you want it, automatic selection when you do not.

**Why it matters.** You stop asking "which tool covers this repository?" The answer is always the same tool. One engine across every language and every layer means there are no quiet gaps where a whole class of risk goes unscanned because nobody wired up the right vendor.

**How it helps teams.** Security teams get real coverage without integrating a dozen products. Platform teams give every repository the same thorough treatment by default. Developers get one consistent result instead of a different tool and a different format for every part of the stack.

### The containerized scanner backend

**What it is.** Every scanner runs inside containers that the Wolf manages. The images are built from sources you can inspect and rebuild yourself, organized so the common tools live in a lean default image and heavier specialized toolchains live in their own images that are pulled only when needed.

**Key features.**

- Scanners packaged into maintained, versioned container images.
- A lean default image for common tools, with separate images for heavier toolchains so you never carry weight you do not use.
- Pinned tool versions, so a scan today and a scan next quarter are comparable.
- Images that build locally with no registry credentials required, and publish to your own registry when you are ready.
- Repository source mounted read-only, scanners run without elevated privilege, and the runtime stays isolated.

**Why it matters.** Scanning that depends on a vendor's cloud is scanning you do not fully control. By running every tool in containers you own, the Wolf works in private networks and air-gapped environments, stays reproducible, and never requires your source to leave your boundary.

**How it helps teams.** Security and platform teams get reproducible, self-owned scanning they can run anywhere. Regulated and disconnected environments get a scanning engine that fits their constraints instead of fighting them.

### Trustworthy findings and noise reduction

**What it is.** Raw scanner output is messy and repetitive. The same issue gets reported three different ways by three different tools, and the same backlog reappears scan after scan. The Wolf turns that raw output into findings you can trust.

**Key features.**

- Deduplication that collapses the same issue reported by multiple tools into one finding.
- Stable fingerprinting, so a finding keeps its identity across scans even as the file around it changes.
- Scoring and corroboration, so the findings most likely to matter rise to the top.
- Durable suppressions for issues you have reviewed and accepted, with an audit trail.
- Baselines and comparisons, so you see what changed against a known-good point in time instead of re-reading the whole backlog.

**Why it matters.** Alert fatigue is the reason real findings get missed. When the noise is collapsed, the duplicates are merged, the accepted items are suppressed, and only the new and the important are surfaced, the finding count becomes something a team will actually act on.

**How it helps teams.** Developers stop drowning. Security teams triage a clean, deduplicated list. Leadership gets a number that means something rather than a raw count that only ever grows.

### The autonomous remediation engine

**What it is.** This is what sets the Wolf apart. Most tools hand you a list of problems and walk away. The Wolf includes an engine that takes a finding, prepares an isolated working branch, writes a fix, and then proves the fix is real by rebuilding the code and rescanning the exact file to confirm the issue is gone and nothing new broke.

**Key features.**

- Per-finding work in isolated branches, so changes are contained and reviewable.
- A verification gate that judges a fix by what landed on disk, by a successful rebuild, and by a targeted rescan, never by trusting that the change looks correct.
- A regression check, so a fix that clears one issue but introduces another is rejected.
- A flexible engine path that prefers efficient agent tooling and falls back to a direct model path, so it stays capable and cost-aware.
- A safe default of producing a reviewable branch and a proposed change, with no automatic push and no surprise commits.
- The entire remediation surface gated behind a master switch that is off until you turn it on.

**Why it matters.** The remediation gap is where security programs stall. Finding problems is the easy half; fixing them at scale is the half that never gets staffed. An engine that produces verified fixes turns a backlog of findings into a queue of reviewable branches, and judges every fix by proof rather than by hope.

**How it helps teams.** Engineers review a clean branch with a fix that has already been rebuilt and rescanned, instead of starting from a raw finding. Security teams watch the backlog shrink. Leadership sees remediation become a throughput problem the platform helps solve, rather than a permanent staffing shortfall.

### Auto-remediation loops

**What it is.** Beyond fixing a single finding, the Wolf can run the full loop: scan, fix, rescan, and repeat, driving a repository toward a cleaner state across iterations within the bounds you set.

**Key features.**

- A closed loop of scan, remediate, and verify, run automatically.
- Bounded by iteration, time, and cost limits you control.
- Severity floors, so the loop spends effort where it matters first.
- A full record of every attempt and its outcome.

**Why it matters.** The manual version of this loop, export the findings, hand them to an engineer, wait, rescan, repeat, is exactly the slow path that leaves findings open for months. Automating it, with limits and an audit trail, compresses weeks of back-and-forth into a process the platform runs for you.

**How it helps teams.** Teams point the loop at a repository and review the results, rather than shepherding each finding by hand. Progress on the backlog becomes continuous rather than occasional.

### Quality gates and policy enforcement

**What it is.** Define the standard once, then let the Wolf enforce it on every scan. A gate evaluates a scan against your policy and returns a clear pass or fail that your pipeline can act on.

**Key features.**

- Policies scoped where you need them, applied consistently across scans.
- Rules such as no new high-severity findings, no committed secrets, and dependency risk under a set threshold.
- A clear pass or fail result for continuous integration.
- Durable gate results recorded alongside the scan.

**Why it matters.** A finding caught at the gate is far cheaper and safer than the same finding caught in an incident review. Consistent policy means the standard is actually standard, instead of depending on who remembered to check.

**How it helps teams.** Developers get fast, unambiguous feedback. Security and compliance teams get assurance that the rules are enforced everywhere, automatically, on every change.

### Fleet-wide posture and trends

**What it is.** When you run dozens or hundreds of repositories, per-repository scanning is not enough. Fleet mode rolls everything up: posture across the whole estate, which repositories need attention, where risk is concentrated, and how it is trending over time.

**Key features.**

- Aggregate posture across every repository, broken down by severity.
- A needs-attention view that ranks repositories by real risk.
- Inventory views by source type, language, and collection.
- Trends that show whether risk is improving or worsening week over week.
- Aggregation by rule, so a single systemic issue across many repositories is visible as one pattern.

**Why it matters.** Leadership does not ask about one repository. It asks whether the organization is getting safer. A fleet view turns hundreds of separate scans into the one answer that decision-makers actually need.

**How it helps teams.** Security leaders get a portfolio view of risk. Platform teams see where to invest first. Everyone works from the same picture instead of a patchwork of individual reports.

### Repository sources and connectivity

**What it is.** The Wolf scans code wherever it lives. Point it at a local path, connect a GitHub repository whether public or private, or reach a remote host over SSH.

**Key features.**

- Local path scanning for code already on the machine.
- GitHub repositories, public and private, with credentials managed as encrypted secrets.
- Remote scanning over SSH to reach code on other hosts.
- A writability check that confirms up front whether the Wolf can produce a fix branch for a given source.
- Collections and branches handled as first-class inputs to a scan.

**Why it matters.** Real codebases are not all in one place. A scanning engine that only handles one source type forces you back into per-tool sprawl for everything else. One engine that handles local, hosted, and remote sources keeps coverage unified.

**How it helps teams.** Teams scan everything they own through one workflow, regardless of where the code sits or how it is reached.

### Collections and organization

**What it is.** Repositories can be grouped into collections that reflect how your organization actually works, by team, by environment, by product, or by any boundary that matters to you.

**Key features.**

- Group repositories into named collections.
- Scan and report at the collection level.
- Collection-scoped configuration and tool selection.
- A structure that mirrors teams, products, or environments.

**Why it matters.** A flat list of hundreds of repositories is hard to govern. Collections give the fleet a shape, so posture, policy, and reporting can follow the lines of the organization.

**How it helps teams.** Platform teams organize the fleet to match reality. Leaders get rollups along the boundaries they care about.

### API, CLI, and automation-first design

**What it is.** Everything the Wolf can do is exposed through a documented API and a complete command line, with full parity between them and the web interface. Anything a person can do in the UI, an automation or an AI agent can do through the API or CLI.

**Key features.**

- A full HTTP API with versioned endpoints.
- A command line that covers the entire feature set, suitable for humans and for scripts.
- Offline API documentation, so the reference works even in disconnected environments.
- Scoped access tokens with verb and resource permissions.
- Output formats suited to both terminals and pipelines.

**Why it matters.** A security tool that can only be driven by clicking is a tool that gets skipped under pressure. The Wolf is built to be the engine behind your automation, your pipelines, and your agent workflows, not another dashboard someone has to remember to open.

**How it helps teams.** Platform teams wire the Wolf into continuous integration and internal tooling. Automation and AI workflows drive it directly. Nothing is locked behind a screen.

### Reporting, SARIF, and evidence

**What it is.** Every scan produces durable, portable output: normalized findings, a manifest of what ran, standard SARIF for interoperability, and human-readable reports for review and handoff.

**Key features.**

- SARIF export for interoperability with other tools and platforms.
- A scan manifest recording which tools ran, what was detected, and the resulting counts.
- Normalized findings with rule, severity, and location, plus fingerprints for stability.
- Human-readable summaries and fix-oriented reports.
- Durable artifacts retained per scan.

**Why it matters.** When an auditor or an incident asks what was scanned and what was found, the answer should be a record, not a reconstruction. Standard formats mean the Wolf fits into the tooling you already have rather than trapping its output.

**How it helps teams.** Compliance teams get evidence on demand. Engineering teams get output they can hand off, diff, and feed into other systems.

### Identity, access control, and audit

**What it is.** The Wolf governs who can do what, proves who they are, and keeps a classified record of what was done. Access is role-based, sign-in can require a second factor, sessions are hardened, and every change is logged and classified.

**Key features.**

- Role-based access control with administrator and standard-user roles, and per-user data isolation, so people manage only the repositories, scans, secrets, and credentials they own.
- Two-factor authentication (TOTP authenticator apps), self-service for each user or required organization-wide, with one-time recovery codes and an administrator reset for lost devices.
- Scoped, revocable API keys with fine-grained verb-and-resource permissions and optional expirations, for the CLI, CI, and agents — minted once, stored only as a hash.
- Hardened browser sessions, an administrator bootstrap for first setup, and a clean split between a personal account area and an administrators-only settings area with a global oversight view of every user's keys, secrets, and nodes.
- A classified, security-aware audit log: every mutating action and every login recorded with a semantic event type, a category, a severity, the actor, the source address, and the result — searchable, filterable, and paginated.
- Versioned, consistent access controls across the API, CLI, and UI.

**Why it matters.** A platform that touches source code and can change it must be governed and accountable. Roles and per-user isolation enforce least privilege, two-factor auth hardens the front door, and a classified audit trail turns "who disabled MFA, changed a role, or touched that secret, and from where?" from an investigation into a single filtered query.

**How it helps teams.** Security teams enforce least privilege and require strong authentication. Compliance teams get an attributable, classified record they can filter by category and severity. Administrators hand out exactly the access each user or automation should have, and nothing more, and keep oversight of the whole estate without ever reading another user's secret material.

### Secrets and credential management

**What it is.** The credentials the Wolf needs, such as repository tokens and model keys, are stored as encrypted secrets rather than scattered across config files or command lines.

**Key features.**

- Encrypted storage for repository tokens, model keys, and other sensitive values.
- Per-user scoping, so credentials belong to an owner.
- Secrets referenced by the platform rather than copied around.
- Sensitive values kept out of logs and plain configuration.

**Why it matters.** Credentials in plain text are one of the most common and most damaging mistakes in any toolchain. Centralized, encrypted secret handling removes that exposure from the start.

**How it helps teams.** Teams connect private repositories and optional model providers safely, with a single place to manage and rotate sensitive material.

### Optional AI assistance, off by default

**What it is.** The Wolf can use AI to help triage, enrich, and explain findings, and to power the remediation engine. Every one of these features is optional and off by default, and turns on only when you decide and supply your own provider.

**Key features.**

- AI-assisted enrichment and triage of findings, when enabled.
- Fix prompts and remediation powered by your choice of agent tooling or model provider.
- A master switch per capability, defaulting to off.
- Your provider, your keys, under your control.

**Why it matters.** AI in a security tool should be a choice, not a condition of use. By keeping every AI feature off until you opt in, the Wolf gives teams the benefits of automation without forcing their code or findings through a model they did not choose.

**How it helps teams.** Privacy-sensitive and regulated teams run the Wolf with AI entirely off and lose nothing core. Teams that want the assistance turn it on deliberately, with their own provider, on their own terms.

### Self-hosted, air-gapped, and Kubernetes-ready

**What it is.** The Wolf runs on your infrastructure, in your cloud, or fully disconnected. It is built to operate as a service with separable workers, so it scales the way modern platforms do.

**Key features.**

- Fully self-hosted, with no dependency on a vendor cloud to scan.
- Air-gap capable, with scanner images you can build and host yourself.
- A durable, queue-driven worker model suited to scaling remediation work, and ready for Kubernetes.
- Offline documentation so the platform is usable without internet access.

**Why it matters.** For many organizations, the requirement is simple and absolute: code does not leave the building. A platform that is self-hosted and air-gap capable meets that requirement instead of negotiating with it, and a worker model that scales keeps the platform useful as the fleet grows.

**How it helps teams.** Security and platform teams deploy the Wolf inside their boundary, scale it to their fleet, and run it in the most restricted environments without compromise.

---

## 5. Value by role

**For security teams.** Get real coverage across code, dependencies, secrets, containers, and infrastructure from one engine. Cut alert fatigue with deduplication, scoring, and baselines. Close findings with verified, autonomous fixes instead of waiting on manual remediation. Keep everything inside your boundary, with AI off unless you choose it.

**For platform and DevSecOps teams.** Standardize scanning across every repository and pipeline through one API and CLI. Enforce quality gates so risky code stops at the gate. Organize the fleet into collections, and roll posture up across all of it. Wire the Wolf into continuous integration and internal tooling instead of adding another dashboard.

**For application and development teams.** Get one consistent result for the whole stack instead of a different tool per language. Receive fast, clear gate feedback in your pipeline. Review a clean branch with a fix that has already been rebuilt and rescanned, rather than starting from a raw finding.

**For engineering leadership.** Get one honest answer about risk across the whole portfolio, and a trend that shows whether it is improving. Close the remediation gap that keeps findings open for months. Reduce the cost and incident risk that come from unscanned layers and unfixed issues.

**For compliance and risk teams.** Produce evidence on demand with durable scan records, SARIF export, and a classified, filterable audit log. Rely on role-based access, two-factor authentication, and attributable accountability. Enforce policy baselines across the fleet rather than per repository.

**For regulated and air-gapped environments.** Run a complete scanning and remediation engine entirely inside your boundary, with self-built scanner images and AI off by default. Meet the requirement that code never leaves, without giving up coverage or remediation.

---

## 6. Use cases and scenarios

**Consolidating a pile of scanners into one engine.** Replace a SAST tool, a dependency checker, a secrets scanner, and a container scanner with a single engine that covers all of it and reports one trustworthy result.

**Closing the remediation backlog.** Point the autonomous engine, or the full scan-fix-rescan loop, at a repository and turn a backlog of findings into a queue of reviewable, verified fix branches.

**Enforcing a security bar in CI.** Define a quality gate, such as no new high-severity findings and no committed secrets, and enforce it on every scan so risky code never ships.

**Scanning private and remote code without exposure.** Connect private GitHub repositories with encrypted tokens, or reach code on other hosts over SSH, all through one workflow.

**Getting a fleet-level view of risk.** Roll posture up across hundreds of repositories, find where risk is concentrated, and track whether it is improving over time.

**Operating in an air-gapped environment.** Build the scanner images yourself, run the Wolf fully disconnected with AI off, and scan and remediate without anything leaving the network.

**Feeding existing tools and pipelines.** Export SARIF and durable artifacts so the Wolf's findings flow into the dashboards, tickets, and systems you already run.

---

## 7. What makes the Wolf different

- **It closes findings, not just reports them.** The autonomous remediation engine produces fixes and proves them by rebuild and rescan. That is the half of the problem most tools never touch.
- **Proof over self-report.** A fix is judged by what landed on disk and by a targeted rescan, never by trusting that the change looks right. Reliability is built into the gate.
- **One engine, every layer.** Code, dependencies, secrets, containers, and infrastructure as code, across many languages, unified into one result instead of many silos.
- **Findings you can believe.** Deduplication, fingerprinting, scoring, suppressions, and baselines turn raw noise into a count teams will act on.
- **Automation-first by design.** Full API and CLI parity with scoped tokens and offline docs, built to be driven by pipelines and agents, not just by clicks.
- **Private by default.** Self-hosted, air-gap capable, with optional AI off until you opt in. Your code stays inside your boundary.

---

## 8. Architecture overview

The Wolf is built around a control service, a containerized scanner backend, and separable workers.

- **The control service** is the API, the command line surface, and the web interface, backed by a durable store. It holds repositories, scans, findings, policies, and fleet posture, and serves one consistent experience to people and to machines.
- **The containerized scanner backend** runs every tool in images the platform manages. The repository is mounted read-only, scanners run without elevated privilege, and tool versions are pinned for reproducibility.
- **The remediation worker** is separable from the control service, by design. It claims fix work from a durable queue, prepares isolated branches, drives the engine, and runs every change through the verification gate. The queue-driven model is built to scale and to run under Kubernetes.
- **Optional AI** plugs in only when enabled, using your chosen provider and your keys, with each capability defaulting to off.

The result is an architecture that runs inside your boundary, scales with your fleet, and keeps the act of fixing code governed and verified.

---

## 9. Where the Wolf runs

The Wolf is designed to run wherever your code and your constraints require: in public cloud, in private data centers, and in fully air-gapped environments. Because it is self-hosted and its scanner images can be built and hosted by you, it never depends on a vendor cloud to do its work. One deployment can cover local repositories, hosted repositories, and remote hosts, presenting all of it through a single consistent experience, online or disconnected.

---

## 10. Outcomes and business impact

These are the outcomes to lead with when the audience cares about results rather than features.

- **A closed remediation gap.** Verified fixes turn a stalled backlog into reviewable branches, so findings get resolved instead of aging.
- **Less noise, faster triage.** Deduplication, scoring, and baselines mean the team spends its attention on what matters.
- **Fewer escapes to production.** Quality gates stop risky code at the pipeline instead of in an incident.
- **Real coverage, no gaps.** One engine across every language and layer removes the quiet blind spots that single-vendor tools leave.
- **Lower tooling cost and overhead.** One engine replaces a stack of overlapping scanners and dashboards.
- **Portfolio-level assurance.** Fleet posture and trends give leadership one honest, improving picture of risk.
- **Audit readiness.** Durable records, SARIF, and a classified audit log — filterable by category and severity — make evidence a query rather than a project.
- **Uncompromised privacy.** Self-hosted and air-gap capable, with AI off by default, so code never has to leave the boundary.

---

## 11. Frequently asked questions

**Do we have to replace our existing tools?**
No. The Wolf exports standard SARIF and durable artifacts, so it fits alongside the dashboards, ticketing, and systems you already run. Many teams use it as the unified engine that feeds everything else.

**Does our source code leave our environment?**
No. The Wolf is self-hosted and the scanner images can be built and run by you. It is air-gap capable, and optional AI features are off by default, so nothing leaves your boundary unless you explicitly choose to send it.

**How is the remediation engine different from a tool that suggests fixes?**
It does not just suggest. It prepares an isolated branch, makes the change, then proves it by rebuilding and rescanning the affected file, and rejects the change if a new issue appears. You review a verified branch, and the engine is off until you enable it.

**Can it scan private repositories?**
Yes. Connect private GitHub repositories with credentials stored as encrypted secrets, scan local paths, or reach code on other hosts over SSH, all through one workflow.

**Can we drive it from automation instead of a UI?**
Yes. Everything is available through a documented API and a complete command line with full parity, plus scoped access tokens and offline documentation. The platform is built to be driven by pipelines and agents.

**Do we have to use AI?**
No. Every AI feature is optional and off by default. The core scanning, gating, fleet posture, and reporting work fully without it. When you do want assistance, you bring your own provider and keys.

**Does it work in an air-gapped environment?**
Yes. Build the scanner images yourself, run the Wolf disconnected with AI off, and scan and remediate entirely inside the network.

**How does it handle access control?**
Through role-based access control with administrator and standard-user roles and per-user data isolation, optional two-factor authentication that can be required organization-wide, scoped and revocable API keys with verb-and-resource permissions, hardened sessions, and a classified, searchable audit log — applied consistently across the API, CLI, and UI.

---

## 12. Glossary

- **Finding.** A single normalized issue produced by a scanner, with rule, severity, location, and a stable fingerprint.
- **Scanner.** One of the many security or quality tools the Wolf runs inside its containerized backend.
- **Quality gate.** A policy evaluated against a scan that returns a clear pass or fail for a pipeline.
- **Baseline.** A known-good point in time that later scans are compared against, so you see what changed.
- **Suppression.** A reviewed and accepted finding that is set aside, with an audit trail.
- **Fingerprint.** A stable identity for a finding that survives across scans even as the surrounding code changes.
- **Fleet.** The full set of repositories the Wolf manages, viewed and governed as one.
- **Collection.** A named group of repositories that mirrors a team, product, or environment.
- **Remediation engine.** The component that produces verified fixes on isolated branches.
- **Verification gate.** The check that judges a fix by rebuild and targeted rescan rather than by appearance.
- **Loop.** The automated cycle of scan, fix, and rescan, bounded by limits you set.
- **SARIF.** A standard format for static analysis results, used for interoperability.

---

## 13. About AlphaBravo and boilerplate

**Short boilerplate.**
The Wolf is developed by AlphaBravo, a team focused on secure, cloud-native, and production-grade engineering. AlphaBravo builds the Wolf to find problems across the whole stack, tell the truth about them, and help close them, all inside your own boundary.

**Long boilerplate.**
The Wolf is developed by AlphaBravo. AlphaBravo builds secure, cloud-native platforms for organizations that take security seriously, including the most demanding regulated and air-gapped environments. The Wolf reflects that focus: a single engine that unifies a deep catalog of scanners, turns their raw output into findings teams can trust, enforces standards in the pipeline, rolls risk up across an entire fleet, and goes the step scanners never take by producing fixes it proves are real. It is self-hosted, automation-first, and private by default, because the secure path should also be the practical one.

The Wolf. Find it. Prove it. Fix it.

To learn more or to see the Wolf in action, reach out to AlphaBravo. <https://alphabravo.io>
