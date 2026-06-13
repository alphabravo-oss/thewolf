# UI/UX Flow Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the structural and surface gaps in the wolf web UI surfaced by the live walkthrough on 2026-06-13: an inverted Repos → Collections flow, an unreachable GitHub source type, half the product missing from the sidebar, and a handful of display bugs.

**Architecture:** Targeted React/TanStack-Router edits in `ui-next/`. Two extraction refactors (the AddRepoForm into its own component, the sidebar nav into a single config array) followed by surgical fixes elsewhere. No backend changes — the API already supports everything; the UI just doesn't call it.

**Tech Stack:** React 18, TanStack Router, TanStack Query, Tailwind, lucide-react, sonner (toasts), Vite. Tests via Vitest + React Testing Library where they exist; UI flow verification via Chrome DevTools MCP for the live demo.

**Source audit:** `docs/superpowers/specs/2026-06-13-ui-ux-walkthrough-findings.md` (informal; this plan is the actionable form).

---

## File Structure

Files this plan creates or changes, with one-line responsibility each.

**New files:**
- `ui-next/src/components/add-repo-form.tsx` — Self-contained source-type-tabbed form: Local / GitHub / Remote git URL / SSH node. Used from both `/repos` and `/collections/$id`.
- `ui-next/src/lib/parse-frameworks.ts` — Tiny JSON-array → `string[]` parser with a graceful fallback for legacy non-JSON payloads.
- `ui-next/src/components/frameworks-chips.tsx` — Renders an array of framework names as inline chip badges.
- `ui-next/src/routes/_authed.audit.tsx` + `_authed.audit.index.tsx` — Read-only admin table of `/api/v1/audit-log` entries.
- `ui-next/src/lib/parse-frameworks.test.ts` — Unit test for the parser.
- `ui-next/src/components/add-repo-form.test.tsx` — Component-level test for source-type tab switching + body payload.

**Modified files:**
- `ui-next/src/components/sidebar.tsx` — Extend the `primary` nav array with Loops, Fixes, Scanners, Audit; keep Settings in `secondary`.
- `ui-next/src/routes/_authed.repos.index.tsx` — Add an "+ Add repo" button that toggles `AddRepoForm`; replace the misleading description copy.
- `ui-next/src/routes/_authed.collections.$collectionId.tsx` — Delete the inline copy of the form and import the shared component, threading `collectionId` so it auto-links after create.
- `ui-next/src/routes/_authed.repos.$repoId.tsx` — Replace the raw-JSON Frameworks `StaticText` with `<FrameworksChips />`.
- `ui-next/src/routes/_authed.scans.$scanId.index.tsx` (or `_authed.scans.$scanId.tsx`, whichever owns the header — verify with grep first) — Add a "No scanners ran" banner when `tools_selected.length === 0 && status === "completed"`.
- `ui-next/src/routes/_authed.scans.index.tsx` — Render `0s` instead of `—` for completed scans whose duration rounds to zero.
- `ui-next/src/lib/api.ts` — Add typed wrappers for `/api/v1/audit-log` if not already present.

**Read-only references (don't modify):**
- `cmd/wolf/main.go` — bootstrap admin (unchanged; UI just consumes the API).
- `internal/scantarget/github.go` — `ParseGitHubSource` validation (UI must keep input acceptable to this).
- `internal/api/routes/repos.go:107-132` — server-side validation order (the UI's `source_type: "github"` must match).
- `internal/api/openapi/spec.go` — endpoint catalog (the new audit page should match `GET /audit-log` shape).

---

## Definition of Done — whole project

The plan is complete only when **all** of the following are true. A run-through against a fresh DB should pass every item.

1. A user signing in for the first time can reach `/repos`, click "+ Add repo", and create a repo of any of these types without leaving the page: **Local path · GitHub · Remote git URL · SSH node**.
2. The GitHub tab sends `source_type: "github"` with `source_path` validated client-side against the same shapes `ParseGitHubSource` accepts (`owner/repo`, `github.com/owner/repo`, full URL, `.git` suffix, SSH form). A bad value surfaces a field-level error before submit.
3. A private-GitHub UX hint reminds the user to create a `github_token` secret if one doesn't exist yet, and links directly to Settings → Secrets.
4. The Collections "Add repo" form uses the same `AddRepoForm` component (no duplicate code). After a successful create, the new repo is linked to the active collection automatically.
5. The sidebar `primary` array contains, in this order: Dashboard · Collections · Repos · Scans · Findings · Fixes · Loops · Scanners · Audit. Each item lights up on its matching path.
6. `/audit` shows the most recent 100 audit-log rows in a paginated table (Method · Path · Status · Actor · When), is admin-only (server enforces; UI calls `/audit-log` and surfaces a polite 403 message if returned).
7. The repo detail page renders Frameworks as chips, not a JSON string. A repo with **no** frameworks shows "—".
8. A completed scan with `0` tools selected (no scanner backend) shows a banner: "No scanners ran — run `wolf doctor` or check the Scanners tab". The misleading "without any issues. Nice." text only appears when at least one tool actually ran.
9. The Scans list Duration column shows `0s` for completed-but-instant scans and a real value otherwise. It never disagrees with the detail page.
10. The Repos page description no longer says "Add new ones from a collection's Add repo form" — it says something accurate ("Source-code targets — local paths, GitHub, remote git URLs, or SSH nodes.").
11. `pnpm --filter ui-next test` passes. `pnpm --filter ui-next build` produces a `dist/` with index.html under 2KB and assets matching the dist served by `wolf serve`.
12. `go build ./...`, `go vet ./...`, `go test ./...` still all green — no backend regressions (this plan should not touch backend code, but verify).
13. The README "API & CLI" section gains a one-line entry for the new UI capabilities (the changelog-like polish).
14. Manual smoke (Chrome DevTools MCP): login → `/repos` → Add repo (GitHub tab, `owner/repo`) → server creates it → user lands on repo detail → Scan now → scan list shows correct duration → Findings page is reachable → sidebar shows all 9 primary items → `/audit` lists the recent mutations.

---

## Task 1: Add a Frameworks JSON parser

**Why first:** smallest unit, independently committable, sets the testing scaffolding pattern the rest of the plan reuses.

**Files:**
- Create: `ui-next/src/lib/parse-frameworks.ts`
- Test: `ui-next/src/lib/parse-frameworks.test.ts`

- [ ] **Step 1: Write the failing test**

```ts
// ui-next/src/lib/parse-frameworks.test.ts
import { describe, expect, it } from "vitest";
import { parseFrameworks } from "./parse-frameworks";

describe("parseFrameworks", () => {
  it("parses a JSON array of strings", () => {
    expect(parseFrameworks(`["react","vite","tailwindcss"]`)).toEqual([
      "react", "vite", "tailwindcss",
    ]);
  });

  it("returns [] for an empty string", () => {
    expect(parseFrameworks("")).toEqual([]);
  });

  it("returns [] for null/undefined", () => {
    expect(parseFrameworks(null as unknown as string)).toEqual([]);
    expect(parseFrameworks(undefined as unknown as string)).toEqual([]);
  });

  it("falls back to comma-split for legacy non-JSON payloads", () => {
    expect(parseFrameworks("react, vite,  tailwindcss")).toEqual([
      "react", "vite", "tailwindcss",
    ]);
  });

  it("ignores non-string entries inside the JSON array", () => {
    expect(parseFrameworks(`["react", 42, null, "vite"]`)).toEqual([
      "react", "vite",
    ]);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm --filter ui-next test parse-frameworks --run`
Expected: FAIL with `Cannot find module './parse-frameworks'`.

- [ ] **Step 3: Implement the parser**

```ts
// ui-next/src/lib/parse-frameworks.ts
// Frameworks come from the API as a JSON-encoded array stored in
// repo.detected_frameworks. Some legacy rows are comma-separated plain
// strings — handle both, fall back to [] on garbage.
export function parseFrameworks(raw: string | null | undefined): string[] {
  if (!raw) return [];
  const trimmed = raw.trim();
  if (!trimmed) return [];

  if (trimmed.startsWith("[")) {
    try {
      const parsed = JSON.parse(trimmed);
      if (Array.isArray(parsed)) {
        return parsed.filter((v): v is string => typeof v === "string");
      }
    } catch {
      // fall through to the comma-split path
    }
  }

  return trimmed.split(",").map((s) => s.trim()).filter(Boolean);
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm --filter ui-next test parse-frameworks --run`
Expected: PASS, all 5 tests green.

- [ ] **Step 5: Commit**

```bash
git add ui-next/src/lib/parse-frameworks.ts ui-next/src/lib/parse-frameworks.test.ts
git commit -m "feat(ui): parse repo.detected_frameworks JSON into string[]"
```

---

## Task 2: Render Frameworks as chips on the repo detail page

**Files:**
- Create: `ui-next/src/components/frameworks-chips.tsx`
- Modify: `ui-next/src/routes/_authed.repos.$repoId.tsx` (the line currently rendering raw `detected_frameworks`)

- [ ] **Step 1: Grep for the current render site**

Run: `grep -n "detected_frameworks\|Frameworks" ui-next/src/routes/_authed.repos.\$repoId.tsx`
Expected: a line near the metadata block reading roughly
`<div>{repo.detected_frameworks}</div>` or similar.

- [ ] **Step 2: Implement the chips component**

```tsx
// ui-next/src/components/frameworks-chips.tsx
import { parseFrameworks } from "@/lib/parse-frameworks";

export function FrameworksChips({ raw }: { raw: string | null | undefined }) {
  const items = parseFrameworks(raw);
  if (items.length === 0) {
    return <span className="text-muted-foreground">—</span>;
  }
  return (
    <div className="flex flex-wrap gap-1.5">
      {items.map((name) => (
        <span
          key={name}
          className="inline-flex h-6 items-center rounded-md border border-border/60 bg-muted/30 px-2 text-xs text-foreground/80"
        >
          {name}
        </span>
      ))}
    </div>
  );
}
```

- [ ] **Step 3: Wire it into the repo detail page**

Find the existing Frameworks render in `_authed.repos.$repoId.tsx` (a line like
`<div>{repo.detected_frameworks}</div>` or a `StaticText` of the raw value)
and replace with:

```tsx
import { FrameworksChips } from "@/components/frameworks-chips";

// …in the metadata grid…
<div>
  <div className="text-xs uppercase tracking-wide text-muted-foreground">
    Frameworks
  </div>
  <FrameworksChips raw={repo.detected_frameworks} />
</div>
```

- [ ] **Step 4: Build + visual check**

Run: `pnpm --filter ui-next build`
Expected: build succeeds, no TS errors.

Then start the server (or hit the running one on `:8779`), open the wolf repo's detail page, confirm chips render instead of `["react", ...]`. Take a screenshot to `.claude/ui-screens/16-frameworks-chips.png` for the record.

- [ ] **Step 5: Commit**

```bash
git add ui-next/src/components/frameworks-chips.tsx ui-next/src/routes/_authed.repos.\$repoId.tsx
git commit -m "fix(ui): render repo frameworks as chips, not raw JSON"
```

---

## Task 3: Extract AddRepoForm into a shared component

This is the load-bearing refactor — every subsequent UI improvement depends on it. Make this one work cleanly before moving on.

**Files:**
- Create: `ui-next/src/components/add-repo-form.tsx`
- Create: `ui-next/src/components/add-repo-form.test.tsx`
- Modify: `ui-next/src/routes/_authed.collections.$collectionId.tsx` (delete the inline form, import the new one)

The exact source lines to delete are roughly `_authed.collections.$collectionId.tsx:455–620` (the `AddRepoForm`-equivalent JSX and its hooks). Verify with grep before deleting.

- [ ] **Step 1: Read the existing form to capture its hooks + props**

Run: `sed -n '400,620p' ui-next/src/routes/_authed.collections.\$collectionId.tsx`
Read carefully: what state hooks it uses, what mutations it calls (`POST /repos`, `POST /collections/{id}/repos`), what props the parent passes (`collectionId`, `onClose`, `eligible` query).

- [ ] **Step 2: Create the shared component**

```tsx
// ui-next/src/components/add-repo-form.tsx
// One source-type-tabbed form for creating repositories. Used standalone
// from the Repos page, and threaded through a collection when called from
// the collection detail page (sets collectionId so the new repo is
// auto-linked).
import { useState } from "react";
import { XIcon } from "lucide-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Link } from "@tanstack/react-router";
import { api } from "@/lib/api";

type Mode = "local" | "github" | "git" | "ssh";

export type AddRepoFormProps = {
  /** When set, the form attaches the new repo to this collection on success. */
  collectionId?: string;
  /** Called after a successful create (or on Close). Parents typically navigate or refetch. */
  onDone: (repoId: string | null) => void;
};

// Mirrors internal/scantarget/github.go ParseGitHubSource. Kept lenient: the
// server is the source of truth, but failing client-side avoids a round trip
// for obvious typos.
function isLikelyGitHubSource(s: string): boolean {
  const v = s.trim()
    .replace(/^https?:\/\/github\.com\//, "")
    .replace(/^github\.com\//, "")
    .replace(/^git@github\.com:/, "")
    .replace(/\.git$/, "")
    .replace(/^\/+|\/+$/g, "");
  const parts = v.split("/");
  return parts.length === 2 && parts.every((p) => p.length > 0 && !/[\s@:]/.test(p));
}

export function AddRepoForm({ collectionId, onDone }: AddRepoFormProps) {
  const [mode, setMode] = useState<Mode>("local");
  const [name, setName] = useState("");
  const [sourcePath, setSourcePath] = useState("");
  const [branch, setBranch] = useState("main");
  const [remoteNodeId, setRemoteNodeId] = useState("");
  const qc = useQueryClient();

  const create = useMutation({
    mutationFn: async () => {
      const body: Record<string, unknown> = {
        name: name.trim(),
        source_type: mode === "git" ? "git" : mode,
        source_path: sourcePath.trim(),
        default_branch: branch.trim() || "main",
      };
      if (mode === "ssh") body.remote_node_id = remoteNodeId;
      const { data } = await api.post<{ data: { id: string } }>("/repos", body);
      const repoId = data.data.id;
      if (collectionId) {
        await api.post(`/collections/${collectionId}/repos`, { repo_id: repoId });
      }
      return repoId;
    },
    onSuccess: (repoId) => {
      qc.invalidateQueries({ queryKey: ["repos"] });
      if (collectionId) {
        qc.invalidateQueries({ queryKey: ["collection", collectionId] });
      }
      toast.success("Repository created");
      onDone(repoId);
    },
    onError: (e) => {
      const msg = e instanceof Error ? e.message : "Create failed";
      toast.error(msg);
    },
  });

  const submitDisabled =
    !name.trim() ||
    !sourcePath.trim() ||
    (mode === "github" && !isLikelyGitHubSource(sourcePath)) ||
    (mode === "ssh" && !remoteNodeId) ||
    create.isPending;

  return (
    <div className="border border-border/40 rounded-lg p-3 mb-4 space-y-3 bg-muted/10">
      <div className="flex items-center justify-between">
        <div className="text-xs text-muted-foreground">
          {collectionId ? "Add a repo to this collection" : "Add a repository"}
        </div>
        <button
          type="button"
          onClick={() => onDone(null)}
          className="size-7 grid place-items-center rounded hover:bg-muted/40"
          aria-label="Close"
        >
          <XIcon className="size-4" />
        </button>
      </div>

      <div className="flex gap-1 text-xs">
        {(
          [
            ["local", "Local path"],
            ["github", "GitHub"],
            ["git", "Remote git URL"],
            ["ssh", "SSH node"],
          ] as const
        ).map(([m, label]) => (
          <button
            key={m}
            type="button"
            onClick={() => setMode(m)}
            className={`h-7 px-2.5 rounded-md transition ${
              mode === m
                ? "bg-primary text-primary-foreground"
                : "hover:bg-muted/40 text-muted-foreground"
            }`}
            aria-pressed={mode === m}
          >
            {label}
          </button>
        ))}
      </div>

      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!submitDisabled) create.mutate();
        }}
        className="space-y-3"
      >
        <Field label="Name">
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
            placeholder={mode === "github" ? "owner/repo" : "my-project"}
            className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm"
          />
        </Field>

        {mode === "local" && (
          <Field
            label="Absolute path on host"
            hint="Must be under a path Docker can bind-mount (typically anywhere under /Users on macOS)."
          >
            <input
              value={sourcePath}
              onChange={(e) => setSourcePath(e.target.value)}
              required
              placeholder="/Users/me/code/my-project"
              className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm font-mono"
            />
          </Field>
        )}

        {mode === "github" && (
          <>
            <Field
              label="GitHub repo"
              hint='Accepts owner/repo, github.com/owner/repo, or a full https://github.com/... URL.'
            >
              <input
                value={sourcePath}
                onChange={(e) => setSourcePath(e.target.value)}
                required
                placeholder="alphabravo-oss/thewolf"
                className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm font-mono"
              />
            </Field>
            <div className="text-xs text-muted-foreground">
              Private repository? Add a{" "}
              <Link
                to="/settings"
                search={{ tab: "secrets" }}
                className="underline underline-offset-2"
              >
                github_token secret
              </Link>{" "}
              and it'll be used automatically.
            </div>
          </>
        )}

        {mode === "git" && (
          <Field label="Git URL">
            <input
              value={sourcePath}
              onChange={(e) => setSourcePath(e.target.value)}
              required
              placeholder="https://gitlab.example.com/team/repo.git"
              className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm font-mono"
            />
          </Field>
        )}

        {mode === "ssh" && (
          <>
            <Field label="SSH node">
              <NodePicker value={remoteNodeId} onChange={setRemoteNodeId} />
            </Field>
            <Field label="Absolute remote path">
              <input
                value={sourcePath}
                onChange={(e) => setSourcePath(e.target.value)}
                required
                placeholder="/srv/code/my-project"
                className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm font-mono"
              />
            </Field>
          </>
        )}

        <Field label="Default branch">
          <input
            value={branch}
            onChange={(e) => setBranch(e.target.value)}
            placeholder="main"
            className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm"
          />
        </Field>

        <div className="flex justify-end">
          <button
            type="submit"
            disabled={submitDisabled}
            className="h-8 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-50"
          >
            {create.isPending ? "Creating…" : collectionId ? "Create + add" : "Create"}
          </button>
        </div>
      </form>
    </div>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block space-y-1">
      <div className="text-xs text-muted-foreground">{label}</div>
      {children}
      {hint && <div className="text-xs text-muted-foreground/80">{hint}</div>}
    </label>
  );
}

function NodePicker({
  value,
  onChange,
}: {
  value: string;
  onChange: (id: string) => void;
}) {
  // Fetched lazily so we don't pay for it when the user is on a non-SSH tab.
  const nodes = useQuery({
    queryKey: ["nodes"],
    queryFn: async () => {
      const { data } = await api.get<{ data: Array<{ id: string; name: string; host: string }> }>(
        "/nodes",
      );
      return data.data ?? [];
    },
  });
  if (nodes.isLoading) {
    return (
      <select
        disabled
        className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm opacity-60"
      >
        <option>Loading nodes…</option>
      </select>
    );
  }
  if ((nodes.data ?? []).length === 0) {
    return (
      <div className="text-xs text-muted-foreground">
        No SSH nodes configured.{" "}
        <Link to="/settings" search={{ tab: "nodes" }} className="underline underline-offset-2">
          Add one in Settings → Nodes
        </Link>
        .
      </div>
    );
  }
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      required
      className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm"
    >
      <option value="">Pick a node…</option>
      {nodes.data!.map((n) => (
        <option key={n.id} value={n.id}>
          {n.name} ({n.host})
        </option>
      ))}
    </select>
  );
}
```

- [ ] **Step 3: Write the component test**

```tsx
// ui-next/src/components/add-repo-form.test.tsx
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { AddRepoForm } from "./add-repo-form";

vi.mock("@/lib/api", () => ({
  api: {
    post: vi.fn().mockResolvedValue({ data: { data: { id: "new-repo-1" } } }),
  },
}));

function renderWithClient(ui: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

describe("AddRepoForm", () => {
  it("defaults to Local path mode", () => {
    renderWithClient(<AddRepoForm onDone={() => {}} />);
    expect(screen.getByText("Local path")).toBeInTheDocument();
    expect(screen.getByPlaceholderText(/Users\/me/)).toBeInTheDocument();
  });

  it("switches to GitHub mode and shows the secret hint", () => {
    renderWithClient(<AddRepoForm onDone={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "GitHub" }));
    expect(screen.getByPlaceholderText("alphabravo-oss/thewolf")).toBeInTheDocument();
    expect(screen.getByText(/github_token secret/i)).toBeInTheDocument();
  });

  it("disables submit when GitHub source is malformed", () => {
    renderWithClient(<AddRepoForm onDone={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "GitHub" }));
    fireEvent.change(screen.getByPlaceholderText(/owner\/repo/i), {
      target: { value: "not a github source" },
    });
    fireEvent.change(screen.getByPlaceholderText("my-project"), {
      target: { value: "test" },
    });
    expect(screen.getByRole("button", { name: /Create/i })).toBeDisabled();
  });
});
```

- [ ] **Step 4: Run tests; expect them to pass**

Run: `pnpm --filter ui-next test add-repo-form --run`
Expected: PASS, 3 tests green.

- [ ] **Step 5: Swap the collection page to use the shared component**

In `ui-next/src/routes/_authed.collections.$collectionId.tsx`:
1. Delete the inline `AddRepoForm` (the JSX block previously at ~lines 455-620 and its supporting hooks).
2. Add `import { AddRepoForm } from "@/components/add-repo-form";` at the top.
3. Replace the rendered form with:

```tsx
{showAddRepo && (
  <AddRepoForm
    collectionId={collectionId}
    onDone={(repoId) => {
      setShowAddRepo(false);
      // Toast already fired inside the component on success; nothing else needed.
    }}
  />
)}
```

- [ ] **Step 6: Build + verify Collections still works**

Run: `pnpm --filter ui-next build`
Expected: clean build, no TS errors.

Then in the browser: navigate to an existing collection, click Add repo, switch to GitHub mode, create `alphabravo-oss/thewolf`. Confirm it lands as a repo of `source_type: "github"` and is linked to the collection.

- [ ] **Step 7: Commit**

```bash
git add ui-next/src/components/add-repo-form.tsx \
        ui-next/src/components/add-repo-form.test.tsx \
        ui-next/src/routes/_authed.collections.\$collectionId.tsx
git commit -m "refactor(ui): extract AddRepoForm into a shared component, add GitHub tab"
```

---

## Task 4: Mount AddRepoForm directly on the Repos page

**Files:**
- Modify: `ui-next/src/routes/_authed.repos.index.tsx`

- [ ] **Step 1: Read the current Repos page**

Run: `cat ui-next/src/routes/_authed.repos.index.tsx`
Note the heading block and the description sentence currently misleading users.

- [ ] **Step 2: Replace the page with the new flow**

The structure: header with `+ Add repo` button on the right, the form shown when toggled, the existing list below. Description copy changes too.

```tsx
// at the top, add:
import { useState } from "react";
import { PlusIcon } from "lucide-react";
import { AddRepoForm } from "@/components/add-repo-form";

// inside the component:
const [showAdd, setShowAdd] = useState(false);

// then the JSX:
<div className="flex items-start justify-between gap-4 mb-4">
  <div>
    <h1 className="text-2xl font-semibold tracking-tight">Repositories</h1>
    <p className="text-sm text-muted-foreground mt-1">
      Source-code targets — local paths, GitHub, remote git URLs, or SSH nodes.
    </p>
  </div>
  <button
    type="button"
    onClick={() => setShowAdd((v) => !v)}
    className="inline-flex h-9 items-center gap-1.5 rounded-md bg-primary px-3 text-sm font-medium text-primary-foreground hover:opacity-90"
  >
    <PlusIcon className="size-4" />
    Add repo
  </button>
</div>

{showAdd && (
  <AddRepoForm
    onDone={(repoId) => {
      setShowAdd(false);
      // The component already invalidates the repos query; the list refetches.
      // If the parent wants to navigate to the new repo immediately, do it here.
      if (repoId) {
        // optional: navigate({ to: `/repos/${repoId}` })
      }
    }}
  />
)}
```

Keep the empty state, but update its CTA:

```tsx
// Empty state:
<EmptyState
  icon={<GitForkIcon className="size-6" />}
  title="No repositories yet"
  body="Add a local path, GitHub repo, or SSH-accessible tree to get started."
  cta={
    <button
      type="button"
      onClick={() => setShowAdd(true)}
      className="inline-flex h-9 items-center gap-1.5 rounded-md bg-primary px-3 text-sm font-medium text-primary-foreground hover:opacity-90"
    >
      <PlusIcon className="size-4" />
      Add repo
    </button>
  }
/>
```

- [ ] **Step 3: Build + click-through**

Run: `pnpm --filter ui-next build`
Open `http://127.0.0.1:8779/repos`, click "Add repo", create a repo from each tab, confirm it appears in the list without leaving the page.

- [ ] **Step 4: Commit**

```bash
git add ui-next/src/routes/_authed.repos.index.tsx
git commit -m "feat(ui): create repos directly from the Repos page"
```

---

## Task 5: Extend the sidebar nav to cover all working routes

**Files:**
- Modify: `ui-next/src/components/sidebar.tsx`

- [ ] **Step 1: Inspect the current nav array**

Run: `sed -n '20,45p' ui-next/src/components/sidebar.tsx`
You should see `const primary: NavItem[] = [ Dashboard, Collections, Repos, Scans, Findings ]`.

- [ ] **Step 2: Extend the imports + array**

Replace the lucide-react import block to include the new icons:

```tsx
import {
  LayoutDashboardIcon,
  PackageIcon,
  GitForkIcon,
  BugIcon,
  SettingsIcon,
  GaugeIcon,
  LogOutIcon,
  MenuIcon,
  XIcon,
  WrenchIcon,
  RepeatIcon,
  ContainerIcon,
  ScrollTextIcon,
} from "lucide-react";
```

Then replace the `primary` array with:

```tsx
const primary: NavItem[] = [
  { label: "Dashboard", to: "/", icon: LayoutDashboardIcon },
  { label: "Collections", to: "/collections", icon: PackageIcon },
  { label: "Repos", to: "/repos", icon: GitForkIcon },
  { label: "Scans", to: "/scans", icon: GaugeIcon },
  { label: "Findings", to: "/findings", icon: BugIcon },
  { label: "Fixes", to: "/fixes", icon: WrenchIcon },
  { label: "Loops", to: "/loops", icon: RepeatIcon },
  { label: "Scanners", to: "/scanners", icon: ContainerIcon },
  { label: "Audit", to: "/audit", icon: ScrollTextIcon },
];
```

The Audit entry's route is created in Task 6 — it'll 404 until then, that's expected.

- [ ] **Step 3: Build + verify sidebar visually**

Run: `pnpm --filter ui-next build`
Open the app, confirm the sidebar now shows 9 primary items, that clicking Fixes/Loops/Scanners lands on existing pages (they exist in `ui-next/src/routes/`).

- [ ] **Step 4: Commit**

```bash
git add ui-next/src/components/sidebar.tsx
git commit -m "feat(ui): surface Fixes, Loops, Scanners, Audit in the sidebar"
```

---

## Task 6: Add the Audit log page

**Files:**
- Create: `ui-next/src/routes/_authed.audit.tsx` (route group wrapper)
- Create: `ui-next/src/routes/_authed.audit.index.tsx` (the actual page)

- [ ] **Step 1: Inspect a sibling route for the wrapper boilerplate**

Run: `cat ui-next/src/routes/_authed.findings.tsx`
This is the minimal pass-through wrapper TanStack Router uses for `/findings/*`. Copy its shape for `_authed.audit.tsx`.

- [ ] **Step 2: Create the wrapper**

```tsx
// ui-next/src/routes/_authed.audit.tsx
import { Outlet, createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/_authed/audit")({
  component: () => <Outlet />,
});
```

- [ ] **Step 3: Create the page**

```tsx
// ui-next/src/routes/_authed.audit.index.tsx
import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { api } from "@/lib/api";
import { EmptyState } from "@/components/empty-state";
import { ScrollTextIcon } from "lucide-react";

type AuditEntry = {
  id: string;
  method: string;
  path: string;
  status_code: number;
  user_id: string;
  token_id?: string;
  resource_id?: string;
  action: string;
  created_at: string;
};

export const Route = createFileRoute("/_authed/audit/")({
  component: AuditPage,
});

function AuditPage() {
  const q = useQuery({
    queryKey: ["audit-log"],
    queryFn: async () => {
      const { data } = await api.get<{ data: AuditEntry[] }>(
        "/audit-log?limit=100",
      );
      return data.data ?? [];
    },
  });

  if (q.isLoading) {
    return <div className="text-sm text-muted-foreground">Loading…</div>;
  }
  if (q.isError) {
    const msg = (q.error as Error).message;
    if (/403|forbidden/i.test(msg)) {
      return (
        <EmptyState
          icon={<ScrollTextIcon className="size-6" />}
          title="Admin only"
          body="The audit log is only readable by users with the admin scope."
        />
      );
    }
    return <div className="text-sm text-destructive">{msg}</div>;
  }
  const rows = q.data ?? [];
  if (rows.length === 0) {
    return (
      <EmptyState
        icon={<ScrollTextIcon className="size-6" />}
        title="No audit entries yet"
        body="Mutating API requests will land here once anyone changes anything."
      />
    );
  }

  return (
    <div>
      <div className="mb-4">
        <h1 className="text-2xl font-semibold tracking-tight">Audit log</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Every mutating API request — POST, PUT, DELETE — with the actor, the
          resource, and the response status. Most recent 100.
        </p>
      </div>
      <div className="overflow-x-auto rounded-md border border-border/60">
        <table className="w-full text-sm">
          <thead className="bg-muted/30 text-xs uppercase text-muted-foreground">
            <tr>
              <th className="text-left px-3 py-2">Method</th>
              <th className="text-left px-3 py-2">Path</th>
              <th className="text-left px-3 py-2">Status</th>
              <th className="text-left px-3 py-2">Actor</th>
              <th className="text-left px-3 py-2">When</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.id} className="border-t border-border/40">
                <td className="px-3 py-2 font-mono text-xs">{r.method}</td>
                <td className="px-3 py-2 font-mono text-xs truncate max-w-[40ch]">
                  {r.path}
                </td>
                <td className="px-3 py-2">
                  <StatusPill code={r.status_code} />
                </td>
                <td className="px-3 py-2 text-xs">
                  {r.token_id ? `token:${r.token_id.slice(0, 8)}…` : `user:${r.user_id.slice(0, 8)}…`}
                </td>
                <td className="px-3 py-2 text-xs text-muted-foreground">
                  {new Date(r.created_at).toLocaleString()}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function StatusPill({ code }: { code: number }) {
  const tone =
    code >= 500
      ? "bg-destructive/15 text-destructive"
      : code >= 400
        ? "bg-amber-500/15 text-amber-400"
        : code >= 300
          ? "bg-sky-500/15 text-sky-400"
          : "bg-emerald-500/15 text-emerald-400";
  return (
    <span className={`inline-flex h-5 items-center rounded px-1.5 text-xs ${tone}`}>
      {code}
    </span>
  );
}
```

- [ ] **Step 4: Regenerate the route tree if needed**

If `ui-next` uses `@tanstack/router-plugin` (it does — see `vite.config.ts`), the tree regenerates on file save. If running headless, run:

`pnpm --filter ui-next exec tsr generate` (or whatever the project's generate command is — check `package.json` scripts).
Expected: `ui-next/src/routeTree.gen.ts` updates with the new route.

- [ ] **Step 5: Build + visit /audit**

Run: `pnpm --filter ui-next build`
Open `http://127.0.0.1:8779/audit`, confirm the table renders (likely empty if you just started, but it should be populated after the earlier "create collection" + "add repo" + "create scan" mutations).

- [ ] **Step 6: Commit**

```bash
git add ui-next/src/routes/_authed.audit.tsx \
        ui-next/src/routes/_authed.audit.index.tsx \
        ui-next/src/routeTree.gen.ts
git commit -m "feat(ui): add /audit page surfacing the audit log table"
```

---

## Task 7: "No scanners ran" banner on scan detail

**Files:**
- Modify: whichever of `_authed.scans.$scanId.tsx`, `_authed.scans.$scanId.index.tsx`, or `_authed.scans.$scanId.live.tsx` renders the header block. Grep first.

- [ ] **Step 1: Locate the header**

Run: `grep -rn "tools completed\|No findings\|This scan completed" ui-next/src/routes/_authed.scans.\$scanId*.tsx`
Find the line currently rendering "This scan completed without any issues. Nice." and the surrounding scan-status header.

- [ ] **Step 2: Add the banner above the findings block**

```tsx
// Add near the top of the scan detail view, after the header summary:
{scan.status === "completed" && toolsSelectedCount(scan) === 0 && (
  <div className="mb-4 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-200">
    <div className="font-medium">No scanners ran</div>
    <div className="text-xs text-amber-200/80 mt-0.5">
      This scan completed without running any tools. The container scanner
      backend may not be configured — try{" "}
      <code className="font-mono text-xs">wolf doctor</code> from the CLI, or
      open the Scanners tab to install missing tools.
    </div>
  </div>
)}
```

Add the helper at module scope:

```tsx
function toolsSelectedCount(scan: { tools_selected?: string }): number {
  if (!scan.tools_selected) return 0;
  try {
    const arr = JSON.parse(scan.tools_selected);
    return Array.isArray(arr) ? arr.length : 0;
  } catch {
    return 0;
  }
}
```

- [ ] **Step 3: Guard the misleading no-issues message**

Find:
```tsx
<div>This scan completed without any issues. Nice.</div>
```
and change to:
```tsx
{toolsSelectedCount(scan) > 0 && (
  <div>This scan completed without any issues. Nice.</div>
)}
```

- [ ] **Step 4: Build + spot-check**

Run: `pnpm --filter ui-next build`
Open a scan whose `tools_selected` is `[]` (the one created during the walkthrough) and confirm:
- The amber banner appears.
- The "without any issues. Nice." line is gone.

For a scan that did run tools and found nothing, "Nice." should still show. (Hard to validate without the scanner backend — verify by editing a row's `tools_selected` directly in SQLite if needed for the test, or trust the conditional.)

- [ ] **Step 5: Commit**

```bash
git add ui-next/src/routes/_authed.scans.\$scanId*.tsx
git commit -m "fix(ui): banner when a completed scan ran zero scanners"
```

---

## Task 8: Duration consistency on the Scans list

**Files:**
- Modify: `ui-next/src/routes/_authed.scans.index.tsx`

- [ ] **Step 1: Find the duration render site**

Run: `grep -nE "duration|—|emdash|Duration" ui-next/src/routes/_authed.scans.index.tsx`
The row's Duration cell almost certainly does something like
`{durationText ?? "—"}` or `{startedAt && completedAt ? ... : "—"}`.

- [ ] **Step 2: Replace the rendering with a small helper**

```tsx
// Above the component:
function formatDuration(started?: string | null, completed?: string | null): string {
  if (!started || !completed) return "—";
  const ms = new Date(completed).getTime() - new Date(started).getTime();
  if (Number.isNaN(ms) || ms < 0) return "—";
  if (ms < 1000) return "0s";
  if (ms < 60_000) return `${Math.round(ms / 1000)}s`;
  const m = Math.floor(ms / 60_000);
  const s = Math.round((ms % 60_000) / 1000);
  return `${m}m ${s}s`;
}
```

Then in the table row:
```tsx
<td className="px-3 py-2 text-xs">
  {formatDuration(scan.started_at, scan.completed_at)}
</td>
```

- [ ] **Step 3: Build + verify both states**

Run: `pnpm --filter ui-next build`
Visit `/scans`:
- A completed-instant scan should show `0s`, not `—`.
- A running scan should still show `—`.

- [ ] **Step 4: Commit**

```bash
git add ui-next/src/routes/_authed.scans.index.tsx
git commit -m "fix(ui): show 0s for instant scans in the Scans list Duration column"
```

---

## Task 9: README + smoke verification

**Files:**
- Modify: `README.md` (the "API & CLI" section — note the new UI capabilities)

- [ ] **Step 1: Document the new UI affordances**

In the "API & CLI" section, after the CLI examples, add:

```markdown
### Web UI

The Settings → "AI features" toggle is the master switch (defaults off).
The Repositories page can create a repo of any source type — local path,
GitHub (use a `github_token` secret for private repos), remote git URL,
or SSH node. Loops, Fixes, Scanners, and the admin Audit log all have
dedicated nav entries.
```

- [ ] **Step 2: Run the full smoke**

In a single shell:

```bash
# kill any prior dev server
lsof -ti :8779 | xargs -r kill ; sleep 1

# rebuild UI dist
pnpm --filter ui-next build

# rebuild wolf binary
go build -o /tmp/wolf-local ./cmd/wolf

# fresh DB
rm -rf /tmp/wlocal && mkdir -p /tmp/wlocal/{data,browse}
set -a; . .env; set +a
WOLF_DB_DSN=/tmp/wlocal/data/wolf.db WOLF_BROWSE_ROOTS=/tmp/wlocal/browse \
  /tmp/wolf-local serve --bind 127.0.0.1:8779 --skip-scan-init >/tmp/wlocal/serve.log 2>&1 &
sleep 3

# verify health
curl -sf http://127.0.0.1:8779/api/v1/health >/dev/null && echo "OK"
```

- [ ] **Step 3: Walk the flow in a browser**

Sign in with `admin@wolf.local` / the password from `.env`. Then:
1. `/repos` → "+ Add repo" → GitHub tab → enter `alphabravo-oss/thewolf` → submit → land on detail page with `source_type: "github"`.
2. Click Scan now → confirm the "No scanners ran" banner appears on the completed scan.
3. Back to `/scans` → confirm Duration shows `0s`.
4. Sidebar → click each of Loops, Fixes, Scanners, Audit → each loads its page.
5. `/audit` → see the create-repo and create-scan rows in the table.

- [ ] **Step 4: Final commit + push**

```bash
go test ./...
pnpm --filter ui-next test --run
git add README.md
git commit -m "docs(README): note the new UI affordances"
git push origin main
```

---

## Risk register + rollback

- **Sidebar grows tall on small viewports.** Mitigation: the sidebar already has a mobile drawer. The 4 new items don't push past 900px viewport height. If shorter screens are common, group Loops/Fixes under a "Run" header in a follow-up.
- **`/audit` is admin-only — non-admin testers won't see anything.** That's intended; the page itself renders the 403 message cleanly per Task 6 Step 3.
- **The AddRepoForm tests use a Vitest mock for `@/lib/api`** — if `lib/api.ts` is restructured later, the mock module path may need updating. The component is otherwise self-contained.
- **Rollback** for any task is a single `git revert` of its commit. Tasks 3–4 are the riskiest (delete + replace flow); if a regression surfaces, revert both and the collection-detail page reverts to its pre-refactor inline form.

## Out of scope — explicit deferral

The audit found **four** API surfaces that have no UI route at all today:

- **Baselines** (`/api/v1/repos/{id}/baselines` + `/scans/{id}/diff`) — needs a "Baselines" tab on the repo detail page and a "Baseline" column / diff toggle on Findings.
- **Quality gates / policies** (`/api/v1/policies`, `/scans/{id}/gate`) — needs a Policies admin page and a gate-result panel on the scan detail.
- **Suppressions** (`/api/v1/suppressions`) — needs a global Suppressions list page plus a "Suppress" action from each finding row.
- **SARIF import/export** (`/api/v1/sarif/import`, `/scans/{id}/sarif`) — needs an upload flow + a download button on scan detail.

Each is a multi-task addition with its own data model wiring and design
decisions (where do baselines live in the IA — under repo, or under scans?
should suppressions be a sidebar item or a finding-row action?). Bundling
them into this plan would dilute the focus. They get their own follow-up
spec + plan: `docs/superpowers/specs/2026-06-14-scan-quality-ui-design.md`.

This plan deliberately covers the **flow-blocking** issues (Repos page,
GitHub source, sidebar completeness for already-implemented routes) and the
**display correctness** issues (frameworks, banner, duration). Everything
else is functional but invisible — a separate, larger problem.

## Verification gates

Before declaring the plan done, run all of:

```bash
go vet ./...                               # backend untouched, must stay clean
go test ./...                              # backend unaffected
pnpm --filter ui-next test --run           # UI unit + component tests
pnpm --filter ui-next build                # produces dist/
# Manual: live walkthrough per the smoke in Task 9 Step 3.
```

Plan is done when every Definition-of-Done item above is checkable and the smoke ends with a green `/audit` table reflecting the just-performed mutations.
