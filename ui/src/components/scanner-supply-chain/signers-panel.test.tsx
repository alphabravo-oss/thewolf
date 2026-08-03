import type { PropsWithChildren } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  safeMaskedReference,
  SignersPanel,
  validateSignerDraft,
  type SignerDraft,
} from "./signers-panel";
import { ScannerReleaseCapabilitiesBoundary } from "./capabilities";
import {
  scannerSupplyChainApi,
  type ScannerReleaseCapabilities,
  type SignerProfile,
} from "@/lib/scanner-supply-chain";

vi.mock("sonner", () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

const stableCapabilities: ScannerReleaseCapabilities = {
  mode: "stable_control",
  read: true,
  candidates: true,
  canary: true,
  stable_control: true,
};

const readOnlyCapabilities: ScannerReleaseCapabilities = {
  mode: "read_only",
  read: true,
  candidates: false,
  canary: false,
  stable_control: false,
};

const awsSigner: SignerProfile = {
  id: "signer-aws-1",
  name: "Production AWS signer",
  provider: "aws_kms",
  algorithm: "ecdsa-p256-sha256",
  key_reference: "aws-kms://***",
  secret_reference_configured: false,
  workload_identity: true,
  identity: "arn:aws:iam::123456789012:role/wolf-release",
  issuer: "https://sts.amazonaws.com",
  subject: "arn:aws:iam::123456789012:role/wolf-release",
  trust_root_reference: "kubernetes://***",
  state: "active",
  revision: 1,
  created_by: "security.admin@example.test",
  created_at: "2026-07-30T12:00:00Z",
  updated_at: "2026-07-30T12:00:00Z",
};

const managedSigner: SignerProfile = {
  ...awsSigner,
  id: "managed-keyless",
  name: "Wolf managed keyless",
  provider: "managed_keyless",
  algorithm: "cosign-keyless",
  key_reference: "managed-keyless://***",
  identity: "scanner-release@wolf.example",
  issuer: "https://token.actions.githubusercontent.com",
  subject: "repo:example/wolf:ref:refs/heads/main",
  trust_root_reference: "kubernetes://***",
  revision: 8,
};

function wrapper(
  capabilities: ScannerReleaseCapabilities = stableCapabilities,
  role = "admin",
) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  client.setQueryData(["me"], {
    id: "admin-1",
    email: "security.admin@example.test",
    role,
  });
  return function QueryWrapper({ children }: PropsWithChildren) {
    return (
      <QueryClientProvider client={client}>
        <ScannerReleaseCapabilitiesBoundary capabilities={capabilities}>
          {children}
        </ScannerReleaseCapabilitiesBoundary>
      </QueryClientProvider>
    );
  };
}

describe("enterprise signer profile administration", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("defensively refuses to render an unmasked reference", () => {
    expect(safeMaskedReference("aws-kms://***")).toBe("aws-kms://***");
    expect(
      safeMaskedReference(
        "aws-kms://us-east-1/123456789012/alias/private-production-key",
      ),
    ).toBe("Opaque reference configured");
    expect(safeMaskedReference("-----BEGIN PRIVATE KEY-----")).toBe(
      "Opaque reference configured",
    );
  });

  it.each([
    ["aws_kms", "aws-kms://region/account/key", true],
    ["gcp_kms", "gcp-kms://projects/p/locations/l/keyRings/r/cryptoKeys/k", true],
    ["azure_key_vault", "azure-keyvault://vault/keys/release", true],
    ["pkcs11", "pkcs11:token=wolf;object=release;type=private", false],
    ["keyless", "workload://cluster/ns/service-account", true],
    ["offline", "offline://ceremony/release-2026-q3", false],
  ] as const)(
    "validates %s as a provider-specific opaque-reference form",
    (provider, keyReference, workloadIdentity) => {
      const draft: SignerDraft = {
        name: "Release signer",
        provider,
        algorithm:
          provider === "keyless" ? "cosign-keyless" : "ed25519",
        keyReference,
        secretReference: workloadIdentity
          ? ""
          : "file-ref://signing/credential",
        workloadIdentity,
        identity: "release-signer@example.test",
        issuer: "https://issuer.example.test",
        subject: "release-signer@example.test",
        trustRootReference: "kubernetes://wolf-system/signing-roots",
      };
      expect(validateSignerDraft(draft)).toEqual({});
      expect(
        validateSignerDraft({
          ...draft,
          keyReference: "-----BEGIN PRIVATE KEY-----",
        }).keyReference,
      ).toMatch(/no key material/i);
    },
  );

  it("creates provider-specific profiles using only opaque references", async () => {
    vi.spyOn(scannerSupplyChainApi, "signers").mockResolvedValue({
      items: [awsSigner, managedSigner],
    });
    const created: SignerProfile = {
      ...awsSigner,
      id: "signer-aws-created",
      name: "Release signer east",
      revision: 1,
    };
    const create = vi
      .spyOn(scannerSupplyChainApi, "createSigner")
      .mockResolvedValue(created);
    const onSelect = vi.fn();

    render(<SignersPanel onSelectSigner={onSelect} />, {
      wrapper: wrapper(),
    });
    const open = await screen.findByRole("button", {
      name: "Create signing profile",
    });
    expect(open).toBeEnabled();
    fireEvent.click(open);
    const dialog = screen.getByRole("dialog", {
      name: "Create signing profile",
    });
    const provider = within(dialog).getByLabelText("Provider");
    expect(
      within(provider).getByRole("option", { name: "AWS KMS" }),
    ).toBeVisible();
    expect(
      within(provider).getByRole("option", {
        name: "Azure Key Vault / Managed HSM",
      }),
    ).toBeVisible();
    expect(
      within(provider).queryByRole("option", { name: /managed keyless/i }),
    ).not.toBeInTheDocument();

    fireEvent.change(within(dialog).getByLabelText("Profile name"), {
      target: { value: "Release signer east" },
    });
    fireEvent.change(within(dialog).getByLabelText("Opaque key reference"), {
      target: {
        value:
          "aws-kms://us-east-1/123456789012/alias/wolf-release-east",
      },
    });
    fireEvent.change(within(dialog).getByLabelText("Bound identity"), {
      target: { value: awsSigner.identity },
    });
    fireEvent.change(within(dialog).getByLabelText("Issuer URI"), {
      target: { value: awsSigner.issuer },
    });
    fireEvent.change(within(dialog).getByLabelText("Bound subject"), {
      target: { value: awsSigner.subject },
    });
    fireEvent.change(
      within(dialog).getByLabelText("Opaque trust-root policy reference"),
      {
        target: { value: "kubernetes://wolf-system/aws-kms-roots" },
      },
    );
    const submit = within(dialog).getByRole("button", {
      name: "Create profile",
    });
    expect(submit).toBeEnabled();
    fireEvent.click(submit);

    await waitFor(() => expect(create).toHaveBeenCalledOnce());
    expect(create).toHaveBeenCalledWith(
      {
        name: "Release signer east",
        provider: "aws_kms",
        algorithm: "ed25519",
        key_reference:
          "aws-kms://us-east-1/123456789012/alias/wolf-release-east",
        workload_identity: true,
        identity: awsSigner.identity,
        issuer: awsSigner.issuer,
        subject: awsSigner.subject,
        trust_root_reference: "kubernetes://wolf-system/aws-kms-roots",
      },
      expect.stringMatching(/^wolf-ui-/),
    );
    expect(onSelect).toHaveBeenCalledWith("signer-aws-created");
  });

  it("rotates and revokes with exact ETag, typed confirmation, and stable operation keys", async () => {
    vi.spyOn(scannerSupplyChainApi, "signer").mockResolvedValue({
      signer: awsSigner,
      etag: '"1"',
    });
    vi.spyOn(scannerSupplyChainApi, "signers").mockResolvedValue({
      items: [awsSigner],
    });
    const replacement: SignerProfile = {
      ...awsSigner,
      id: "signer-aws-2",
      revision: 2,
      rotated_from_id: awsSigner.id,
    };
    const rotate = vi
      .spyOn(scannerSupplyChainApi, "rotateSigner")
      .mockResolvedValue(replacement);
    const revoke = vi
      .spyOn(scannerSupplyChainApi, "revokeSigner")
      .mockResolvedValue({
        ...awsSigner,
        state: "revoked",
        revision: 2,
        revocation_reason: "key version retired",
      });
    const onSelect = vi.fn();

    render(
      <SignersPanel signerId={awsSigner.id} onSelectSigner={onSelect} />,
      { wrapper: wrapper() },
    );
    const rotateButton = await screen.findByRole("button", { name: "Rotate" });
    expect(rotateButton).toBeEnabled();
    fireEvent.click(rotateButton);
    const rotateDialog = screen.getByRole("dialog", {
      name: "Rotate signing profile",
    });
    expect(
      within(rotateDialog).getByText(/All write-only references must be re-entered/i),
    ).toBeVisible();
    fireEvent.change(
      within(rotateDialog).getByLabelText("Opaque key reference"),
      {
        target: {
          value:
            "aws-kms://us-east-1/123456789012/alias/wolf-release-v2",
        },
      },
    );
    fireEvent.change(
      within(rotateDialog).getByLabelText(
        "Opaque trust-root policy reference",
      ),
      {
        target: { value: "kubernetes://wolf-system/aws-kms-roots-v2" },
      },
    );
    fireEvent.change(
      within(rotateDialog).getByLabelText(
        `Type ${awsSigner.id} to confirm rotation`,
      ),
      { target: { value: awsSigner.id } },
    );
    fireEvent.click(
      within(rotateDialog).getByRole("button", { name: "Rotate profile" }),
    );
    await waitFor(() => expect(rotate).toHaveBeenCalledOnce());
    expect(rotate.mock.calls[0][0]).toBe(awsSigner.id);
    expect(rotate.mock.calls[0][2]).toBe('"1"');
    expect(rotate.mock.calls[0][3]).toMatch(/^wolf-ui-/);
    expect(rotate.mock.calls[0][1].key_reference).toContain(
      "wolf-release-v2",
    );
    expect(onSelect).toHaveBeenCalledWith("signer-aws-2");

    fireEvent.click(screen.getByRole("button", { name: "Revoke" }));
    const revokeDialog = screen.getByRole("dialog", {
      name: "Revoke signing profile?",
    });
    const confirmRevoke = within(revokeDialog).getByRole("button", {
      name: "Revoke profile",
    });
    expect(confirmRevoke).toBeDisabled();
    fireEvent.change(within(revokeDialog).getByLabelText("Revocation reason"), {
      target: { value: "key version retired" },
    });
    fireEvent.change(
      within(revokeDialog).getByLabelText(
        `Type ${awsSigner.id} to confirm revocation`,
      ),
      { target: { value: awsSigner.id } },
    );
    fireEvent.click(confirmRevoke);
    await waitFor(() => expect(revoke).toHaveBeenCalledOnce());
    expect(revoke).toHaveBeenCalledWith(
      awsSigner.id,
      "key version retired",
      '"1"',
      expect.stringMatching(/^wolf-ui-/),
    );
  });

  it("keeps managed keyless read-only and masks unexpected server references", async () => {
    vi.spyOn(scannerSupplyChainApi, "signer").mockResolvedValue({
      signer: {
        ...managedSigner,
        key_reference:
          "managed-keyless://deployment/should-never-be-rendered",
        trust_root_reference:
          "kubernetes://wolf-system/should-never-be-rendered",
      },
      etag: '"8"',
    });
    vi.spyOn(scannerSupplyChainApi, "signers").mockResolvedValue({
      items: [managedSigner],
    });

    render(
      <SignersPanel
        signerId={managedSigner.id}
        onSelectSigner={vi.fn()}
      />,
      { wrapper: wrapper() },
    );

    expect(
      await screen.findByText(/Deployment-owned managed keyless profile/i),
    ).toBeVisible();
    expect(screen.getByRole("button", { name: "Rotate" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Revoke" })).toBeDisabled();
    expect(screen.getAllByText("Opaque reference configured")).toHaveLength(2);
    expect(screen.queryByText(/should-never-be-rendered/)).not.toBeInTheDocument();
  });

  it("keeps signer mutations disabled in observe-only mode", async () => {
    vi.spyOn(scannerSupplyChainApi, "signers").mockResolvedValue({
      items: [awsSigner],
    });
    render(<SignersPanel onSelectSigner={vi.fn()} />, {
      wrapper: wrapper(readOnlyCapabilities),
    });

    expect(
      await screen.findByRole("button", { name: "Create signing profile" }),
    ).toBeDisabled();
  });

  it("does not load signer references without administrator access", async () => {
    const signers = vi.spyOn(scannerSupplyChainApi, "signers");
    render(<SignersPanel onSelectSigner={vi.fn()} />, {
      wrapper: wrapper(stableCapabilities, "user"),
    });

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Signer profiles require administrator access",
    );
    expect(signers).not.toHaveBeenCalled();
  });
});
