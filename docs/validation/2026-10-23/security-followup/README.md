# Security boundary follow-up — 2026-10-23

Evidence for PLAN R03.10/R03.11. The full goal remains active. These source
changes are uncommitted and are newer than the r5 images and deployment
qualification in the parent directory; no new image or deployment qualification
is claimed here. Existing working-tree edits and running services were preserved.

## Repairs

**GitHub credentials:** branch discovery used a substring test on an untrusted
remote/origin URL before retrieving the repository owner's stored GitHub token.
A lookalike host, userinfo, path or query could trigger that selection. The
shared helper now requires HTTPS, exactly `github.com`, port 443/default, and no
URL userinfo. Both repository paths use it before looking up credentials, and
the actual remote-list function rejects unsafe token-bearing inputs before
creating a transport. Public non-GitHub remotes still work without these
credentials. Plain HTTP, SSH, userinfo URLs and alternate ports do not receive
an automatically selected GitHub HTTP token; use the canonical HTTPS remote for
that credential type. Existing pinned go-git code strips cross-origin redirect
credentials. This does not close broader source transport or SSRF qualification.

**CLI installation:** the old temporary-directory rename could cross filesystems,
fall back to following/truncating the destination path, and chmod a replaced
path. Installation now opens the destination root, creates an exclusive random
temporary entry on that filesystem, copies the verified binary, changes its mode
through the descriptor, syncs/closes it, and publishes through confined rename.
No copy-to-final fallback remains. Failed copy/rename preserves the old entry;
symlink/hardlink destinations are replaced without changing their targets.
Archive binary sizes outside 1–180 MiB fail before output creation; oversized
HTTP downloads fail explicitly rather than proceeding to checksum verification.
This is atomic publication, not a power-loss recovery certification or complete
agent/home-directory isolation.

The larger Git subprocess audit remains R03.3. The browse endpoint still checks
metadata through an opened root before passing a pathname to Git; the installed
go-billy pathname wrapper also has check/open boundaries, so simply swapping in
that wrapper would not establish rename-safe confinement. Git worktrees, shared
gitdirs, repository configuration, filters/hooks and subprocess limits still
need adversarial qualification.

## Validation

- `go test -race ./internal/fix/... ./internal/api/routes ./internal/scantarget ./internal/repo -count=1`
  passed **24 packages**; [output](race.txt).
- After `make scanners-sync`,
  `go test -race ./internal/fix/git ./internal/scannerbuild ./internal/scannertools/manifest ./internal/scannertools/lock -count=1`
  passed all four packages; **27 distinct packages** passed across the two runs;
  [output](generated-and-git.txt).
- `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run`
  passed with zero issues; [output](lint.txt).
- `git diff --check` and the final generated-context check passed. A redacted
  gitleaks directory scan of this evidence passed; [output](evidence-secrets.txt).
  No UI source changed in this follow-up.
- Locked gosec high/high analysis still **fails with 37 findings**: 30 G703,
  four G702, three G704, with no package-analysis errors. The prior installer
  fallback sink is gone; no suppression was added. [Current report](gosec.json)
  and [all original alerts plus the manually discovered credential issue](security-triage.json).
  A lower count is not independent security sign-off.

The initial credential-selection test lacked its required owner row and failed
a foreign-key constraint; the fixture was corrected and rerun successfully.
Negative transport tests use a local HTTP sentinel so a future regression
cannot send test credentials to external hosts.

Scanner lock:
`sha256:eac85fecdc453c13747ceaf8c1d1604d6005ad79a10fd289e4ab6a30413ec73b`.
Definition:
`sha256:918063d84c38d9e9970f0e3bd360bf89870166cbfc84c8677777209dc722b1ed`.
No dependency, schema, release exception, or runtime policy bypass was added.
