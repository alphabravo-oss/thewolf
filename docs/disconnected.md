# Disconnected Community operation

Wolf Community can run without outbound network for scanning when scanner
images and the control plane are already present.

1. Install from Compose or an air-gap image set produced by the Community
   scanner factory (`wolf scanner-release` offline bundles).
2. `wolf init` then `wolf backup` / `wolf restore` for the control-plane
   database and settings. Do not invent a second backup format.
3. Set `WOLF_SCANNERS_NETWORK=none` (default). Tools that declare
   `network_required` stay skipped unless you opt in.
4. Open `/api/v1/docs` on the host — OpenAPI is bundled, no internet.
5. Configure AI providers only if you have a reachable internal endpoint.
   AI stays off until enabled.
6. Enterprise certified air-gap (signed overlay image, Helm overlay, customer
   license) is a private-channel artifact. Community must not serve it
   (`GET /packaging/bundle` is 404).
