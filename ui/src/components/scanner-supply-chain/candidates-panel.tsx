import { memo, useId, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeftIcon,
  BanIcon,
  CheckCircle2Icon,
  ExternalLinkIcon,
  GitCommitHorizontalIcon,
  Loader2Icon,
  RefreshCcwIcon,
  RocketIcon,
  XCircleIcon,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ActionDialog } from "./action-dialog";
import { ArtifactDiffViewer } from "./artifact-diff-viewer";
import { StructuredLogViewer } from "./history";
import { useScannerEvents } from "./use-events";
import { useScannerReleaseCapabilities } from "./capabilities";
import {
  CodeValue,
  PageHeading,
  PanelHeading,
  ResourceState,
  RiskBadge,
  StatusBadge,
  Timestamp,
  humanize,
} from "./primitives";
import {
  parseJson,
  scannerSupplyChainApi,
  type CandidateDetail,
  type CandidateExceptionInput,
  type CandidateSelectionSummary,
  type ComparisonDelta,
  type GateEvidence,
  type RiskSummary,
  type VerificationSummary,
} from "@/lib/scanner-supply-chain";
import { CursorNavigation } from "./cursor-navigation";
import {
  safeBackendFailureMessage,
  safeDisplayText,
  safeErrorMessage,
  safeEvidenceHref,
} from "@/lib/safe-display";

type CandidateAction = "retry" | "cancel" | "approve" | "reject" | "publish";

export const CandidatesPanel = memo(function CandidatesPanel({
  candidateId,
  cursor,
  state: controlledState,
  onStateChange,
  onCursorChange = () => undefined,
  onSelectCandidate,
}: {
  candidateId?: string;
  cursor?: string;
  state?: string;
  onStateChange?: (state: string) => void;
  onCursorChange?: (cursor?: string) => void;
  onSelectCandidate: (id?: string) => void;
}) {
  const [localState, setLocalState] = useState("");
  const state = controlledState ?? localState;
  const changeState = onStateChange ?? setLocalState;
  if (candidateId) {
    return (
      <CandidateDetailView
        candidateId={candidateId}
        onBack={() => onSelectCandidate(undefined)}
      />
    );
  }
  return (
    <CandidateList
      cursor={cursor}
      state={state}
      onStateChange={changeState}
      onCursorChange={onCursorChange}
      onSelectCandidate={onSelectCandidate}
    />
  );
});

function CandidateList({
  cursor,
  state,
  onStateChange,
  onCursorChange,
  onSelectCandidate,
}: {
  cursor?: string;
  state: string;
  onStateChange: (state: string) => void;
  onCursorChange: (cursor?: string) => void;
  onSelectCandidate: (id: string) => void;
}) {
  const candidates = useQuery({
    queryKey: ["scanner-supply-chain", "candidates", state, cursor],
    queryFn: () =>
      scannerSupplyChainApi.candidates({ state, cursor, limit: 100 }),
    placeholderData: (previous) => previous,
  });
  const items = candidates.data?.items ?? [];

  return (
    <div className="space-y-5">
      <PageHeading
        title="Release candidates"
        description="Review deterministic lock changes, build evidence, regression comparisons, and approval state before publication."
        actions={
          <select
            value={state}
            onChange={(event) => {
              onStateChange(event.target.value);
              onCursorChange(undefined);
            }}
            aria-label="Filter candidates by state"
            className="h-10 rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <option value="">All states</option>
            <option value="awaiting_approval">Awaiting approval</option>
            <option value="building">Building</option>
            <option value="failed">Failed</option>
            <option value="approved">Approved</option>
            <option value="published">Published</option>
            <option value="rejected">Rejected</option>
          </select>
        }
      />
      <ResourceState
        loading={candidates.isPending}
        error={candidates.error}
        empty={items.length === 0}
        emptyTitle="No release candidates"
        emptyDescription="Select discovered updates or create a complete candidate from the Overview page."
        onRetry={() => candidates.refetch()}
      >
        <div className="overflow-hidden rounded-lg border border-border/70 bg-card">
          <div
            className="overflow-x-auto focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
            role="region"
            tabIndex={0}
            aria-label="Scanner candidate inventory"
          >
            <table className="w-full min-w-[58rem] text-sm">
              <thead className="bg-muted/20 text-left text-xs text-muted-foreground">
                <tr>
                  <th className="px-4 py-2 font-medium">Candidate</th>
                  <th className="px-4 py-2 font-medium">State</th>
                  <th className="px-4 py-2 font-medium">Risk</th>
                  <th className="px-4 py-2 font-medium">Definition</th>
                  <th className="px-4 py-2 font-medium">Policy</th>
                  <th className="px-4 py-2 font-medium">Updated</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border/50">
                {items.map((candidate) => {
                  const riskSummary = parseJson<RiskSummary>(
                    candidate.risk_summary,
                    {},
                  );
                  const freshness = candidateFreshnessPresentation(
                    candidate.selection,
                  );
                  return (
                    <tr
                      key={candidate.id}
                      className="cursor-pointer [content-visibility:auto] [contain-intrinsic-size:0_64px] hover:bg-muted/15"
                      tabIndex={0}
                      onClick={() => onSelectCandidate(candidate.id)}
                      onKeyDown={(event) => {
                        if (event.key === "Enter" || event.key === " ") {
                          event.preventDefault();
                          onSelectCandidate(candidate.id);
                        }
                      }}
                      aria-label={`Open candidate ${candidate.id}`}
                    >
                      <td className="px-4 py-3">
                        <p className="font-mono text-xs">{candidate.id}</p>
                        {candidate.error_class === "no_changes" ? (
                          <p className="mt-1 max-w-80 truncate text-xs text-emerald-300">
                            Already current; no build was required.
                          </p>
                        ) : candidate.error_detail ? (
                          <p className="mt-1 max-w-80 truncate text-xs text-red-300">
                            {safeBackendFailureMessage(
                              candidate.error_class,
                              "Candidate processing did not complete. Review bounded evidence before retrying.",
                            )}
                          </p>
                        ) : freshness ? (
                          <p className="mt-1 max-w-96 text-xs text-muted-foreground">
                            <span className="mr-1 rounded border border-border/60 bg-muted/40 px-1.5 py-0.5 font-medium text-foreground">
                              {freshness.label}
                            </span>
                            {freshness.shortDescription}
                          </p>
                        ) : null}
                      </td>
                      <td className="px-4 py-3">
                        <StatusBadge
                          state={
                            candidate.error_class === "no_changes"
                              ? "current"
                              : candidate.state
                          }
                        />
                      </td>
                      <td className="px-4 py-3">
                        <RiskBadge
                          risk={
                            candidate.risk ??
                            riskSummary.highest_risk ??
                            riskSummary.risk
                          }
                        />
                      </td>
                      <td className="px-4 py-3">
                        <CodeValue title={candidate.definition_commit}>
                          {candidate.definition_commit.slice(0, 12)}
                        </CodeValue>
                      </td>
                      <td className="px-4 py-3 text-xs">
                        Revision {candidate.policy_revision}
                        {candidate.policy_decision
                          ? ` · ${humanize(candidate.policy_decision)}`
                          : ""}
                      </td>
                      <td className="px-4 py-3 text-xs text-muted-foreground">
                        <Timestamp value={candidate.updated_at} />
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          <CursorNavigation
            currentCursor={cursor}
            nextCursor={candidates.data?.next_cursor}
            loading={candidates.isFetching}
            label="Candidate history"
            onCursorChange={onCursorChange}
          />
        </div>
      </ResourceState>
    </div>
  );
}

function CandidateDetailView({
  candidateId,
  onBack,
}: {
  candidateId: string;
  onBack: () => void;
}) {
  const { capabilities, permissions } = useScannerReleaseCapabilities();
  const queryClient = useQueryClient();
  const [action, setAction] = useState<CandidateAction>();
  const [exceptionGate, setExceptionGate] = useState<GateEvidence>();
  const candidate = useQuery({
    queryKey: ["scanner-supply-chain", "candidate", candidateId],
    queryFn: () => scannerSupplyChainApi.candidate(candidateId),
    refetchInterval: (query) => {
      const state = query.state.data?.state;
      return state &&
        ["queued", "building", "testing", "publishing"].includes(state)
        ? 5_000
        : false;
    },
  });
  const candidateTerminal = Boolean(
    candidate.data &&
    ["published", "rejected", "failed"].includes(candidate.data.state),
  );
  const eventStream = useScannerEvents(
    "candidate",
    candidateId,
    candidateTerminal,
  );

  const mutateAction = useMutation({
    mutationFn: ({
      selectedAction,
      reason,
    }: {
      selectedAction: CandidateAction;
      reason: string;
    }) => {
      const requiresOperate =
        selectedAction === "retry" || selectedAction === "cancel";
      if (
        (requiresOperate && !permissions.operate) ||
        (!requiresOperate && !permissions.approve) ||
        (selectedAction === "publish" && !capabilities.canary) ||
        (selectedAction !== "publish" && !capabilities.candidates)
      ) {
        throw new Error(
          "This action is unavailable in the current release-management mode",
        );
      }
      const data = candidate.data;
      if (!data) throw new Error("Candidate is unavailable");
      const payload: Record<string, unknown> = { reason };
      const receiptDigest = data.publication_receipt_digest;
      if (selectedAction === "approve") {
        if (!receiptDigest) {
          throw new Error(
            "The completed build has no verified publication receipt",
          );
        }
        payload.lock_digest = data.lock_digest;
        payload.policy_decision_digest = data.policy_decision;
        payload.evidence_digest = receiptDigest;
      }
      if (selectedAction === "publish") {
        if (!receiptDigest) {
          throw new Error(
            "The completed build has no verified publication receipt",
          );
        }
        payload.receipt_digest = receiptDigest;
      }
      return scannerSupplyChainApi.candidateAction(
        data.id,
        selectedAction,
        payload,
        data.version,
      );
    },
    onSuccess: (_, variables) => {
      toast.success(`${humanize(variables.selectedAction)} command accepted`);
      setAction(undefined);
      queryClient.invalidateQueries({
        queryKey: ["scanner-supply-chain"],
      });
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Candidate action failed")),
  });
  const createException = useMutation({
    mutationFn: (input: CandidateExceptionInput) =>
      scannerSupplyChainApi.createCandidateException(candidateId, input),
    onSuccess: () => {
      toast.success("Candidate exception recorded for policy re-evaluation");
      setExceptionGate(undefined);
      queryClient.invalidateQueries({
        queryKey: ["scanner-supply-chain", "candidate", candidateId],
      });
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Candidate exception failed")),
  });

  return (
    <div className="space-y-5">
      <Button type="button" variant="ghost" size="sm" onClick={onBack}>
        <ArrowLeftIcon aria-hidden="true" /> All candidates
      </Button>
      <ResourceState
        loading={candidate.isPending}
        error={candidate.error}
        onRetry={() => candidate.refetch()}
        variant="cards"
      >
        {candidate.data ? (
          <CandidateContent
            candidate={candidate.data}
            onAction={setAction}
            onException={setExceptionGate}
            exceptionPending={createException.isPending}
            eventStream={eventStream}
          />
        ) : null}
      </ResourceState>
      {candidate.data && action ? (
        <CandidateActionDialog
          action={action}
          candidate={candidate.data}
          pending={mutateAction.isPending}
          onClose={() => setAction(undefined)}
          onConfirm={(reason) =>
            mutateAction.mutate({ selectedAction: action, reason })
          }
        />
      ) : null}
      {exceptionGate ? (
        <CandidateExceptionDialog
          gate={exceptionGate}
          pending={createException.isPending}
          error={createException.error}
          onClose={() => setExceptionGate(undefined)}
          onConfirm={(input) => createException.mutate(input)}
        />
      ) : null}
    </div>
  );
}

const CandidateContent = memo(function CandidateContent({
  candidate,
  onAction,
  onException,
  exceptionPending,
  eventStream,
}: {
  candidate: CandidateDetail;
  onAction: (action: CandidateAction) => void;
  onException: (gate: GateEvidence) => void;
  exceptionPending: boolean;
  eventStream: ReturnType<typeof useScannerEvents>;
}) {
  const {
    capabilities,
    permissions,
    loading: capabilitiesLoading,
  } = useScannerReleaseCapabilities();
  const riskSummary = parseJson<RiskSummary>(candidate.risk_summary, {});
  const gates = useMemo<GateEvidence[]>(() => {
    if (candidate.gates?.length) return candidate.gates;
    return (candidate.build_steps ?? []).map((step) => ({
      name: step.step_key,
      state: step.state,
      summary: step.error_detail
        ? safeBackendFailureMessage(
            step.error_class,
            "This build step did not complete. Review its bounded evidence before retrying.",
          )
        : typeof step.summary === "string"
          ? safeDisplayText(step.summary, 1_024)
          : "Bounded build-step evidence was recorded.",
      evidence_digest: step.output_digest,
      evidence_uri: step.output_uri,
      started_at: step.started_at,
      completed_at: step.completed_at,
    }));
  }, [candidate.gates, candidate.build_steps]);
  const isTerminal = ["published", "rejected", "cancelled"].includes(
    candidate.state,
  );
  const canApprove = candidate.state === "awaiting_approval";
  const canPublish = ["approved", "auto_approved"].includes(candidate.state);
  const canRetry = ["failed", "blocked"].includes(candidate.state);
  const logEntries = useMemo(() => {
    const persisted = candidate.logs ?? [];
    const steps = (candidate.build_steps ?? []).map((step, index) => ({
      id: `step-${step.id}`,
      sequence: persisted.length + index + 1,
      timestamp:
        step.completed_at ??
        step.started_at ??
        candidate.updated_at ??
        candidate.created_at,
      level: step.error_detail ? "error" : "info",
      step: step.step_key,
      message: step.error_detail
        ? safeBackendFailureMessage(
            step.error_class,
            "This build step did not complete. Review its bounded evidence before retrying.",
          )
        : typeof step.summary === "string"
          ? safeDisplayText(step.summary, 2_048)
          : `Build step ${humanize(step.state)}.`,
      redacted: true,
    }));
    const events = eventStream.events.map((event, index) => ({
      id: event.id ?? `event-${event.sequence}`,
      sequence: persisted.length + steps.length + index + 1,
      timestamp: event.created_at,
      level: event.new_state === "failed" ? "error" : "info",
      step: event.event_type,
      message:
        event.reason ??
        `${humanize(event.prior_state)} → ${humanize(event.new_state)}`,
      redacted: true,
    }));
    return [...persisted, ...steps, ...events];
  }, [
    candidate.logs,
    candidate.build_steps,
    candidate.updated_at,
    candidate.created_at,
    eventStream.events,
  ]);

  return (
    <div className="space-y-5">
      <PageHeading
        title={`Candidate ${candidate.id}`}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <StatusBadge
              state={
                candidate.error_class === "no_changes"
                  ? "current"
                  : candidate.state
              }
            />
            <RiskBadge
              risk={
                candidate.risk ?? riskSummary.highest_risk ?? riskSummary.risk
              }
            />
            <span>
              Created <Timestamp value={candidate.created_at} /> by{" "}
              {safeDisplayText(candidate.actor ?? "system", 256)}
            </span>
          </span>
        }
        actions={
          <>
            {canRetry ? (
              <Button
                type="button"
                variant="outline"
                onClick={() => onAction("retry")}
                disabled={
                  capabilitiesLoading ||
                  !permissions.operate ||
                  !capabilities.candidates
                }
              >
                <RefreshCcwIcon aria-hidden="true" /> Retry
              </Button>
            ) : null}
            {!isTerminal && !canApprove && !canPublish ? (
              <Button
                type="button"
                variant="outline"
                onClick={() => onAction("cancel")}
                disabled={
                  capabilitiesLoading ||
                  !permissions.operate ||
                  !capabilities.candidates
                }
              >
                <BanIcon aria-hidden="true" /> Cancel
              </Button>
            ) : null}
            {canApprove ? (
              <>
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => onAction("reject")}
                  disabled={
                    capabilitiesLoading ||
                    !permissions.approve ||
                    !capabilities.candidates
                  }
                >
                  <XCircleIcon aria-hidden="true" /> Reject
                </Button>
                <Button
                  type="button"
                  onClick={() => onAction("approve")}
                  disabled={
                    capabilitiesLoading ||
                    !permissions.approve ||
                    !capabilities.candidates ||
                    candidate.separation_of_duties
                      ?.current_actor_can_approve === false
                  }
                  title={candidate.separation_of_duties?.reason}
                >
                  <CheckCircle2Icon aria-hidden="true" /> Approve
                </Button>
              </>
            ) : null}
            {canPublish ? (
              <Button
                type="button"
                onClick={() => onAction("publish")}
                disabled={
                  capabilitiesLoading ||
                  !permissions.approve ||
                  !capabilities.canary
                }
                title={
                  !capabilities.canary ? "Requires canary mode" : undefined
                }
              >
                <RocketIcon aria-hidden="true" /> Publish
              </Button>
            ) : null}
          </>
        }
      />

      <CandidateFreshnessNote selection={candidate.selection} />

      {candidate.error_class === "no_changes" ? (
        <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 p-4 text-sm text-emerald-200">
          <strong>Already current:</strong> Scheduled discovery found no scanner
          definition changes, so the candidate completed without a build.
        </div>
      ) : candidate.error_detail ? (
        <div className="rounded-lg border border-red-500/30 bg-red-500/10 p-4 text-sm text-red-200">
          <strong>Candidate processing requires review:</strong>{" "}
          {safeBackendFailureMessage(
            candidate.error_class,
            "Candidate processing did not complete. Review bounded evidence and the operation audit before retrying.",
          )}
        </div>
      ) : null}

      <dl className="grid gap-3 rounded-lg border border-border/70 bg-card p-4 sm:grid-cols-2 xl:grid-cols-4">
        <Metadata label="Definition commit">
          <CodeValue title={candidate.definition_commit}>
            {candidate.definition_commit}
          </CodeValue>
        </Metadata>
        <Metadata label="Lock digest">
          <CodeValue title={candidate.lock_digest}>
            {candidate.lock_digest}
          </CodeValue>
        </Metadata>
        <Metadata label="Policy revision">
          <span className="flex min-w-0 items-center gap-1">
            <span className="shrink-0">{candidate.policy_revision} ·</span>
            <CodeValue title={candidate.policy_decision}>
              {policyDecisionLabel(candidate.policy_decision)}
            </CodeValue>
          </span>
        </Metadata>
        <Metadata label="Last updated">
          <Timestamp value={candidate.updated_at} />
        </Metadata>
      </dl>

      {candidate.signature || candidate.provenance ? (
        <div className="grid gap-3 md:grid-cols-2">
          {candidate.signature ? (
            <CandidateVerificationEvidence
              label="Signature evidence"
              evidence={candidate.signature}
            />
          ) : null}
          {candidate.provenance ? (
            <CandidateVerificationEvidence
              label="Provenance evidence"
              evidence={candidate.provenance}
            />
          ) : null}
        </div>
      ) : null}

      <Tabs defaultValue="evidence">
        <div className="overflow-x-auto pb-1">
          <TabsList className="min-w-max">
            <TabsTrigger value="evidence">Gates &amp; evidence</TabsTrigger>
            <TabsTrigger value="changes">Changes</TabsTrigger>
            <TabsTrigger value="comparisons">Comparisons</TabsTrigger>
            <TabsTrigger value="logs">Build log</TabsTrigger>
            <TabsTrigger value="approvals">Approvals</TabsTrigger>
          </TabsList>
        </div>
        <TabsContent value="evidence">
          <GateChecklist
            gates={gates}
            onException={
              ["queued", "building", "blocked", "awaiting_approval"].includes(
                candidate.state,
              ) &&
              permissions.approve &&
              capabilities.candidates
                ? onException
                : undefined
            }
            exceptionPending={exceptionPending}
          />
        </TabsContent>
        <TabsContent value="changes">
          <Changes candidate={candidate} />
        </TabsContent>
        <TabsContent value="comparisons">
          <ComparisonGrid comparisons={candidate.comparisons} />
        </TabsContent>
        <TabsContent value="logs">
          <div className="mb-2 flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
            <span>
              Durable event stream: <StatusBadge state={eventStream.state} />
            </span>
            {eventStream.error ? (
              <span className="text-amber-300">
                Event stream reconnecting. Persisted candidate evidence remains
                available.
              </span>
            ) : null}
          </div>
          <StructuredLogViewer
            entries={logEntries}
            downloadUrl={
              candidate.build_steps?.find((step) => step.output_uri)?.output_uri
            }
          />
        </TabsContent>
        <TabsContent value="approvals">
          <ApprovalPanel candidate={candidate} />
        </TabsContent>
      </Tabs>
    </div>
  );
});

export function candidateFreshnessPresentation(
  selection: CandidateSelectionSummary | undefined,
):
  | { label: string; shortDescription: string; description: string }
  | undefined {
  switch (selection?.rebuild_reason) {
    case "no_stable_release":
      return {
        label: "Baseline rebuild",
        shortDescription: "No stable release exists.",
        description:
          "The weekly schedule forced a complete build because no stable scanner release exists yet.",
      };
    case "maximum_stable_image_age_exceeded":
      return {
        label: "Age-triggered rebuild",
        shortDescription: "The stable image exceeded its maximum age.",
        description:
          "The weekly schedule forced a complete build because the stable scanner image reached the configured freshness ceiling.",
      };
    case "policy_forced_weekly_rebuild":
      return {
        label: "Policy-forced rebuild",
        shortDescription: "Weekly rebuild is required by policy.",
        description:
          "The active policy requires a complete weekly rebuild even when scanner definitions are unchanged.",
      };
    case "stable_release_within_maximum_age":
      return {
        label: "Freshness no-op",
        shortDescription: "The stable image is within its maximum age.",
        description: selection.no_op_if_unchanged
          ? "The stable scanner image is within the configured freshness ceiling, so this scheduled run completes without a build when definitions are unchanged."
          : "The stable scanner image is within the configured freshness ceiling.",
      };
    default:
      return undefined;
  }
}

function CandidateFreshnessNote({
  selection,
}: {
  selection: CandidateSelectionSummary | undefined;
}) {
  const presentation = candidateFreshnessPresentation(selection);
  if (!presentation) return null;
  return (
    <div className="rounded-lg border border-blue-500/30 bg-blue-500/10 p-4 text-sm">
      <span className="rounded border border-blue-500/40 bg-blue-500/10 px-2 py-0.5 text-xs font-medium">
        {presentation.label}
      </span>
      <p className="mt-2 text-muted-foreground">{presentation.description}</p>
    </div>
  );
}

function CandidateVerificationEvidence({
  label,
  evidence,
}: {
  label: string;
  evidence: VerificationSummary;
}) {
  const total = evidence.total_count ?? evidence.keys?.length ?? 0;
  const verified = evidence.verified_count ?? 0;
  return (
    <section className="rounded-lg border border-border/70 bg-card p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-medium">{label}</h3>
        <StatusBadge state={evidence.state} />
      </div>
      <p className="mt-2 text-xs text-muted-foreground">
        {total > 0
          ? `${verified} of ${total} required evidence steps verified.`
          : safeDisplayText(evidence.detail ?? "Evidence is pending.", 512)}
      </p>
      {evidence.digests?.length ? (
        <p className="mt-2 text-xs text-muted-foreground">
          {evidence.digests.length} immutable digest
          {evidence.digests.length === 1 ? "" : "s"} recorded
        </p>
      ) : null}
    </section>
  );
}

function GateChecklist({
  gates,
  onException,
  exceptionPending,
}: {
  gates: GateEvidence[];
  onException?: (gate: GateEvidence) => void;
  exceptionPending: boolean;
}) {
  if (!gates.length) {
    return (
      <div className="rounded-lg border border-dashed border-border p-8 text-center text-sm text-muted-foreground">
        Gate evidence has not been persisted for this candidate yet.
      </div>
    );
  }
  return (
    <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
      <PanelHeading
        title="Mandatory release gates"
        description="Approval binds this evidence to the exact lock and policy digest."
      />
      <div className="divide-y divide-border/50">
        {gates.map((gate) => {
          const evidenceHref = safeEvidenceHref(gate.evidence_uri);
          return (
            <div
              key={gate.name}
              className="grid gap-2 px-4 py-3 sm:grid-cols-[minmax(10rem,1fr)_auto_minmax(12rem,2fr)] sm:items-center"
            >
              <span className="text-sm font-medium">{humanize(gate.name)}</span>
              <StatusBadge state={gate.excepted ? "excepted" : gate.state} />
              <div className="min-w-0 text-xs text-muted-foreground">
                <p>
                  {safeDisplayText(
                    gate.summary ?? "No summary was provided.",
                    1_024,
                  )}
                </p>
                {evidenceHref ? (
                  <a
                    href={evidenceHref}
                    target="_blank"
                    rel="noreferrer"
                    className="mt-1 inline-flex items-center gap-1 text-primary hover:underline"
                  >
                    Evidence{" "}
                    <ExternalLinkIcon className="size-3" aria-hidden="true" />
                  </a>
                ) : gate.evidence_digest ? (
                  <CodeValue title={gate.evidence_digest}>
                    {gate.evidence_digest}
                  </CodeValue>
                ) : null}
                {onException && isExceptionEligibleGate(gate) ? (
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    className="mt-2"
                    disabled={exceptionPending}
                    onClick={() => onException(gate)}
                  >
                    Record exception
                  </Button>
                ) : null}
              </div>
            </div>
          );
        })}
      </div>
    </section>
  );
}

function Changes({ candidate }: { candidate: CandidateDetail }) {
  const proposalHref = safeEvidenceHref(candidate.proposal_url);
  return (
    <div className="space-y-4">
      <ArtifactDiffViewer ownerType="candidate" ownerId={candidate.id} />
      {proposalHref ? (
        <a
          href={proposalHref}
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-2 text-sm text-primary hover:underline"
        >
          <GitCommitHorizontalIcon className="size-4" aria-hidden="true" />
          Open the reviewable Git proposal
        </a>
      ) : null}
    </div>
  );
}

function ComparisonGrid({
  comparisons,
}: {
  comparisons?: CandidateDetail["comparisons"];
}) {
  const values: Array<[string, ComparisonDelta | undefined]> = [
    ["Finding normalization", comparisons?.findings],
    ["Vulnerabilities", comparisons?.vulnerabilities],
    ["Licenses", comparisons?.licenses],
    ["Performance", comparisons?.performance],
  ];
  return (
    <div className="grid gap-4 md:grid-cols-2">
      {values.map(([label, comparison]) => (
        <section
          key={label}
          className="rounded-lg border border-border/70 bg-card p-4"
        >
          <div className="flex items-center justify-between gap-3">
            <h3 className="text-sm font-medium">{label}</h3>
            <StatusBadge state={comparison?.status ?? "pending"} />
          </div>
          <p className="mt-2 text-sm text-muted-foreground">
            {safeDisplayText(
              comparison?.summary ??
                "Comparison evidence has not been recorded yet.",
              1_024,
            )}
          </p>
          {comparison ? (
            <dl className="mt-3 grid grid-cols-3 gap-2 text-xs">
              <Metadata label="Baseline">{comparison.baseline ?? "—"}</Metadata>
              <Metadata label="Candidate">
                {comparison.candidate ?? "—"}
              </Metadata>
              <Metadata label="Delta">{comparison.delta ?? "—"}</Metadata>
            </dl>
          ) : null}
        </section>
      ))}
    </div>
  );
}

function ApprovalPanel({ candidate }: { candidate: CandidateDetail }) {
  const approvals = candidate.approvals ?? [];
  return (
    <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
      <PanelHeading
        title="Approval record"
        description={
          safeDisplayText(candidate.separation_of_duties?.reason, 512) ||
          "Approval decisions are immutable and bind the current evidence."
        }
      />
      {approvals.length ? (
        <div className="divide-y divide-border/50">
          {approvals.map((approval) => (
            <div key={approval.id} className="px-4 py-3">
              <div className="flex flex-wrap items-center gap-2">
                <StatusBadge state={approval.action} />
                <span className="text-sm font-medium">
                  {safeDisplayText(approval.actor, 256)}
                </span>
                <span className="text-xs text-muted-foreground">
                  <Timestamp value={approval.created_at} />
                </span>
              </div>
              <p className="mt-1 text-sm text-muted-foreground">
                {safeDisplayText(approval.reason, 1_024)}
              </p>
              {approval.action === "exception" ? (
                <dl className="mt-3 grid gap-3 rounded-md border border-amber-500/30 bg-amber-500/10 p-3 text-xs sm:grid-cols-2">
                  <Metadata label="Exception gate">
                    {humanize(
                      safeDisplayText(approval.exception_scope, 128) ||
                        "Not reported",
                    )}
                  </Metadata>
                  <Metadata label="Control owner">
                    {safeDisplayText(approval.exception_owner_id, 256) ||
                      "Not reported"}
                  </Metadata>
                  <Metadata label="Compensating control">
                    {safeDisplayText(approval.compensating_control, 1_024) ||
                      "Not reported"}
                  </Metadata>
                  <Metadata label="Expires">
                    <Timestamp value={approval.expires_at} />
                  </Metadata>
                </dl>
              ) : null}
              {approval.evidence_digest ? (
                <CodeValue title={approval.evidence_digest}>
                  {approval.evidence_digest}
                </CodeValue>
              ) : null}
            </div>
          ))}
        </div>
      ) : (
        <p className="p-8 text-center text-sm text-muted-foreground">
          No approval decision has been recorded.
        </p>
      )}
    </section>
  );
}

function CandidateExceptionDialog({
  gate,
  pending,
  error,
  onClose,
  onConfirm,
}: {
  gate: GateEvidence;
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onConfirm: (input: CandidateExceptionInput) => void;
}) {
  const ownerId = useId();
  const reasonId = useId();
  const controlId = useId();
  const digestId = useId();
  const expirationId = useId();
  const [owner, setOwner] = useState("");
  const [reason, setReason] = useState("");
  const [control, setControl] = useState("");
  const [digest, setDigest] = useState(gate.evidence_digest ?? "");
  const [expiresAt, setExpiresAt] = useState(defaultExceptionExpiration);
  const expiration = new Date(expiresAt);
  const validExpiration =
    Number.isFinite(expiration.getTime()) &&
    expiration.getTime() > Date.now() &&
    expiration.getTime() <= Date.now() + 90 * 24 * 60 * 60 * 1_000;
  const valid =
    owner.trim().length >= 3 &&
    reason.trim().length >= 3 &&
    control.trim().length >= 3 &&
    /^sha256:[a-f0-9]{64}$/.test(digest.trim()) &&
    validExpiration;

  return (
    <Dialog open onOpenChange={(open) => !open && !pending && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto overscroll-contain">
        <DialogHeader>
          <DialogTitle>
            Record {humanize(gate.name)} gate exception?
          </DialogTitle>
          <DialogDescription>
            This creates an expiring, auditable approval record. It does not
            bypass hard supply-chain gates or mutate existing evidence; trusted
            policy evaluation must consume the record.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor={ownerId}>Compensating-control owner</Label>
            <Input
              id={ownerId}
              name="exception_owner_id"
              value={owner}
              onChange={(event) => setOwner(event.target.value)}
              autoComplete="off"
              spellCheck={false}
              placeholder="team-security-platform"
            />
            <p className="text-xs text-muted-foreground">
              The owner must be distinct from the approving operator.
            </p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor={reasonId}>Exception reason</Label>
            <textarea
              id={reasonId}
              name="exception_reason"
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              rows={3}
              maxLength={2_000}
              placeholder="Explain the bounded risk being accepted…"
              className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor={controlId}>Compensating control</Label>
            <textarea
              id={controlId}
              name="compensating_control"
              value={control}
              onChange={(event) => setControl(event.target.value)}
              rows={3}
              maxLength={2_000}
              placeholder="Describe monitoring, isolation, or rollback controls…"
              className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor={digestId}>Exception evidence digest</Label>
            <Input
              id={digestId}
              name="exception_evidence_digest"
              value={digest}
              onChange={(event) => setDigest(event.target.value)}
              autoComplete="off"
              spellCheck={false}
              placeholder={`sha256:${"0".repeat(64)}`}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor={expirationId}>Expiration</Label>
            <Input
              id={expirationId}
              name="exception_expires_at"
              type="datetime-local"
              value={expiresAt}
              onChange={(event) => setExpiresAt(event.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              Expiration must be in the future, within 90 days, and within the
              active policy’s maximum exception age.
            </p>
          </div>
          {error ? (
            <p
              className="rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-200"
              role="alert"
            >
              {safeErrorMessage(error, "The exception could not be recorded.")}
            </p>
          ) : null}
        </div>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={onClose}
            disabled={pending}
          >
            Cancel
          </Button>
          <Button
            type="button"
            disabled={!valid || pending}
            aria-busy={pending || undefined}
            onClick={() =>
              onConfirm({
                gate: gate.name,
                owner_id: owner.trim(),
                reason: reason.trim(),
                compensating_control: control.trim(),
                evidence_digest: digest.trim(),
                expires_at: expiration.toISOString(),
              })
            }
          >
            {pending ? (
              <Loader2Icon className="animate-spin" aria-hidden="true" />
            ) : null}
            Record exception
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function isExceptionEligibleGate(gate: GateEvidence): boolean {
  const hardGates = new Set([
    "lock",
    "artifacts",
    "platforms",
    "signature",
    "provenance",
    "parser",
    "source",
    "secret_scan",
  ]);
  return (
    !hardGates.has(gate.name) &&
    !["passed", "completed", "excepted"].includes(gate.state)
  );
}

function defaultExceptionExpiration(): string {
  const date = new Date(Date.now() + 7 * 24 * 60 * 60 * 1_000);
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 16);
}

function CandidateActionDialog({
  action,
  candidate,
  pending,
  onClose,
  onConfirm,
}: {
  action: CandidateAction;
  candidate: CandidateDetail;
  pending: boolean;
  onClose: () => void;
  onConfirm: (reason: string) => void;
}) {
  const descriptions: Record<CandidateAction, string> = {
    retry: `Retry safe failed steps for ${candidate.id}. Completed immutable evidence will be reused only when its input digest still matches.`,
    cancel: `Request cooperative cancellation of ${candidate.id}. Running build steps stop at a safe boundary.`,
    approve: `Approve ${candidate.id} at lock ${candidate.lock_digest ?? "not reported"} under policy revision ${candidate.policy_revision}.`,
    reject: `Reject ${candidate.id}. It cannot be published unless a new candidate or allowed retry is created.`,
    publish: `Publish ${candidate.id} as an immutable signed scanner release after rechecking every mandatory gate.`,
  };
  return (
    <ActionDialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      title={`${humanize(action)} candidate?`}
      description={descriptions[action]}
      confirmLabel={humanize(action)}
      pending={pending}
      destructive={action === "cancel" || action === "reject"}
      onConfirm={onConfirm}
    />
  );
}

function policyDecisionLabel(value?: string): string {
  if (value && /^sha256:[a-f0-9]{64}$/.test(value)) {
    return `decision ${value.slice(0, 19)}…`;
  }
  return humanize(value);
}

function Metadata({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 min-w-0 text-sm">{children}</dd>
    </div>
  );
}
