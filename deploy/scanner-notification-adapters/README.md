# Scanner notification adapters

The opt-in Compose notification worker mounts this directory read-only at
`/opt/wolf-notification-adapters`. Install only the adapters enabled by the
active scanner release policy:

- `webhook`
- `email`
- `siem`

Each file must be an executable, not a shell command string. Wolf starts the
configured executable directly, sends one
`wolf.scanner-notification-delivery/v1` JSON object on standard input, and
expects exactly:

```json
{"status":"delivered","provider_message_id":"optional-provider-id"}
```

The adapter resolves the opaque `destination_ref` to its endpoint and
credential. Keep those mappings and secrets outside the Wolf database. A
non-zero exit is retryable; an invalid or oversized success response is sent
directly to the dead-letter queue. Use the stable `idempotency_key` at the
provider boundary because a worker can be interrupted after the provider
accepts a message but before Wolf commits the delivery result.

Do not place real credentials in this source directory. Mount secret files or
extend the Compose service with deployment-specific secret references.
