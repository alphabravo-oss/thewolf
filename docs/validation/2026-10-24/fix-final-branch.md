# F07.6 — final branch acceptance

Before returning an assembled branch or publishing it, the orchestrator now runs a required final verifier. It loads the full original scan baseline even for jobs targeting a subset of findings, and includes the scan's selected/completed tools plus tools attached to baseline findings. Tools with no original findings are therefore still checked. Missing scan records, malformed tool scope, incomplete accepted-finding evidence, missing/skipped tool execution and scanner failures stop the job with its workspace retained.

The production gate checks every changed language supported by the existing build verifier, then scans the whole checkout. Earlier accepted fixes and findings cleared in previous rounds must remain absent. New tool/rule/file findings or additional occurrences block acceptance; unchanged baseline findings can remain, including findings outside the selected fix set. Rule/file occurrence counts tolerate line shifts. This retains the existing loop identity approach rather than claiming canonical vulnerability identity or proving semantic equivalence between replacement occurrences.

Continuation restores prior accepted findings and round clearances from the saved summary. Push resumes also run final verification without running engines. A PR retry for an already-published job requires a clean checkout at its saved published SHA. Final verification stages, including skipped build checks, are saved in the job summary. Surviving uncommitted edits are committed only after final verification. A diff change during verification stops publication; Go build output is discarded and Python syntax checks no longer create `__pycache__` files in the proposed patch.

Verification:

- `go test -race ./internal/fix/... -count=1` passes ([receipt](fix-final-branch.txt)). After extending selected-tool scope and retaining earlier round clearances, `go test -race ./internal/fix/orchestrator -count=1` passes again ([receipt](fix-final-branch-scope.txt)).
- Real Git worktrees with two tool turns prove the final gate rejects reintroduced earlier fixes, findings in extra files, unavailable scanners and scanner-side source mutations, while accepting a branch with an unchanged, unselected baseline finding. Tests assert retained workspaces and no push/cleanup on failure. Scanner results and remote publication are injected; this is not a real remote journey.
- Publication/continuation tests verify saved fixes are rechecked, earlier round clearances are retained, changed published HEADs cannot be reused, and engines are not rerun for push resumes.
- Gate tests cover increased occurrence counts, shifted existing findings, zero-finding tool scope and build failures in an extra language. Real Go/Python checks prove verification creates no binary/bytecode files in the checkout.
- Production wiring tests reject nil results, silently omitted tools, skipped tools and scanner failures; whole-checkout explicit tool scope is asserted.
- Scoped Go lint reports zero issues ([receipt](fix-final-branch-lint.txt)); scanner context/lock synchronization is recorded [here](fix-final-branch-sync.txt).

F07.6 remains open for complete cancellation/restart attempt-history qualification. Build checks retain existing language/toolchain limitations and explicitly record skips (F06); immutable scanner/image/policy inputs remain F08; adversarial harness/process isolation remains F09/F13; actual remote qualification remains F15. No deployment or remote publication occurred.
