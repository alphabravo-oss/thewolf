# Customer-managed scanner release signing

Wolf supports AWS KMS, Google Cloud KMS, Azure Key Vault/Managed HSM,
PKCS#11 HSMs, workload-identity keyless signing, and disconnected offline
signers through one provider-neutral adapter contract. Managed keyless signing
uses the same verification and evidence path but remains deployment-owned;
the signer administration API cannot overwrite it.

## Security model

- The API and database store opaque key, secret, and trust-root references.
  They never accept or read private-key bytes.
- `GET` and list responses mask references (`aws-kms://***`,
  `kubernetes://***`) and expose only whether a secret reference is configured.
- The adapter is a deployment-owned executable invoked directly without a
  shell. Wolf sends bounded JSON on stdin, accepts strict bounded JSON on
  stdout, and redacts bounded stderr.
- The deterministic request digest binds operation ID, profile revision and
  digest, artifact digest/media type/URI, definition commit, lock digest, and
  policy ID/revision.
- The adapter must use the request operation ID as its provider-side
  idempotency token or durable operation record. Wolf writes a durable
  `started` marker first and will not retry an ambiguous external result.
- Wolf cryptographically verifies the returned signature and requires exact
  algorithm, identity, issuer, subject, trust-root reference, and external
  operation-ID matches before recording evidence.
- Offline import verifies against a policy-pinned public key. It never trusts
  an embedded bundle key.

## Profile document

Deployments mount a strict profile at
`WOLF_SCANNER_SIGNER_PROFILE_FILE`. This is configuration, not a secret:

```json
{
  "schema_version": "wolf.scanner-signer-profile/v1",
  "id": "release-prod",
  "name": "Production release signer",
  "provider": "aws_kms",
  "algorithm": "ecdsa-p256-sha256",
  "key_reference": "aws-kms://us-east-1/123456789012/alias/wolf-release",
  "workload_identity": true,
  "identity": "arn:aws:iam::123456789012:role/wolf-release",
  "issuer": "https://sts.amazonaws.com",
  "subject": "arn:aws:iam::123456789012:role/wolf-release",
  "trust_root_reference": "kubernetes://wolf-system/aws-kms-roots",
  "revision": 1
}
```

Allowed key-reference prefixes are `aws-kms://`, `gcp-kms://`,
`azure-keyvault://`, `pkcs11:`, `workload://`, `offline://`, and
`managed-keyless://`. Authentication must use `workload_identity: true` or an
opaque `secret_reference` using `secret://`, `kubernetes://`, `vault://`, or
`file-ref://`. Put mounted credential files under
`/run/wolf/signing/credentials`; never put credential contents in the profile.

## Provider examples

| Provider | Key reference example | Preferred authentication | Notes |
|---|---|---|---|
| AWS KMS | `aws-kms://us-east-1/123/alias/wolf-release` | IRSA/workload identity | Pin the key version returned by KMS in evidence. |
| GCP KMS | `gcp-kms://projects/p/locations/global/keyRings/r/cryptoKeys/k` | GKE Workload Identity | Adapter resolves and returns the CryptoKeyVersion. |
| Azure Key Vault/HSM | `azure-keyvault://vault-name/keys/wolf-release` | Azure federated workload identity | Use Managed HSM URI semantics inside the adapter. |
| PKCS#11 | `pkcs11:token=wolf;object=release;type=private` | mounted module/socket plus `file-ref://` PIN reference | Do not place the PIN in the URI or environment. |
| Keyless | `workload://cluster/ns/service-account` | projected OIDC token | Pin Fulcio issuer/subject and Rekor/trust root. |
| Offline | `offline://ceremony/release-2026-q3` | one-way request/result exchange | Keep the started journal with the request until an authorized result is imported. |

The adapter request contains schema
`wolf.scanner-signing-request/v1`, the opaque references, expected identity
policy, artifact and immutable binding, base64 canonical signing payload, and
request/operation/profile digests. It must return
`wolf.scanner-signing-result/v1` with the exact digests, signature and
signature URI, public key or certificate, key version, exact identity mapping,
trust-root reference plus `trust_verified: true`, transparency entry when
applicable, and an exact `external_operation_id`.

## Administration

```bash
wolf scanner signer create signer.yaml
wolf scanner signer list
wolf scanner signer show PROFILE_ID
wolf scanner signer rotate PROFILE_ID replacement.yaml --if-match 1
wolf scanner signer revoke PROFILE_ID --if-match 2 \
  --reason "key version disabled after scheduled rotation"
```

Equivalent API routes are under
`/api/v1/scanner-supply-chain/signers`. Create and rotation are strict,
write-only-reference requests. Rotation creates revision N+1 and disables the
prior revision atomically. Revocation immediately prevents new requests;
already published signatures remain evidence but policy must reject the
revoked profile for new promotion/import decisions.

Administrators can perform the same workflow in **Scanners → Signing**. The
web console:

- lists active, rotated/disabled, revoked, and deployment-owned managed
  keyless profiles without exposing full key, secret, or trust-root references;
- provides provider-specific opaque-reference forms and workload-identity
  guidance for AWS KMS, Google Cloud KMS, Azure Key Vault/Managed HSM,
  PKCS#11, keyless, and offline signers;
- requires every write-only reference to be entered again during rotation
  instead of replaying a masked read value;
- uses the exact profile ETag for atomic rotation and revocation, disables
  duplicate submission while a command is pending, and requires typed profile
  confirmation for both operations;
- treats managed keyless profiles as inspectable deployment configuration;
  customer create, rotate, and revoke controls cannot replace them; and
- retains the rotation/revocation chain and impact warnings needed to verify
  historical signatures after a signer stops accepting new work.

Signer mutations are available only to administrators when scanner release
management is in `candidate` mode or higher. Observe-only deployments retain
masked list and detail access with all mutation controls disabled.

Recommended rotation order:

1. Add the replacement public key/profile digest to every offline trust policy.
2. Create and validate the replacement profile in staging.
3. Rotate with the active revision in `If-Match`.
4. Verify a release and bundle through the new profile.
5. Retain the disabled profile for the evidence retention window.
6. Revoke immediately on compromise; record reason, actor, and timestamp.

## Release-step contract

For `signature/<image>` and `release-manifest-signature`, the prior release
step writes:

```json
{
  "schema_version": "wolf.scanner-signing-artifact/v1",
  "artifact": {
    "uri": "oci://registry.example/wolf/scanners@sha256:...",
    "digest": "sha256:...",
    "media_type": "application/vnd.oci.image.manifest.v1+json"
  }
}
```

The path is
`.wolf-signing/requests/signature--<image>.json` or
`.wolf-signing/requests/release-manifest-signature.json` in the operation
workspace. The result contains the signature URI/digest and complete verified
identity evidence. Missing descriptors or unconfigured signing capabilities
fail closed.

## Deployment

For Kubernetes, set `scannerRelease.signing.enabled`, `profileSecret`,
`adapterPath`, optional `credentialSecret`, `workloadIdentity`, and a dedicated
`serviceAccountName`. The managed coordinator mounts the signer profile and,
when configured, credential Secret read-only so it can construct the bounded
signing Job. Signing runs under the dedicated signer service account. Put IRSA,
GCP, or Azure workload-identity annotations on that service account only; only
a workload-identity signing Job gets its service-account token.

Private signer configuration is never mounted into the API, ordinary scanner,
BuildKit, or non-signing adapter pods. API bundle export operates on previously
persisted, verified signature artifacts and public evidence; it does not need
signing credentials. The signer profile Secret, signer credential Secret, and
every other managed adapter credential Secret must be distinct.

For Compose/local signing, set absolute
`WOLF_SCANNER_SIGNER_PROFILE_HOST_FILE`, optional
`WOLF_SCANNER_SIGNER_CREDENTIAL_HOST_DIR`, adapter path, and the explicit
signer network. Keep `WOLF_SCANNER_SIGNER_NETWORK=none` for PKCS#11/offline
signing; select a restricted operator-created network for cloud KMS. Portable
bundle export uses persisted verified evidence and does not mount the private
signer profile into the API container.

## Validation checklist

- Create/list/show confirms all references are masked.
- Submit `private_key` or PEM content and confirm rejection.
- Run the fake-adapter contract tests and provider sandbox tests.
- Sign the same operation twice and confirm one provider call and identical
  evidence.
- Interrupt after provider submission and confirm retry fails ambiguous rather
  than creating another signature.
- Alter issuer, subject, trust root, request digest, public key, signature, or
  operation ID and confirm failure.
- Rotate and confirm the old revision is disabled; revoke and confirm new
  requests fail.
- Export/import a signed bundle using a policy-pinned public key, then confirm
  unknown and revoked profiles fail.
- Inspect logs, API responses, evidence, and bundle metadata for credential or
  private-key material.
