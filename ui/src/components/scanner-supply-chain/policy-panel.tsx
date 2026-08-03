import { memo, useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowDownIcon,
  ArrowUpIcon,
  BeakerIcon,
  CheckCircle2Icon,
  HistoryIcon,
  PlusIcon,
  RotateCcwIcon,
  SaveIcon,
  ShieldCheckIcon,
  Trash2Icon,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ActionDialog } from "./action-dialog";
import { useScannerReleaseCapabilities } from "./capabilities";
import {
  JsonPreview,
  PageHeading,
  PanelHeading,
  ResourceState,
  StatusBadge,
  Timestamp,
  humanize,
} from "./primitives";
import {
  parseJson,
  scannerSupplyChainApi,
  type PolicyRules,
  type PolicySchedule,
  type Risk,
  type ScannerPolicy,
} from "@/lib/scanner-supply-chain";
import { safeDisplayText, safeErrorMessage } from "@/lib/safe-display";

type PolicyDraft = {
  schedule: PolicySchedule;
  rules: PolicyRules;
};

const CHANGE_CLASSES = [
  "rebuild_only",
  "patch",
  "minor",
  "major",
  "parser",
  "license",
  "platform",
  "source",
];
const GATES = [
  "lock",
  "artifacts",
  "platforms",
  "smoke",
  "parser",
  "vulnerability",
  "license",
  "sbom",
  "signature",
  "provenance",
  "source",
  "secret_scan",
  "compose",
  "kubernetes",
];

export const PolicyPanel = memo(function PolicyPanel() {
  const {
    capabilities,
    permissions,
    loading: capabilitiesLoading,
  } = useScannerReleaseCapabilities();
  const queryClient = useQueryClient();
  const policy = useQuery({
    queryKey: ["scanner-supply-chain", "policy"],
    queryFn: scannerSupplyChainApi.policy,
  });
  const candidates = useQuery({
    queryKey: ["scanner-supply-chain", "candidates", "policy-dry-run"],
    queryFn: () => scannerSupplyChainApi.candidates({ limit: 50 }),
    staleTime: 60_000,
  });
  const history = useQuery({
    queryKey: ["scanner-supply-chain", "policy", "history"],
    queryFn: scannerSupplyChainApi.policyHistory,
    staleTime: 60_000,
  });
  const [draft, setDraft] = useState<PolicyDraft>(() => defaultDraft());
  const [candidateId, setCandidateId] = useState("");
  const [restoreRevision, setRestoreRevision] = useState<number>();

  const policyRevision = policy.data?.revision;
  useEffect(() => {
    if (!policy.data) return;
    setDraft(policyToDraft(policy.data));
  }, [policyRevision]);

  const dirty = useMemo(() => {
    if (!policy.data) return false;
    return JSON.stringify(draft) !== JSON.stringify(policyToDraft(policy.data));
  }, [draft, policy.data]);

  const validation = useMutation({
    mutationFn: () => scannerSupplyChainApi.validatePolicy(draft),
    onSuccess: (result) => {
      if (result?.valid) toast.success("Policy is valid");
      else toast.error("Policy validation found blocking errors");
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Validation failed")),
  });
  const save = useMutation({
    mutationFn: () => {
      if (!permissions.administer || !capabilities.candidates) {
        throw new Error("Policy changes require candidate mode");
      }
      if (!policy.data) throw new Error("Active policy is unavailable");
      return scannerSupplyChainApi.updatePolicy(draft, policy.data.revision);
    },
    onSuccess: () => {
      toast.success("Policy revision activated");
      queryClient.invalidateQueries({
        queryKey: ["scanner-supply-chain", "policy"],
      });
    },
    onError: (error) =>
      toast.error(safeErrorMessage(error, "Policy update failed")),
  });
  const dryRun = useMutation({
    mutationFn: () => scannerSupplyChainApi.policyDryRun(candidateId, draft),
    onError: (error) => toast.error(safeErrorMessage(error, "Dry run failed")),
  });
  const restore = useMutation({
    mutationFn: (reason: string) => {
      if (!permissions.administer || !capabilities.candidates) {
        throw new Error("Policy restore requires candidate mode");
      }
      if (restoreRevision === undefined)
        throw new Error("Revision is unavailable");
      return scannerSupplyChainApi.restorePolicy(restoreRevision, reason);
    },
    onSuccess: () => {
      toast.success("Historical policy restored as a new active revision");
      setRestoreRevision(undefined);
      queryClient.invalidateQueries({
        queryKey: ["scanner-supply-chain", "policy"],
      });
    },
    onError: (error) => toast.error(safeErrorMessage(error, "Restore failed")),
  });

  return (
    <div className="space-y-5">
      <PageHeading
        title="Release policy"
        description="Versioned schedules, mandatory evidence, approval controls, canary thresholds, rollback, retention, and notification routing."
        actions={
          <>
            <Button
              type="button"
              variant="outline"
              onClick={() => validation.mutate()}
              disabled={
                capabilitiesLoading ||
                !permissions.read ||
                !capabilities.read ||
                validation.isPending
              }
            >
              <ShieldCheckIcon aria-hidden="true" /> Validate
            </Button>
            <Button
              type="button"
              onClick={() => save.mutate()}
              disabled={
                capabilitiesLoading ||
                !permissions.administer ||
                !capabilities.candidates ||
                !dirty ||
                save.isPending
              }
              title={
                !permissions.administer
                  ? "Supply-chain administrator access is required"
                  : !capabilities.candidates
                    ? "Requires candidate mode"
                    : undefined
              }
            >
              <SaveIcon aria-hidden="true" /> Save new revision
            </Button>
          </>
        }
      />
      <ResourceState
        loading={policy.isPending}
        error={policy.error}
        onRetry={() => policy.refetch()}
        variant="cards"
      >
        {policy.data ? (
          <div className="space-y-4">
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border/70 bg-card p-3 text-sm">
              <span>
                Active revision <strong>{policy.data.revision}</strong> ·
                created by {policy.data.created_by ?? "system"} ·{" "}
                <Timestamp
                  value={policy.data.updated_at ?? policy.data.created_at}
                />
              </span>
              <StatusBadge
                state={
                  validation.data?.valid === false
                    ? "failed"
                    : dirty
                      ? "changed"
                      : "active"
                }
              />
            </div>

            {validation.data ? (
              <ValidationResult result={validation.data} />
            ) : null}

            <Tabs defaultValue="configuration">
              <div className="overflow-x-auto pb-1">
                <TabsList className="min-w-max">
                  <TabsTrigger value="configuration">Configuration</TabsTrigger>
                  <TabsTrigger value="preview">Preview &amp; diff</TabsTrigger>
                  <TabsTrigger value="dry-run">Historical dry run</TabsTrigger>
                  <TabsTrigger value="history">Revision history</TabsTrigger>
                </TabsList>
              </div>
              <TabsContent value="configuration">
                <PolicyForm
                  draft={draft}
                  onChange={setDraft}
                  editable={
                    !capabilitiesLoading &&
                    permissions.administer &&
                    capabilities.candidates
                  }
                />
              </TabsContent>
              <TabsContent value="preview">
                <div className="grid gap-4 xl:grid-cols-2">
                  <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
                    <PanelHeading
                      title="Proposed JSON"
                      description="Read-only canonical request preview"
                    />
                    <div className="p-4">
                      <JsonPreview value={draft} />
                    </div>
                  </section>
                  <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
                    <PanelHeading title="Diff from active revision" />
                    <pre className="max-h-[32rem] overflow-auto p-4 font-mono text-xs leading-5 text-muted-foreground">
                      {policy.data.diff ??
                        createObjectDiff(policyToDraft(policy.data), draft)}
                    </pre>
                  </section>
                </div>
              </TabsContent>
              <TabsContent value="dry-run">
                <section className="overflow-hidden rounded-lg border border-border/70 bg-card">
                  <PanelHeading
                    title="Evaluate a historical candidate"
                    description="Runs the unsaved draft without changing candidate or policy state."
                  />
                  <div className="space-y-4 p-4">
                    <div className="flex flex-col gap-2 sm:flex-row">
                      <label className="sr-only" htmlFor="policy-candidate">
                        Candidate
                      </label>
                      <select
                        id="policy-candidate"
                        value={candidateId}
                        onChange={(event) => setCandidateId(event.target.value)}
                        className="h-10 min-w-0 flex-1 rounded-md border border-input bg-background px-3 text-sm"
                      >
                        <option value="">Choose a historical candidate</option>
                        {(candidates.data?.items ?? []).map((candidate) => (
                          <option key={candidate.id} value={candidate.id}>
                            {candidate.id} · {humanize(candidate.state)}
                          </option>
                        ))}
                      </select>
                      <Button
                        type="button"
                        onClick={() => dryRun.mutate()}
                        disabled={
                          capabilitiesLoading ||
                          !permissions.read ||
                          !capabilities.read ||
                          !candidateId ||
                          dryRun.isPending
                        }
                      >
                        <BeakerIcon aria-hidden="true" /> Evaluate
                      </Button>
                    </div>
                    {dryRun.data ? (
                      <div className="rounded-lg border border-border/70 bg-background/60 p-4">
                        <div className="flex flex-wrap items-center gap-2">
                          <StatusBadge state={dryRun.data.outcome} />
                          <span className="text-sm">
                            Automatic promotion:{" "}
                            {dryRun.data.auto_promotion
                              ? "eligible"
                              : "not eligible"}
                          </span>
                        </div>
                        {dryRun.data.blocking_reasons?.length ? (
                          <ul className="mt-3 text-sm text-red-300">
                            {dryRun.data.blocking_reasons.map((reason) => (
                              <li key={reason}>
                                • {safeDisplayText(reason, 1_024)}
                              </li>
                            ))}
                          </ul>
                        ) : null}
                        {dryRun.data.advisories?.length ? (
                          <ul className="mt-3 text-sm text-amber-200">
                            {dryRun.data.advisories.map((advisory) => (
                              <li key={advisory}>
                                • {safeDisplayText(advisory, 1_024)}
                              </li>
                            ))}
                          </ul>
                        ) : null}
                      </div>
                    ) : null}
                  </div>
                </section>
              </TabsContent>
              <TabsContent value="history">
                <ResourceState
                  loading={history.isPending}
                  error={history.error}
                  empty={!history.data?.items.length}
                  emptyTitle="No policy revision history"
                  onRetry={() => history.refetch()}
                >
                  <div className="overflow-hidden rounded-lg border border-border/70 bg-card">
                    <div className="divide-y divide-border/50">
                      {(history.data?.items ?? []).map((revision) => (
                        <div
                          key={`${revision.id}-${revision.revision}`}
                          className="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-center sm:justify-between"
                        >
                          <div>
                            <div className="flex items-center gap-2">
                              <HistoryIcon
                                className="size-4 text-muted-foreground"
                                aria-hidden="true"
                              />
                              <span className="font-medium">
                                Revision {revision.revision}
                              </span>
                              <StatusBadge
                                state={
                                  revision.enabled ? "active" : "historical"
                                }
                              />
                            </div>
                            <p className="mt-1 text-xs text-muted-foreground">
                              by {revision.created_by ?? "system"} ·{" "}
                              <Timestamp
                                value={
                                  revision.updated_at ?? revision.created_at
                                }
                              />
                            </p>
                          </div>
                          <Button
                            type="button"
                            size="sm"
                            variant="outline"
                            disabled={
                              capabilitiesLoading ||
                              !permissions.administer ||
                              !capabilities.candidates ||
                              revision.enabled
                            }
                            onClick={() =>
                              setRestoreRevision(revision.revision)
                            }
                          >
                            <RotateCcwIcon aria-hidden="true" /> Restore as new
                            revision
                          </Button>
                        </div>
                      ))}
                    </div>
                  </div>
                </ResourceState>
              </TabsContent>
            </Tabs>
          </div>
        ) : null}
      </ResourceState>

      <ActionDialog
        open={restoreRevision !== undefined}
        onOpenChange={(open) => {
          if (!open) setRestoreRevision(undefined);
        }}
        title={`Restore policy revision ${restoreRevision ?? ""}?`}
        description="The historical content is copied into a new active revision. Existing evidence remains bound to the policy revision under which it was produced."
        confirmLabel="Restore revision"
        pending={restore.isPending}
        onConfirm={(reason) => restore.mutate(reason)}
      />
    </div>
  );
});

function PolicyForm({
  draft,
  onChange,
  editable,
}: {
  draft: PolicyDraft;
  onChange: (draft: PolicyDraft) => void;
  editable: boolean;
}) {
  const schedule = draft.schedule;
  const rules = draft.rules;
  const daily = schedule.daily_discovery ?? {};
  const weekly = schedule.weekly_candidate ?? {};
  const windows = schedule.maintenance_windows ?? [];

  function updateSchedule(values: Partial<PolicySchedule>) {
    onChange({ ...draft, schedule: { ...schedule, ...values } });
  }
  function updateRules(values: Partial<PolicyRules>) {
    onChange({ ...draft, rules: { ...rules, ...values } });
  }
  function updateWindow(
    index: number,
    values: Partial<NonNullable<PolicySchedule["maintenance_windows"]>[number]>,
  ) {
    updateSchedule({
      maintenance_windows: windows.map((window, current) =>
        current === index ? { ...window, ...values } : window,
      ),
    });
  }
  function moveWindow(index: number, direction: -1 | 1) {
    const destination = index + direction;
    if (destination < 0 || destination >= windows.length) return;
    const next = [...windows];
    [next[index], next[destination]] = [next[destination], next[index]];
    updateSchedule({ maintenance_windows: next });
  }

  return (
    <fieldset
      disabled={!editable}
      aria-label="Scanner release policy configuration"
      className="grid gap-4 disabled:opacity-80 xl:grid-cols-2"
    >
      <FormSection
        title="Schedules and maintenance"
        description="Cron expressions and previews are evaluated by the server in the organization timezone."
      >
        {!editable ? (
          <p className="rounded-md border border-border bg-muted/30 p-2 text-xs text-muted-foreground">
            Policy configuration is read-only for this persona or deployment
            mode.
          </p>
        ) : null}
        <div className="grid gap-2 sm:grid-cols-2">
          <CheckboxField
            checked={daily.enabled !== false}
            onChange={(enabled) =>
              updateSchedule({ daily_discovery: { ...daily, enabled } })
            }
            label="Enable daily discovery"
          />
          <CheckboxField
            checked={weekly.enabled !== false}
            onChange={(enabled) =>
              updateSchedule({ weekly_candidate: { ...weekly, enabled } })
            }
            label="Enable weekly candidate"
          />
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Timezone">
            <Input
              value={schedule.timezone ?? ""}
              onChange={(event) =>
                updateSchedule({ timezone: event.target.value })
              }
              placeholder="America/New_York"
            />
          </Field>
          <Field label="Daily discovery time">
            <Input
              type="time"
              value={daily.at ?? "02:00"}
              onChange={(event) =>
                updateSchedule({
                  daily_discovery: {
                    ...daily,
                    frequency: "daily",
                    at: event.target.value,
                  },
                })
              }
            />
          </Field>
          <Field label="Daily jitter">
            <Input
              value={daily.jitter ?? schedule.jitter ?? ""}
              onChange={(event) =>
                updateSchedule({
                  daily_discovery: {
                    ...daily,
                    frequency: "daily",
                    jitter: event.target.value,
                  },
                })
              }
              placeholder="20m"
            />
          </Field>
          <Field label="Daily catch-up window">
            <Input
              value={daily.catch_up ?? schedule.catch_up_window ?? ""}
              onChange={(event) =>
                updateSchedule({
                  daily_discovery: {
                    ...daily,
                    frequency: "daily",
                    catch_up: event.target.value,
                  },
                })
              }
              placeholder="6h"
            />
          </Field>
          <Field label="Weekly candidate day">
            <select
              value={weekly.weekday ?? "Sunday"}
              onChange={(event) =>
                updateSchedule({
                  weekly_candidate: {
                    ...weekly,
                    frequency: "weekly",
                    weekday: event.target.value,
                  },
                })
              }
              className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm"
            >
              {[
                "Sunday",
                "Monday",
                "Tuesday",
                "Wednesday",
                "Thursday",
                "Friday",
                "Saturday",
              ].map((day) => (
                <option key={day} value={day}>
                  {day}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Weekly candidate time">
            <Input
              type="time"
              value={weekly.at ?? "03:00"}
              onChange={(event) =>
                updateSchedule({
                  weekly_candidate: {
                    ...weekly,
                    frequency: "weekly",
                    at: event.target.value,
                  },
                })
              }
            />
          </Field>
          <Field label="Weekly jitter">
            <Input
              value={weekly.jitter ?? ""}
              onChange={(event) =>
                updateSchedule({
                  weekly_candidate: {
                    ...weekly,
                    frequency: "weekly",
                    jitter: event.target.value,
                  },
                })
              }
              placeholder="20m"
            />
          </Field>
          <Field label="Weekly catch-up window">
            <Input
              value={weekly.catch_up ?? ""}
              onChange={(event) =>
                updateSchedule({
                  weekly_candidate: {
                    ...weekly,
                    frequency: "weekly",
                    catch_up: event.target.value,
                  },
                })
              }
              placeholder="48h"
            />
          </Field>
          <Field label="Maximum stable image age">
            <Input
              value={schedule.maximum_stable_image_age ?? "168h0m0s"}
              onChange={(event) =>
                updateSchedule({
                  maximum_stable_image_age: event.target.value,
                })
              }
              placeholder="168h0m0s"
              aria-describedby="maximum-stable-image-age-help"
            />
            <span
              id="maximum-stable-image-age-help"
              className="text-xs text-muted-foreground"
            >
              Force a candidate when the stable scanner image exceeds this Go
              duration, even if definitions are unchanged.
            </span>
          </Field>
          <div className="flex items-end pb-2">
            <CheckboxField
              checked={schedule.force_weekly_rebuild === true}
              onChange={(forceWeeklyRebuild) =>
                updateSchedule({
                  force_weekly_rebuild: forceWeeklyRebuild,
                })
              }
              label="Force every weekly rebuild"
            />
          </div>
        </div>
        <div className="space-y-3 border-t border-border/60 pt-3">
          <div className="flex items-center justify-between gap-3">
            <div>
              <h3 className="text-xs font-medium">Maintenance windows</h3>
              <p className="text-xs text-muted-foreground">
                Named five-field cron windows cannot overlap and preserve their
                order.
              </p>
            </div>
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={windows.length >= 32}
              onClick={() =>
                updateSchedule({
                  maintenance_windows: [
                    ...windows,
                    {
                      id: newMaintenanceWindowID(),
                      name: `Maintenance window ${windows.length + 1}`,
                      cron: "0 3 * * 0",
                      duration: "2h",
                    },
                  ],
                })
              }
            >
              <PlusIcon aria-hidden="true" /> Add window
            </Button>
          </div>
          {windows.length === 0 ? (
            <p className="rounded-md border border-dashed border-border p-3 text-xs text-muted-foreground">
              No maintenance restriction is configured.
            </p>
          ) : null}
          {windows.map((window, index) => (
            <div
              key={window.id || `${window.name}-${index}`}
              className="space-y-3 rounded-md border border-border/70 bg-background/40 p-3"
            >
              <div className="flex items-center justify-between gap-2">
                <span className="text-xs font-medium">Window {index + 1}</span>
                <div className="flex gap-1">
                  <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    disabled={index === 0}
                    aria-label={`Move ${window.name} up`}
                    onClick={() => moveWindow(index, -1)}
                  >
                    <ArrowUpIcon aria-hidden="true" />
                  </Button>
                  <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    disabled={index === windows.length - 1}
                    aria-label={`Move ${window.name} down`}
                    onClick={() => moveWindow(index, 1)}
                  >
                    <ArrowDownIcon aria-hidden="true" />
                  </Button>
                  <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    aria-label={`Remove ${window.name}`}
                    onClick={() =>
                      updateSchedule({
                        maintenance_windows: windows.filter(
                          (_, current) => current !== index,
                        ),
                      })
                    }
                  >
                    <Trash2Icon aria-hidden="true" />
                  </Button>
                </div>
              </div>
              <div className="grid gap-3 sm:grid-cols-3">
                <Field label="Name">
                  <Input
                    value={window.name}
                    onChange={(event) =>
                      updateWindow(index, { name: event.target.value })
                    }
                  />
                </Field>
                <Field label="Cron">
                  <Input
                    value={window.cron ?? ""}
                    onChange={(event) =>
                      updateWindow(index, { cron: event.target.value })
                    }
                    placeholder="0 3 * * 0"
                  />
                </Field>
                <Field label="Duration">
                  <Input
                    value={window.duration ?? ""}
                    onChange={(event) =>
                      updateWindow(index, { duration: event.target.value })
                    }
                    placeholder="2h"
                  />
                </Field>
              </div>
            </div>
          ))}
        </div>
      </FormSection>

      <FormSection
        title="Approval and separation of duties"
        description="Manual approval remains the default; auto-promotion is narrowly policy-gated."
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Approval mode">
            <select
              value={rules.approval_mode ?? "manual"}
              onChange={(event) =>
                updateRules({
                  approval_mode: event.target
                    .value as PolicyRules["approval_mode"],
                })
              }
              className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm"
            >
              <option value="manual">Manual</option>
              <option value="policy_gated">Policy-gated automation</option>
            </select>
          </Field>
          <Field label="Required approvers">
            <Input
              type="number"
              min={0}
              value={rules.required_approvals ?? 1}
              onChange={(event) =>
                updateRules({ required_approvals: Number(event.target.value) })
              }
            />
          </Field>
        </div>
        <CheckboxField
          checked={rules.separate_creator ?? true}
          onChange={(separate_creator) => updateRules({ separate_creator })}
          label="Candidate creator cannot be the only approver"
        />
        <CheckboxGroup<Risk>
          label="Auto-promote risk classes"
          options={["low", "medium"]}
          values={rules.auto_promote_risks ?? []}
          onChange={(auto_promote_risks) => updateRules({ auto_promote_risks })}
        />
        <CheckboxGroup
          label="Auto-promote change classes"
          options={CHANGE_CLASSES}
          values={rules.auto_promote_changes ?? []}
          onChange={(auto_promote_changes) =>
            updateRules({ auto_promote_changes })
          }
        />
      </FormSection>

      <FormSection
        title="Mandatory gates and exceptions"
        description="Signature, provenance, parser, source, platform, lock, artifact, and secret gates remain non-bypassable."
      >
        <CheckboxGroup
          label="Required gates"
          options={GATES}
          values={rules.required_gates ?? GATES}
          onChange={(required_gates) => updateRules({ required_gates })}
        />
        <div className="grid gap-2 sm:grid-cols-2">
          <CheckboxField
            checked={rules.allow_exceptions?.vulnerability ?? true}
            onChange={(value) =>
              updateRules({
                allow_exceptions: {
                  ...(rules.allow_exceptions ?? {}),
                  vulnerability: value,
                },
              })
            }
            label="Allow expiring vulnerability exceptions"
          />
          <CheckboxField
            checked={rules.allow_exceptions?.license ?? true}
            onChange={(value) =>
              updateRules({
                allow_exceptions: {
                  ...(rules.allow_exceptions ?? {}),
                  license: value,
                },
              })
            }
            label="Allow expiring license exceptions"
          />
        </div>
        <Field label="Maximum exception age">
          <Input
            value={rules.exception_max_age ?? ""}
            onChange={(event) =>
              updateRules({ exception_max_age: event.target.value })
            }
            placeholder="720h"
          />
        </Field>
      </FormSection>

      <FormSection
        title="Canary and rollback thresholds"
        description="Hard integrity failures always roll back regardless of configured rate thresholds."
      >
        <div className="grid gap-3 sm:grid-cols-3">
          <NumberField
            label="Canary workers"
            value={rules.canary?.size ?? 1}
            onChange={(size) =>
              updateRules({ canary: { ...(rules.canary ?? {}), size } })
            }
          />
          <NumberField
            label="Minimum samples"
            value={rules.canary?.minimum_samples ?? 10}
            onChange={(minimum_samples) =>
              updateRules({
                canary: { ...(rules.canary ?? {}), minimum_samples },
              })
            }
          />
          <Field label="Observation">
            <Input
              value={rules.canary?.observation ?? ""}
              onChange={(event) =>
                updateRules({
                  canary: {
                    ...(rules.canary ?? {}),
                    observation: event.target.value,
                  },
                })
              }
              placeholder="15m"
            />
          </Field>
        </div>
        <CheckboxField
          checked={rules.rollback?.automatic ?? true}
          onChange={(automatic) =>
            updateRules({ rollback: { ...(rules.rollback ?? {}), automatic } })
          }
          label="Automatically roll back when a threshold is exceeded"
        />
        <div className="grid gap-3 sm:grid-cols-3">
          <DecimalField
            label="Max infra failure rate"
            value={rules.rollback?.max_infrastructure_failure_rate ?? 0.02}
            onChange={(max_infrastructure_failure_rate) =>
              updateRules({
                rollback: {
                  ...(rules.rollback ?? {}),
                  max_infrastructure_failure_rate,
                },
              })
            }
          />
          <DecimalField
            label="Max duration regression"
            value={rules.rollback?.max_duration_regression ?? 0.2}
            onChange={(max_duration_regression) =>
              updateRules({
                rollback: {
                  ...(rules.rollback ?? {}),
                  max_duration_regression,
                },
              })
            }
          />
          <NumberField
            label="Max parser failures"
            value={rules.rollback?.max_parser_failures ?? 0}
            onChange={(max_parser_failures) =>
              updateRules({
                rollback: {
                  ...(rules.rollback ?? {}),
                  max_parser_failures,
                },
              })
            }
          />
        </div>
      </FormSection>

      <FormSection title="Retention and notifications">
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Artifact retention">
            <Input
              value={rules.retention?.artifacts ?? ""}
              onChange={(event) =>
                updateRules({
                  retention: {
                    ...(rules.retention ?? {}),
                    artifacts: event.target.value,
                  },
                })
              }
              placeholder="2160h"
            />
          </Field>
          <Field label="Log retention">
            <Input
              value={rules.retention?.logs ?? ""}
              onChange={(event) =>
                updateRules({
                  retention: {
                    ...(rules.retention ?? {}),
                    logs: event.target.value,
                  },
                })
              }
              placeholder="720h"
            />
          </Field>
        </div>
        <Field label="Notification destinations">
          <Input
            value={rules.notifications?.destinations?.join(", ") ?? ""}
            onChange={(event) =>
              updateRules({
                notifications: {
                  destinations: event.target.value
                    .split(",")
                    .map((value) => value.trim())
                    .filter(Boolean),
                },
              })
            }
            placeholder="security-webhook, release-email, siem"
          />
        </Field>
      </FormSection>
    </fieldset>
  );
}

function ValidationResult({
  result,
}: {
  result: NonNullable<ScannerPolicy["validation"]>;
}) {
  return (
    <div
      className={`rounded-lg border p-4 ${
        result.valid
          ? "border-emerald-500/30 bg-emerald-500/10"
          : "border-red-500/30 bg-red-500/10"
      }`}
      role="status"
    >
      <div className="flex items-center gap-2">
        <CheckCircle2Icon
          className={`size-4 ${result.valid ? "text-emerald-300" : "text-red-300"}`}
          aria-hidden="true"
        />
        <p className="text-sm font-medium">
          {result.valid ? "Policy is valid" : "Policy has blocking errors"}
        </p>
      </div>
      {[...(result.errors ?? []), ...(result.warnings ?? [])].length ? (
        <ul className="mt-2 text-xs text-muted-foreground">
          {result.errors?.map((error) => (
            <li key={error}>• {safeDisplayText(error, 1_024)}</li>
          ))}
          {result.warnings?.map((warning) => (
            <li key={warning}>• {safeDisplayText(warning, 1_024)}</li>
          ))}
        </ul>
      ) : null}
      {result.valid && result.next_execution ? (
        <div className="mt-3 border-t border-current/15 pt-3 text-xs">
          <p className="font-medium">Next trusted server-clock execution</p>
          <dl className="mt-2 grid gap-1 sm:grid-cols-2">
            {result.next_execution.daily_discovery ? (
              <div>
                <dt className="inline text-muted-foreground">
                  Daily discovery:{" "}
                </dt>
                <dd className="inline">
                  <Timestamp value={result.next_execution.daily_discovery} />
                </dd>
              </div>
            ) : null}
            {result.next_execution.weekly_candidate ? (
              <div>
                <dt className="inline text-muted-foreground">
                  Weekly candidate:{" "}
                </dt>
                <dd className="inline">
                  <Timestamp value={result.next_execution.weekly_candidate} />
                </dd>
              </div>
            ) : null}
          </dl>
          {(result.next_execution.maintenance_windows ?? []).length ? (
            <ul className="mt-2 space-y-1 text-muted-foreground">
              {result.next_execution.maintenance_windows?.map((window) => (
                <li key={window.id || window.name}>
                  {window.name}: <Timestamp value={window.at} /> for{" "}
                  {window.duration}
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function FormSection({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-lg border border-border/70 bg-card p-4">
      <h2 className="text-sm font-semibold">{title}</h2>
      {description ? (
        <p className="mt-1 text-xs text-muted-foreground">{description}</p>
      ) : null}
      <div className="mt-4 space-y-3">{children}</div>
    </section>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block space-y-1.5">
      <span className="text-xs font-medium">{label}</span>
      {children}
    </label>
  );
}

function NumberField({
  label,
  value,
  onChange,
}: {
  label: string;
  value: number;
  onChange: (value: number) => void;
}) {
  return (
    <Field label={label}>
      <Input
        type="number"
        min={0}
        value={value}
        onChange={(event) => onChange(Number(event.target.value))}
      />
    </Field>
  );
}

function DecimalField({
  label,
  value,
  onChange,
}: {
  label: string;
  value: number;
  onChange: (value: number) => void;
}) {
  return (
    <Field label={label}>
      <Input
        type="number"
        min={0}
        step={0.01}
        value={value}
        onChange={(event) => onChange(Number(event.target.value))}
      />
    </Field>
  );
}

function CheckboxField({
  checked,
  onChange,
  label,
}: {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label: string;
}) {
  return (
    <label className="flex items-start gap-2 text-sm">
      <input
        type="checkbox"
        checked={checked}
        onChange={(event) => onChange(event.target.checked)}
        className="mt-0.5 size-4 rounded border-border accent-primary"
      />
      <span>{label}</span>
    </label>
  );
}

function CheckboxGroup<T extends string>({
  label,
  options,
  values,
  onChange,
}: {
  label: string;
  options: T[];
  values: T[];
  onChange: (values: T[]) => void;
}) {
  return (
    <fieldset>
      <legend className="mb-2 text-xs font-medium">{label}</legend>
      <div className="flex flex-wrap gap-x-4 gap-y-2">
        {options.map((option) => (
          <CheckboxField
            key={option}
            checked={values.includes(option)}
            onChange={(checked) =>
              onChange(
                checked
                  ? [...values, option]
                  : values.filter((value) => value !== option),
              )
            }
            label={humanize(option)}
          />
        ))}
      </div>
    </fieldset>
  );
}

function policyToDraft(policy: ScannerPolicy): PolicyDraft {
  return {
    schedule: parseJson<PolicySchedule>(policy.schedule, {}),
    rules: parseJson<PolicyRules>(policy.rules, {}),
  };
}

function defaultDraft(): PolicyDraft {
  return {
    schedule: {
      timezone: "UTC",
      daily_discovery: {
        frequency: "daily",
        at: "02:00",
        jitter: "20m",
        catch_up: "6h",
      },
      weekly_candidate: {
        frequency: "weekly",
        weekday: "Sunday",
        at: "03:00",
        jitter: "20m",
        catch_up: "48h",
      },
      maximum_stable_image_age: "168h0m0s",
      force_weekly_rebuild: false,
      maintenance_windows: [
        {
          name: "Weekly security maintenance",
          cron: "0 3 * * 0",
          duration: "2h",
        },
      ],
    },
    rules: {
      schema_version: "wolf.scanner-policy/v1",
      revision: 1,
      approval_mode: "manual",
      required_approvals: 1,
      separate_creator: true,
      auto_promote_risks: ["low"],
      auto_promote_changes: ["rebuild_only", "patch"],
      required_gates: GATES,
      allow_exceptions: { vulnerability: true, license: true },
      exception_max_age: "720h",
      canary: { size: 1, minimum_samples: 10, observation: "15m" },
      rollback: {
        automatic: true,
        max_infrastructure_failure_rate: 0.02,
        max_duration_regression: 0.2,
        max_parser_failures: 0,
      },
      retention: { artifacts: "2160h", logs: "720h" },
      notifications: { destinations: [] },
    },
  };
}

function newMaintenanceWindowID(): string {
  if (typeof globalThis.crypto?.randomUUID === "function") {
    return `maintenance-${globalThis.crypto.randomUUID()}`;
  }
  return `maintenance-${Date.now().toString(36)}`;
}

function createObjectDiff(before: PolicyDraft, after: PolicyDraft): string {
  if (JSON.stringify(before) === JSON.stringify(after)) {
    return "No changes from the active revision.";
  }
  return [
    "--- active-policy.json",
    "+++ proposed-policy.json",
    ...JSON.stringify(before, null, 2)
      .split("\n")
      .map((line) => `- ${line}`),
    ...JSON.stringify(after, null, 2)
      .split("\n")
      .map((line) => `+ ${line}`),
  ].join("\n");
}
