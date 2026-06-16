# Deploying The Wolf

Wolf ships as a Docker Compose stack. This guide covers running it on a public
server with TLS, and the hardened Docker-access path.

## TL;DR

```bash
cp .env.example .env        # then edit it (admin password, domain, email, secret)

# Local / private (HTTP on localhost):
docker compose up -d --build

# Public server with automatic HTTPS (Let's Encrypt):
docker compose --profile proxy up -d --build

# Public + HTTPS + hardened Docker access (recommended for shared hosts):
docker compose -f docker-compose.yml -f docker-compose.hardened.yml \
  --profile proxy up -d --build
```

Add `--profile postgres` to any of the above to use Postgres instead of SQLite.

---

## TLS

Wolf itself speaks HTTP only — TLS is terminated by the bundled **Caddy** reverse
proxy (the `proxy` profile). Caddy sets `X-Forwarded-Proto`, which Wolf uses to
mark session cookies `Secure`. There are two modes, both driven by `.env`.

### Mode 1 — Automatic (Let's Encrypt)  *(default)*

```ini
WOLF_DOMAIN=wolf.example.com     # must resolve to this host's public IP
ACME_EMAIL=you@example.com       # ACME contact / renewal notices
```

```bash
docker compose --profile proxy up -d
```

Caddy obtains and auto-renews a certificate for `WOLF_DOMAIN`. Ports 80 and 443
must be reachable from the internet (80 is used for the ACME challenge and a
redirect to 443).

### Mode 2 — Bring your own certificate

Use this for an internal CA, a wildcard cert, or a cert you already manage.

```ini
CADDYFILE=./deploy/Caddyfile.byocert
WOLF_DOMAIN=wolf.example.com
WOLF_TLS_DIR=/absolute/path/to/your/certs   # must contain cert.pem + key.pem
```

- `cert.pem` is the full chain (leaf + intermediates), `key.pem` the private key.
- `ACME_EMAIL` is ignored in this mode.

```bash
docker compose --profile proxy up -d
```

The directory is mounted read-only into Caddy at `/certs`. To rotate the cert,
replace the files and `docker compose restart caddy`.

---

## Hardened Docker access (socket-proxy)

Wolf spawns scanner containers, which normally requires the host Docker socket
(`/var/run/docker.sock`) — and that socket is effectively **root on the host**.
The `docker-compose.hardened.yml` override puts a
[docker-socket-proxy](https://github.com/Tecnativa/docker-socket-proxy) in front
of the real socket. Only the proxy mounts the socket; Wolf talks to a filtered
TCP API over the internal network and the direct socket mount is removed.

```bash
docker compose -f docker-compose.yml -f docker-compose.hardened.yml \
  --profile proxy up -d --build
```

The proxy allows only the API groups Wolf needs (containers, images, build,
version) and denies the rest (swarm, secrets, services, volume management,
networks, …). Requires Docker Compose v2.24+ (for the `!override` tag).

> **This reduces, but does not eliminate, the risk.** A compromised Wolf could
> still create a container with host bind-mounts. For full isolation, run Wolf
> on a **dedicated host**, and consider **rootless Docker** or **sysbox**.

If you don't run the in-app scanner-image builder, you can tighten the proxy
further by removing `BUILD`, `SESSION`, and `DISTRIBUTION` from its environment
in `docker-compose.hardened.yml`.

---

## Public-server checklist

1. **Set a strong `WOLF_MASTER_KEY`** (32+ chars). Without it the JWT secret
   regenerates on restart and logs everyone out.
2. **Change the admin password** (`WOLF_ADMIN_PASSWORD`). Self-service
   registration is **off by default** — add users from Settings → Users.
3. **Front with the proxy** and don't expose Wolf's own port publicly. Keep
   `WOLF_BIND=127.0.0.1` (or remove the port mapping); only Caddy faces 80/443.
4. **`WOLF_CORS_ORIGINS=https://your-domain`** so the browser origin matches.
5. **Use the hardened socket-proxy override** on shared/multi-tenant hosts.
6. **Use Postgres** (`--profile postgres`) for durability and concurrency; set a
   real `POSTGRES_PASSWORD` (or a Docker secret).
7. **Firewall:** open only 80 + 443. Postgres binds to localhost by default.
8. **Scanner images:** make sure `WOLF_SCANNERS_TAG` resolves for your host's
   architecture (publish multi-arch images via `make scanners-buildx-all` or
   the `scanners-image` CI workflow).
9. **Back up** the `wolf-data` (and `pg-data`) volumes.
