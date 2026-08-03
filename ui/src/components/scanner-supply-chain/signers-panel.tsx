import { memo, useId, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeftIcon,
  KeyRoundIcon,
  Loader2Icon,
  PlusIcon,
  RefreshCwIcon,
  ShieldCheckIcon,
  ShieldOffIcon,
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
import { useMe } from "@/lib/me";
import {
  createIdempotencyKey,
  scannerSupplyChainApi,
  type SignerProfile,
  type SignerProfileInput,
  type SignerProvider,
} from "@/lib/scanner-supply-chain";
import { useScannerReleaseCapabilities } from "./capabilities";
import {
  CodeValue,
  PageHeading,
  ResourceState,
  StatusBadge,
  Timestamp,
} from "./primitives";
import { safeErrorMessage } from "@/lib/safe-display";

const SECRET_REFERENCE =
  /^(secret|kubernetes|vault|file-ref):\/\/[A-Za-z0-9][A-Za-z0-9_./:@-]{0,511}$/;
const PRIVATE_MATERIAL = /private_key|begin private|[\r\n]/i;
const ALGORITHMS = [
  "ed25519",
  "ecdsa-p256-sha256",
  "rsa-pss-sha256",
  "cosign-keyless",
] as const;

type CustomerSignerProvider = Exclude<SignerProvider, "managed_keyless">;

type ProviderDefinition = {
  label: string;
  keyPrefix: string;
  example: string;
  guidance: string;
  workloadGuidance: string;
  recommendedWorkloadIdentity: boolean;
  algorithms: readonly string[];
};

const PROVIDERS: Record<CustomerSignerProvider, ProviderDefinition> = {
  aws_kms: {
    label: "AWS KMS",
    keyPrefix: "aws-kms://",
    example: "aws-kms://us-east-1/123456789012/alias/wolf-release",
    guidance:
      "Reference a KMS key or alias. Wolf records the provider-returned key version in signature evidence.",
    workloadGuidance:
      "Prefer IRSA or another short-lived AWS workload identity. Bind the expected role ARN below.",
    recommendedWorkloadIdentity: true,
    algorithms: ALGORITHMS.filter((value) => value !== "cosign-keyless"),
  },
  gcp_kms: {
    label: "Google Cloud KMS",
    keyPrefix: "gcp-kms://",
    example:
      "gcp-kms://projects/project/locations/global/keyRings/release/cryptoKeys/wolf",
    guidance:
      "Reference a CryptoKey; the adapter resolves and returns the exact CryptoKeyVersion.",
    workloadGuidance:
      "Prefer GKE Workload Identity Federation with a narrowly scoped service account.",
    recommendedWorkloadIdentity: true,
    algorithms: ALGORITHMS.filter((value) => value !== "cosign-keyless"),
  },
  azure_key_vault: {
    label: "Azure Key Vault / Managed HSM",
    keyPrefix: "azure-keyvault://",
    example: "azure-keyvault://vault-name/keys/wolf-release",
    guidance:
      "Reference a Key Vault or Managed HSM key. The adapter must return the exact key version.",
    workloadGuidance:
      "Prefer Azure federated workload identity with a vault-scoped managed identity.",
    recommendedWorkloadIdentity: true,
    algorithms: ALGORITHMS.filter((value) => value !== "cosign-keyless"),
  },
  pkcs11: {
    label: "PKCS#11 HSM",
    keyPrefix: "pkcs11:",
    example: "pkcs11:token=wolf;object=release;type=private",
    guidance:
      "Reference an HSM object only. Never put a PIN, private key, or module secret in the URI.",
    workloadGuidance:
      "Use an opaque file-ref://, secret://, vault://, or kubernetes:// reference for the PIN or session credential.",
    recommendedWorkloadIdentity: false,
    algorithms: ALGORITHMS.filter((value) => value !== "cosign-keyless"),
  },
  keyless: {
    label: "Workload keyless",
    keyPrefix: "workload://",
    example: "workload://cluster/wolf-system/scanner-release",
    guidance:
      "Bind the projected OIDC workload identity used for keyless signing.",
    workloadGuidance:
      "Workload identity is mandatory. Pin the certificate issuer, subject, and Rekor/Fulcio trust roots.",
    recommendedWorkloadIdentity: true,
    algorithms: ["cosign-keyless"],
  },
  offline: {
    label: "Offline signer",
    keyPrefix: "offline://",
    example: "offline://ceremony/release-2026-q3",
    guidance:
      "Reference an authorized offline ceremony or signing station; no key material enters Wolf.",
    workloadGuidance:
      "Use an opaque reference to the one-way request/result exchange authorization or mounted credential.",
    recommendedWorkloadIdentity: false,
    algorithms: ALGORITHMS.filter((value) => value !== "cosign-keyless"),
  },
};

export type SignerDraft = {
  name: string;
  provider: CustomerSignerProvider;
  algorithm: string;
  keyReference: string;
  secretReference: string;
  workloadIdentity: boolean;
  identity: string;
  issuer: string;
  subject: string;
  trustRootReference: string;
};

export const SignersPanel = memo(function SignersPanel({
  signerId,
  onSelectSigner,
}: {
  signerId?: string;
  onSelectSigner: (id?: string) => void;
}) {
  const me = useMe();
  if (me.isPending) {
    return (
      <div
        role="status"
        className="rounded-lg border border-border p-6 text-sm"
      >
        Loading signer administration access…
      </div>
    );
  }
  if (me.isError || me.data?.role !== "admin") {
    return (
      <div
        role="alert"
        className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm text-destructive-text"
      >
        Signer profiles require administrator access. No signing references were
        loaded.
      </div>
    );
  }
  return signerId ? (
    <SignerDetailView
      signerId={signerId}
      onBack={() => onSelectSigner(undefined)}
      onSelectSigner={onSelectSigner}
    />
  ) : (
    <SignerList onSelectSigner={onSelectSigner} />
  );
});

function SignerList({
  onSelectSigner,
}: {
  onSelectSigner: (id: string) => void;
}) {
  const {
    capabilities,
    permissions,
    loading: capabilitiesLoading,
  } = useScannerReleaseCapabilities();
  const [state, setState] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const signers = useQuery({
    queryKey: ["scanner-supply-chain", "signers", "all"],
    queryFn: () => scannerSupplyChainApi.signers(true),
  });
  const items = (signers.data?.items ?? []).filter(
    (profile) => !state || profile.state === state,
  );
  const createUnavailableReason = capabilitiesLoading
    ? "Checking release-management capabilities."
    : !permissions.administer
      ? "Supply-chain administrator access is required."
      : !capabilities.candidates
        ? "Creating signer profiles requires candidate mode or higher."
        : undefined;

  return (
    <div className="space-y-5">
      <PageHeading
        title="Signing profiles"
        description="Customer KMS, HSM, keyless, and offline signing identities. References are write-only and always masked after submission."
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              onClick={() => setCreateOpen(true)}
              disabled={Boolean(createUnavailableReason)}
              title={createUnavailableReason}
            >
              <PlusIcon aria-hidden="true" /> Create signing profile
            </Button>
            <select
              aria-label="Filter signing profiles by state"
              value={state}
              onChange={(event) => setState(event.target.value)}
              className="h-10 rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              <option value="">All states</option>
              <option value="active">Active</option>
              <option value="disabled">Rotated / disabled</option>
              <option value="revoked">Revoked</option>
            </select>
          </div>
        }
      />

      <SecretBoundaryNotice />

      <ResourceState
        loading={signers.isPending}
        error={signers.error}
        empty={items.length === 0}
        emptyTitle={
          state ? `No ${state} signer profiles` : "No signer profiles"
        }
        emptyDescription="Create a customer profile or configure deployment-owned managed keyless signing."
        onRetry={() => signers.refetch()}
      >
        <div className="overflow-hidden rounded-lg border border-border/70 bg-card">
          <div
            className="overflow-x-auto focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
            role="region"
            tabIndex={0}
            aria-label="Signing profile inventory"
          >
            <table className="w-full min-w-[58rem] text-sm">
              <thead className="bg-muted/20 text-left text-xs text-muted-foreground">
                <tr>
                  <th className="px-4 py-2 font-medium">Profile</th>
                  <th className="px-4 py-2 font-medium">Provider</th>
                  <th className="px-4 py-2 font-medium">Identity binding</th>
                  <th className="px-4 py-2 font-medium">Authentication</th>
                  <th className="px-4 py-2 font-medium">State</th>
                  <th className="px-4 py-2 font-medium">Revision</th>
                  <th className="px-4 py-2 font-medium">Updated</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border/50">
                {items.map((profile) => (
                  <tr
                    key={profile.id}
                    tabIndex={0}
                    aria-label={`Open signer ${safeBoundValue(profile.name)}`}
                    className="cursor-pointer [content-visibility:auto] [contain-intrinsic-size:0_72px] hover:bg-muted/15"
                    onClick={() => onSelectSigner(profile.id)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter" || event.key === " ") {
                        event.preventDefault();
                        onSelectSigner(profile.id);
                      }
                    }}
                  >
                    <td className="px-4 py-3">
                      <p className="font-medium">
                        {safeBoundValue(profile.name)}
                      </p>
                      <p className="mt-1 font-mono text-[11px] text-muted-foreground">
                        {profile.id}
                      </p>
                    </td>
                    <td className="px-4 py-3">
                      <p>{providerLabel(profile.provider)}</p>
                      {profile.provider === "managed_keyless" ? (
                        <p className="mt-1 text-xs text-primary">
                          Deployment owned
                        </p>
                      ) : null}
                    </td>
                    <td className="max-w-64 truncate px-4 py-3 text-xs">
                      {safeBoundValue(profile.identity)}
                    </td>
                    <td className="px-4 py-3 text-xs">
                      {profile.workload_identity
                        ? "Workload identity"
                        : profile.secret_reference_configured
                          ? "Opaque secret reference"
                          : "Unreported"}
                    </td>
                    <td className="px-4 py-3">
                      <StatusBadge state={profile.state} />
                    </td>
                    <td className="px-4 py-3 font-mono text-xs">
                      {profile.revision}
                    </td>
                    <td className="px-4 py-3 text-xs text-muted-foreground">
                      <Timestamp value={profile.updated_at} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </ResourceState>

      {createOpen ? (
        <SignerProfileDialog
          mode="create"
          onClose={() => setCreateOpen(false)}
          onSuccess={(profile) => {
            setCreateOpen(false);
            onSelectSigner(profile.id);
          }}
        />
      ) : null}
    </div>
  );
}

function SignerDetailView({
  signerId,
  onBack,
  onSelectSigner,
}: {
  signerId: string;
  onBack: () => void;
  onSelectSigner: (id: string) => void;
}) {
  const queryClient = useQueryClient();
  const {
    capabilities,
    permissions,
    loading: capabilitiesLoading,
  } = useScannerReleaseCapabilities();
  const [rotateOpen, setRotateOpen] = useState(false);
  const [revokeOpen, setRevokeOpen] = useState(false);
  const signer = useQuery({
    queryKey: ["scanner-supply-chain", "signer", signerId],
    queryFn: () => scannerSupplyChainApi.signer(signerId),
  });
  const history = useQuery({
    queryKey: ["scanner-supply-chain", "signers", "all"],
    queryFn: () => scannerSupplyChainApi.signers(true),
  });
  const profile = signer.data?.signer;
  const managed = profile?.provider === "managed_keyless";
  const actionUnavailableReason = capabilitiesLoading
    ? "Checking release-management capabilities."
    : !permissions.administer
      ? "Supply-chain administrator access is required."
      : !capabilities.candidates
        ? "Signer changes require candidate mode or higher."
        : !signer.data?.etag
          ? "The server did not return an ETag. Reload before changing this profile."
          : profile?.state !== "active"
            ? "Only active signer profiles can be rotated or revoked."
            : managed
              ? "Managed keyless is deployment-owned and cannot be changed here."
              : undefined;
  const chain = useMemo(
    () => (profile ? signerHistory(profile, history.data?.items ?? []) : []),
    [history.data?.items, profile],
  );
  const revoke = useMutation({
    mutationFn: ({
      reason,
      idempotencyKey,
    }: {
      reason: string;
      idempotencyKey: string;
    }) => {
      if (!signer.data?.etag) {
        throw new Error("Reload the signer profile to obtain an ETag.");
      }
      return scannerSupplyChainApi.revokeSigner(
        signerId,
        reason,
        signer.data.etag,
        idempotencyKey,
      );
    },
    onSuccess: () => {
      toast.success("Signer profile revoked");
      setRevokeOpen(false);
      queryClient.invalidateQueries({
        queryKey: ["scanner-supply-chain", "signer", signerId],
      });
      queryClient.invalidateQueries({
        queryKey: ["scanner-supply-chain", "signers"],
      });
    },
  });

  return (
    <div className="space-y-5">
      <Button type="button" variant="ghost" size="sm" onClick={onBack}>
        <ArrowLeftIcon aria-hidden="true" /> All signing profiles
      </Button>
      <ResourceState
        loading={signer.isPending}
        error={signer.error}
        onRetry={() => signer.refetch()}
        variant="cards"
      >
        {profile ? (
          <>
            <PageHeading
              title={safeBoundValue(profile.name)}
              description={
                <span className="flex flex-wrap items-center gap-2">
                  <StatusBadge state={profile.state} />
                  <span>{providerLabel(profile.provider)}</span>
                  <span>Revision {profile.revision}</span>
                </span>
              }
              actions={
                <div className="flex flex-wrap gap-2">
                  <Button
                    type="button"
                    variant="outline"
                    onClick={() => setRotateOpen(true)}
                    disabled={Boolean(actionUnavailableReason)}
                    title={actionUnavailableReason}
                  >
                    <RefreshCwIcon aria-hidden="true" /> Rotate
                  </Button>
                  <Button
                    type="button"
                    variant="destructive"
                    onClick={() => setRevokeOpen(true)}
                    disabled={Boolean(actionUnavailableReason)}
                    title={actionUnavailableReason}
                  >
                    <ShieldOffIcon aria-hidden="true" /> Revoke
                  </Button>
                </div>
              }
            />

            {managed ? (
              <div className="rounded-lg border border-primary/40 bg-primary/10 p-4 text-sm">
                <p className="font-medium text-primary">
                  Deployment-owned managed keyless profile
                </p>
                <p className="mt-1 text-xs text-muted-foreground">
                  Inspect its bound identity and trust policy here. Rotation and
                  revocation are controlled by deployment configuration, not the
                  customer signer administration API.
                </p>
              </div>
            ) : null}
            {profile.state === "disabled" ? (
              <div className="rounded-lg border border-amber-500/40 bg-amber-500/10 p-4 text-sm text-amber-100">
                This revision was disabled by atomic rotation. It remains
                visible so historical signatures can be verified.
              </div>
            ) : null}
            {profile.state === "revoked" ? (
              <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm text-destructive-text">
                <p className="font-medium">Revoked signer profile</p>
                <p className="mt-1">
                  New signing is blocked. Published signatures remain evidence,
                  but policy must reject this profile for new promotion and
                  import decisions.
                </p>
                <p className="mt-2 text-xs">
                  Reason:{" "}
                  {profile.revocation_reason
                    ? safeBoundValue(profile.revocation_reason)
                    : "Not reported"}
                </p>
              </div>
            ) : null}

            <SecretBoundaryNotice />

            <dl className="grid gap-4 rounded-lg border border-border/70 bg-card p-4 sm:grid-cols-2 xl:grid-cols-4">
              <DetailValue label="Key reference">
                <CodeValue>
                  {safeMaskedReference(profile.key_reference)}
                </CodeValue>
              </DetailValue>
              <DetailValue label="Secret authentication">
                {profile.secret_reference_configured
                  ? safeMaskedReference(profile.secret_reference)
                  : profile.workload_identity
                    ? "Not required — workload identity"
                    : "Not configured"}
              </DetailValue>
              <DetailValue label="Trust root policy">
                <CodeValue>
                  {safeMaskedReference(profile.trust_root_reference)}
                </CodeValue>
              </DetailValue>
              <DetailValue label="Algorithm">{profile.algorithm}</DetailValue>
              <DetailValue label="Bound identity">
                {safeBoundValue(profile.identity)}
              </DetailValue>
              <DetailValue label="Issuer">
                {safeBoundValue(profile.issuer)}
              </DetailValue>
              <DetailValue label="Subject">
                {safeBoundValue(profile.subject)}
              </DetailValue>
              <DetailValue label="Authentication">
                {profile.workload_identity
                  ? "Workload identity"
                  : "Opaque secret reference"}
              </DetailValue>
            </dl>

            <section className="rounded-lg border border-border/70 bg-card">
              <div className="border-b border-border/70 px-4 py-3">
                <h2 className="font-medium">Rotation and revocation history</h2>
                <p className="mt-1 text-xs text-muted-foreground">
                  Prior revisions are retained for evidence verification.
                  Rotation creates a new profile ID and disables the prior ID.
                </p>
              </div>
              {history.isPending ? (
                <p role="status" className="p-4 text-sm text-muted-foreground">
                  Loading signer history…
                </p>
              ) : (
                <ol className="divide-y divide-border/50">
                  {chain.map((entry) => (
                    <li
                      key={entry.id}
                      className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center"
                    >
                      <div className="min-w-0 flex-1">
                        <button
                          type="button"
                          onClick={() => onSelectSigner(entry.id)}
                          className="font-mono text-xs text-primary hover:underline"
                        >
                          {entry.id}
                        </button>
                        <p className="mt-1 text-xs text-muted-foreground">
                          Revision {entry.revision} · updated{" "}
                          <Timestamp value={entry.updated_at} />
                        </p>
                      </div>
                      <StatusBadge state={entry.state} />
                      {entry.revoked_at ? (
                        <span className="text-xs text-muted-foreground">
                          Revoked <Timestamp value={entry.revoked_at} />
                        </span>
                      ) : null}
                    </li>
                  ))}
                </ol>
              )}
            </section>

            {actionUnavailableReason ? (
              <p className="text-xs text-amber-300">
                Signer changes unavailable: {actionUnavailableReason}
              </p>
            ) : null}
          </>
        ) : null}
      </ResourceState>

      {profile && rotateOpen ? (
        <SignerProfileDialog
          mode="rotate"
          current={profile}
          etag={signer.data?.etag}
          onClose={() => setRotateOpen(false)}
          onSuccess={(replacement) => {
            setRotateOpen(false);
            onSelectSigner(replacement.id);
          }}
        />
      ) : null}
      {profile && revokeOpen ? (
        <RevokeSignerDialog
          profile={profile}
          pending={revoke.isPending}
          error={revoke.error}
          onClose={() => setRevokeOpen(false)}
          onConfirm={(reason, idempotencyKey) =>
            revoke.mutate({ reason, idempotencyKey })
          }
        />
      ) : null}
    </div>
  );
}

function SignerProfileDialog({
  mode,
  current,
  etag,
  onClose,
  onSuccess,
}: {
  mode: "create" | "rotate";
  current?: SignerProfile;
  etag?: string;
  onClose: () => void;
  onSuccess: (profile: SignerProfile) => void;
}) {
  const queryClient = useQueryClient();
  const nameId = useId();
  const providerId = useId();
  const algorithmId = useId();
  const keyId = useId();
  const secretId = useId();
  const identityId = useId();
  const issuerId = useId();
  const subjectId = useId();
  const trustId = useId();
  const confirmationId = useId();
  const initialProvider =
    current && current.provider !== "managed_keyless"
      ? current.provider
      : "aws_kms";
  const initialDefinition = PROVIDERS[initialProvider];
  const [draft, setDraft] = useState<SignerDraft>(() => ({
    name: current?.name ?? "",
    provider: initialProvider,
    algorithm:
      current?.algorithm ?? initialDefinition.algorithms[0] ?? "ed25519",
    keyReference: "",
    secretReference: "",
    workloadIdentity:
      current?.workload_identity ??
      initialDefinition.recommendedWorkloadIdentity,
    identity: current?.identity ?? "",
    issuer: current?.issuer ?? "",
    subject: current?.subject ?? "",
    trustRootReference: "",
  }));
  const [confirmation, setConfirmation] = useState("");
  const [idempotencyKey] = useState(() => createIdempotencyKey());
  const errors = validateSignerDraft(draft);
  const definition = PROVIDERS[draft.provider];
  const save = useMutation({
    mutationFn: () => {
      const input = signerInput(draft);
      if (mode === "create") {
        return scannerSupplyChainApi.createSigner(input, idempotencyKey);
      }
      if (!current || !etag) {
        throw new Error(
          "Reload the current profile before rotating; an ETag is required.",
        );
      }
      return scannerSupplyChainApi.rotateSigner(
        current.id,
        input,
        etag,
        idempotencyKey,
      );
    },
    onSuccess: (profile) => {
      toast.success(
        mode === "create"
          ? "Signing profile created"
          : "Signing profile rotated atomically",
      );
      queryClient.invalidateQueries({
        queryKey: ["scanner-supply-chain", "signers"],
      });
      if (current) {
        queryClient.invalidateQueries({
          queryKey: ["scanner-supply-chain", "signer", current.id],
        });
      }
      onSuccess(profile);
    },
  });
  const update = <K extends keyof SignerDraft>(key: K, value: SignerDraft[K]) =>
    setDraft((previous) => ({ ...previous, [key]: value }));
  const setProvider = (provider: CustomerSignerProvider) => {
    const next = PROVIDERS[provider];
    setDraft((previous) => ({
      ...previous,
      provider,
      algorithm: next.algorithms.includes(previous.algorithm)
        ? previous.algorithm
        : (next.algorithms[0] ?? "ed25519"),
      workloadIdentity: next.recommendedWorkloadIdentity,
      keyReference: "",
      secretReference: "",
    }));
  };
  const canSubmit =
    Object.keys(errors).length === 0 &&
    (mode === "create" || confirmation.trim() === current?.id);

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending) onClose();
      }}
    >
      <DialogContent className="max-h-[94vh] max-w-3xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>
            {mode === "create"
              ? "Create signing profile"
              : "Rotate signing profile"}
          </DialogTitle>
          <DialogDescription>
            {mode === "create"
              ? "Register opaque provider references and exact identity constraints. Private key material is never accepted."
              : `Create a new revision and atomically disable ${current?.id}. All write-only references must be re-entered because reads are masked.`}
          </DialogDescription>
        </DialogHeader>

        {mode === "rotate" ? (
          <div className="rounded-lg border border-amber-500/50 bg-amber-500/10 p-4 text-sm text-amber-100">
            <p className="font-medium">Rotation changes the active signer.</p>
            <p className="mt-1 text-xs">
              Validate the replacement trust root before rotating. Historical
              signatures remain bound to the disabled prior profile; new signing
              uses the new profile ID immediately.
            </p>
          </div>
        ) : null}

        <SecretBoundaryNotice />

        <div className="grid gap-4 sm:grid-cols-2">
          <FormField label="Profile name" htmlFor={nameId}>
            <Input
              id={nameId}
              value={draft.name}
              onChange={(event) => update("name", event.target.value)}
              aria-invalid={Boolean(errors.name)}
            />
          </FormField>
          <FormField label="Provider" htmlFor={providerId}>
            <select
              id={providerId}
              value={draft.provider}
              onChange={(event) =>
                setProvider(event.target.value as CustomerSignerProvider)
              }
              className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              {Object.entries(PROVIDERS).map(([value, provider]) => (
                <option key={value} value={value}>
                  {provider.label}
                </option>
              ))}
            </select>
          </FormField>
          <FormField label="Algorithm" htmlFor={algorithmId}>
            <select
              id={algorithmId}
              value={draft.algorithm}
              onChange={(event) => update("algorithm", event.target.value)}
              className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              {definition.algorithms.map((algorithm) => (
                <option key={algorithm} value={algorithm}>
                  {algorithm}
                </option>
              ))}
            </select>
          </FormField>
          <FormField label="Authentication" htmlFor={`${providerId}-workload`}>
            <label
              htmlFor={`${providerId}-workload`}
              className="flex min-h-10 items-center gap-2 rounded-md border border-input px-3 text-sm"
            >
              <input
                id={`${providerId}-workload`}
                type="checkbox"
                checked={draft.workloadIdentity}
                disabled={draft.provider === "keyless"}
                onChange={(event) =>
                  update("workloadIdentity", event.target.checked)
                }
              />
              Use workload identity
            </label>
          </FormField>
        </div>

        <section className="rounded-lg border border-primary/30 bg-primary/5 p-4 text-sm">
          <p className="font-medium">{definition.label} guidance</p>
          <p className="mt-1 text-xs text-muted-foreground">
            {definition.guidance}
          </p>
          <p className="mt-2 text-xs text-muted-foreground">
            {definition.workloadGuidance}
          </p>
        </section>

        <FormField
          label="Opaque key reference"
          htmlFor={keyId}
          hint={`Required prefix: ${definition.keyPrefix}. Example: ${definition.example}`}
        >
          <Input
            id={keyId}
            value={draft.keyReference}
            onChange={(event) => update("keyReference", event.target.value)}
            placeholder={definition.example}
            autoComplete="off"
            spellCheck={false}
            className="font-mono text-xs"
            aria-invalid={Boolean(errors.keyReference)}
          />
        </FormField>

        {!draft.workloadIdentity ? (
          <FormField
            label="Opaque authentication secret reference"
            htmlFor={secretId}
            hint="Use secret://, kubernetes://, vault://, or file-ref://. Enter a reference, never a credential or PIN."
          >
            <Input
              id={secretId}
              type="password"
              value={draft.secretReference}
              onChange={(event) =>
                update("secretReference", event.target.value)
              }
              placeholder="kubernetes://wolf-system/signer-credentials"
              autoComplete="new-password"
              spellCheck={false}
              aria-invalid={Boolean(errors.secretReference)}
            />
          </FormField>
        ) : null}

        <div className="grid gap-4 sm:grid-cols-2">
          <FormField label="Bound identity" htmlFor={identityId}>
            <Input
              id={identityId}
              value={draft.identity}
              onChange={(event) => update("identity", event.target.value)}
              aria-invalid={Boolean(errors.identity)}
            />
          </FormField>
          <FormField label="Issuer URI" htmlFor={issuerId}>
            <Input
              id={issuerId}
              type="url"
              value={draft.issuer}
              onChange={(event) => update("issuer", event.target.value)}
              placeholder="https://issuer.example"
              aria-invalid={Boolean(errors.issuer)}
            />
          </FormField>
        </div>
        <FormField label="Bound subject" htmlFor={subjectId}>
          <Input
            id={subjectId}
            value={draft.subject}
            onChange={(event) => update("subject", event.target.value)}
            aria-invalid={Boolean(errors.subject)}
          />
        </FormField>
        <FormField
          label="Opaque trust-root policy reference"
          htmlFor={trustId}
          hint="Reference the policy-pinned public verification roots. Do not paste a certificate, public-key block, or secret."
        >
          <Input
            id={trustId}
            type="password"
            value={draft.trustRootReference}
            onChange={(event) =>
              update("trustRootReference", event.target.value)
            }
            placeholder="kubernetes://wolf-system/scanner-signing-roots"
            autoComplete="new-password"
            spellCheck={false}
            aria-invalid={Boolean(errors.trustRootReference)}
          />
        </FormField>

        {mode === "rotate" ? (
          <FormField
            label={`Type ${current?.id} to confirm rotation`}
            htmlFor={confirmationId}
          >
            <Input
              id={confirmationId}
              value={confirmation}
              onChange={(event) => setConfirmation(event.target.value)}
              autoComplete="off"
            />
          </FormField>
        ) : null}

        {Object.keys(errors).length > 0 ? (
          <div
            role="alert"
            className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive-text"
          >
            <p className="font-medium">Complete the signer profile safely:</p>
            <ul className="mt-1 list-disc space-y-1 pl-5 text-xs">
              {Object.values(errors).map((message) => (
                <li key={message}>{message}</li>
              ))}
            </ul>
          </div>
        ) : null}
        {save.error ? (
          <div
            role="alert"
            className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive-text"
          >
            {safeErrorMessage(
              save.error,
              "The signer profile could not be saved.",
            )}
            <p className="mt-1 text-xs">
              Correct the issue and submit again; this form retains one
              idempotency key while it remains open.
            </p>
          </div>
        ) : null}

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={onClose}
            disabled={save.isPending}
          >
            Cancel
          </Button>
          <Button
            type="button"
            onClick={() => save.mutate()}
            disabled={!canSubmit || save.isPending}
          >
            {save.isPending ? (
              <Loader2Icon className="animate-spin" aria-hidden="true" />
            ) : mode === "create" ? (
              <KeyRoundIcon aria-hidden="true" />
            ) : (
              <RefreshCwIcon aria-hidden="true" />
            )}
            {mode === "create" ? "Create profile" : "Rotate profile"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function RevokeSignerDialog({
  profile,
  pending,
  error,
  onClose,
  onConfirm,
}: {
  profile: SignerProfile;
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onConfirm: (reason: string, idempotencyKey: string) => void;
}) {
  const reasonId = useId();
  const confirmationId = useId();
  const [reason, setReason] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [idempotencyKey] = useState(() => createIdempotencyKey());
  const valid =
    reason.trim().length >= 3 &&
    reason.trim().length <= 500 &&
    confirmation.trim() === profile.id;
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !pending) onClose();
      }}
    >
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>Revoke signing profile?</DialogTitle>
          <DialogDescription>
            Revocation immediately blocks this profile from new signing work.
          </DialogDescription>
        </DialogHeader>
        <div className="rounded-lg border border-destructive/50 bg-destructive/10 p-4 text-sm text-destructive-text">
          <p className="font-medium">This is not deletion.</p>
          <p className="mt-1 text-xs">
            Historical signatures remain immutable evidence, but this identity
            must be rejected for new releases, promotions, and imports. Use
            rotation for a planned replacement; revoke for retirement or
            compromise.
          </p>
        </div>
        <FormField label="Revocation reason" htmlFor={reasonId}>
          <textarea
            id={reasonId}
            value={reason}
            onChange={(event) => setReason(event.target.value)}
            rows={3}
            maxLength={500}
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
          <p className="text-right text-xs text-muted-foreground">
            {reason.length}/500
          </p>
        </FormField>
        <FormField
          label={`Type ${profile.id} to confirm revocation`}
          htmlFor={confirmationId}
        >
          <Input
            id={confirmationId}
            value={confirmation}
            onChange={(event) => setConfirmation(event.target.value)}
            autoComplete="off"
          />
        </FormField>
        {error ? (
          <div
            role="alert"
            className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive-text"
          >
            {safeErrorMessage(
              error,
              "The signer profile could not be revoked.",
            )}
          </div>
        ) : null}
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
            variant="destructive"
            disabled={!valid || pending}
            onClick={() => onConfirm(reason.trim(), idempotencyKey)}
          >
            {pending ? (
              <Loader2Icon className="animate-spin" aria-hidden="true" />
            ) : (
              <ShieldOffIcon aria-hidden="true" />
            )}
            Revoke profile
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function SecretBoundaryNotice() {
  return (
    <div className="flex items-start gap-3 rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-4 text-sm">
      <ShieldCheckIcon
        className="mt-0.5 size-5 shrink-0 text-emerald-700 dark:text-emerald-400"
        aria-hidden="true"
      />
      <div>
        <p className="font-medium text-emerald-800 dark:text-emerald-200">
          No private key material crosses this control plane
        </p>
        <p className="mt-1 text-xs text-muted-foreground">
          Enter only provider URIs and opaque secret or trust-policy references.
          Wolf masks every stored reference on read and exposes only the bound
          issuer, subject, and identity constraints.
        </p>
      </div>
    </div>
  );
}

function FormField({
  label,
  htmlFor,
  hint,
  children,
}: {
  label: string;
  htmlFor: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
      {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
    </div>
  );
}

function DetailValue({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 break-all text-sm">{children}</dd>
    </div>
  );
}

function providerLabel(provider: SignerProvider): string {
  if (provider === "managed_keyless") return "Managed keyless";
  return PROVIDERS[provider].label;
}

export function safeMaskedReference(value: string | null | undefined): string {
  if (!value) return "Not configured";
  if (
    /^(aws-kms|gcp-kms|azure-keyvault|workload|offline|managed-keyless|secret|kubernetes|vault|file-ref):\/\/\*\*\*$/.test(
      value,
    ) ||
    value === "pkcs11:***"
  ) {
    return value;
  }
  return "Opaque reference configured";
}

function safeBoundValue(value: string): string {
  const normalized = value.trim();
  if (
    !normalized ||
    normalized.length > 2_048 ||
    containsPrivateMaterial(normalized)
  ) {
    return "Bound value withheld";
  }
  return normalized;
}

export function validateSignerDraft(
  draft: SignerDraft,
): Record<string, string> {
  const errors: Record<string, string> = {};
  const definition = PROVIDERS[draft.provider];
  if (!draft.name.trim()) errors.name = "Profile name is required.";
  if (!definition.algorithms.includes(draft.algorithm)) {
    errors.algorithm = "Choose an algorithm supported by this provider form.";
  }
  const keyReference = draft.keyReference.trim();
  if (
    !keyReference.startsWith(definition.keyPrefix) ||
    containsPrivateMaterial(keyReference)
  ) {
    errors.keyReference = `Key reference must be an opaque ${definition.keyPrefix} URI with no key material.`;
  }
  if (
    !draft.workloadIdentity &&
    (!SECRET_REFERENCE.test(draft.secretReference.trim()) ||
      containsPrivateMaterial(draft.secretReference))
  ) {
    errors.secretReference =
      "Authentication requires a valid opaque secret reference when workload identity is disabled.";
  }
  if (draft.provider === "keyless" && !draft.workloadIdentity) {
    errors.workloadIdentity =
      "Workload keyless signing requires workload identity.";
  }
  if (!draft.identity.trim()) errors.identity = "Bound identity is required.";
  if (!draft.subject.trim()) errors.subject = "Bound subject is required.";
  try {
    const issuer = new URL(draft.issuer.trim());
    if (!issuer.protocol || !issuer.host) throw new Error("not absolute");
  } catch {
    errors.issuer = "Issuer must be an absolute URI.";
  }
  if (
    !SECRET_REFERENCE.test(draft.trustRootReference.trim()) ||
    containsPrivateMaterial(draft.trustRootReference)
  ) {
    errors.trustRootReference =
      "Trust roots require a valid opaque secret or policy reference.";
  }
  return errors;
}

function containsPrivateMaterial(value: string): boolean {
  return PRIVATE_MATERIAL.test(value) || value.includes("\u0000");
}

function signerInput(draft: SignerDraft): SignerProfileInput {
  return {
    name: draft.name.trim(),
    provider: draft.provider,
    algorithm: draft.algorithm,
    key_reference: draft.keyReference.trim(),
    ...(draft.secretReference.trim()
      ? { secret_reference: draft.secretReference.trim() }
      : {}),
    workload_identity: draft.workloadIdentity,
    identity: draft.identity.trim(),
    issuer: draft.issuer.trim(),
    subject: draft.subject.trim(),
    trust_root_reference: draft.trustRootReference.trim(),
  };
}

function signerHistory(
  current: SignerProfile,
  all: SignerProfile[],
): SignerProfile[] {
  const byId = new Map(all.map((profile) => [profile.id, profile]));
  byId.set(current.id, current);
  const connected = new Map<string, SignerProfile>();
  let ancestor: SignerProfile | undefined = current;
  while (ancestor && !connected.has(ancestor.id)) {
    connected.set(ancestor.id, ancestor);
    ancestor = ancestor.rotated_from_id
      ? byId.get(ancestor.rotated_from_id)
      : undefined;
  }
  let changed = true;
  while (changed) {
    changed = false;
    for (const profile of byId.values()) {
      if (
        profile.rotated_from_id &&
        connected.has(profile.rotated_from_id) &&
        !connected.has(profile.id)
      ) {
        connected.set(profile.id, profile);
        changed = true;
      }
    }
  }
  return [...connected.values()].toSorted(
    (left, right) => left.revision - right.revision,
  );
}
