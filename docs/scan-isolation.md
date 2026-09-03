# Scanner isolation

Default is **deny-by-default network** (`--network none`) plus `--cap-drop ALL`
and `no-new-privileges`. Tools that set `network_required: true` in
`scanners/tools.yaml` run with `--network bridge` (override with
`WOLF_SCANNERS_ALLOW_NETWORK`).

| Knob | Default | Meaning |
|---|---|---|
| `WOLF_SCANNERS_NETWORK` | `none` | Network for offline tools |
| `WOLF_SCANNERS_ALLOW_NETWORK` | `bridge` | Network for `network_required` tools |
| `WOLF_SCAN_ISOLATION` | `standard` | `standard`/`strict` = deny-by-default; `relaxed` = legacy all-bridge |

Kubernetes scan Jobs already run without docker.sock on the API pod
(`WOLF_SCAN_RUNTIME=kubernetes` on workers). Helm NetworkPolicy
`*-scanner-network-required` is egress-empty unless you set
`networkPolicy.scannerEgressCIDRs`.

Community Compose still uses Docker-out-of-Docker for laptops. On shared hosts
use `docker-compose.hardened.yml` (socket proxy). Enterprise must not mount
docker.sock on the API Deployment.
