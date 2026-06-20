# Self-hosting octo-doc

From nothing to a live, TLS-secured doc server in ~15 minutes on a $5 VPS.

## TL;DR

```bash
git clone https://github.com/lml2468/octo-doc && cd octo-doc
DOMAIN=docs.example.com docker compose -f deploy/docker-compose.yml up -d --wait
TOKEN=$(curl -s http://localhost:8080/api/admin/bootstrap | jq -r .token)
echo "Publish with:  export TDOC_BASE_URL=https://docs.example.com TDOC_TOKEN=$TOKEN"
```

---

## Option A — Docker Compose (recommended)

### 1. Get a server and a domain

Any cheap VPS works (Hetzner CX22, DigitalOcean, Vultr, Lightsail — ~$5/mo).
Point an A record for `docs.example.com` at its IP. Open ports **80** and
**443** in the firewall (Caddy needs both for ACME + serving).

### 2. Install Docker

```bash
curl -fsSL https://get.docker.com | sh
```

### 3. Clone and launch

```bash
git clone https://github.com/lml2468/octo-doc
cd octo-doc
# DOMAIN drives Caddy's automatic Let's Encrypt cert.
DOMAIN=docs.example.com docker compose -f deploy/docker-compose.yml up -d --wait
```

That's it — the app + Caddy (auto-TLS) are up. `--wait` blocks until the app
healthcheck passes (typically a few seconds; the whole `up` is well under 2
minutes on a clean Ubuntu 24.04 box).

### 4. Mint a write token

```bash
TOKEN=$(curl -s http://localhost:8080/api/admin/bootstrap | jq -r .token)
echo "$TOKEN"          # save this — bootstrap only works once
```

Prefer a fixed token you control? Set `WRITE_TOKEN=...` in a `.env` file next to
the compose file before `up` (bootstrap is then disabled).

### 5. Publish from your machine

```bash
export TDOC_BASE_URL="https://docs.example.com"
export TDOC_TOKEN="<the token>"
/tdoc publish my-doc          # or: bin/tdoc-publish my-doc
# → Published: https://docs.example.com/d/my-doc/v/1
```

### Verify

```bash
curl -sf https://docs.example.com/api/ping        # {"ok":true,"service":"tdoc"}
curl -sf https://docs.example.com/d/my-doc/v/1 | grep -q '<h1' && echo OK
```

---

## Option B — No Docker (`npx` / bare Node)

For a quick local instance or a tiny box without Docker. Needs **Node 22+**
(for the built-in SQLite). No build step, no native modules.

```bash
npx octo-doc                  # SQLite + ./data, listens on :8080
# in another shell:
curl -s localhost:8080/api/admin/bootstrap | jq -r .token
```

Put it behind your own nginx/Caddy/Traefik for TLS — reference configs are in
[`deploy/`](../deploy/) (`nginx.conf.example`, `Caddyfile`,
`traefik.labels.example.yml`).

Run as a systemd service:

```ini
# /etc/systemd/system/octo-doc.service
[Service]
ExecStart=/usr/bin/npx octo-doc
Environment=PORT=8080 DATA_DIR=/var/lib/octo-doc WRITE_TOKEN=...  COOKIE_SECURE=true
Restart=always
User=octo
[Install]
WantedBy=multi-user.target
```

---

## Going bigger: Postgres + S3/MinIO

The default SQLite+FS stack handles a lot. To scale storage independently, flip
two env vars and bring up the optional services — **no app code changes**:

```bash
STORAGE=postgres+s3 \
DATABASE_URL=postgres://octo:octo@postgres:5432/octodoc \
S3_ENDPOINT=http://minio:9000 S3_BUCKET=octo-doc S3_FORCE_PATH_STYLE=1 \
S3_ACCESS_KEY_ID=minioadmin S3_SECRET_ACCESS_KEY=minioadmin \
docker compose -f deploy/docker-compose.yml --profile postgres --profile minio up -d --wait
docker compose -f deploy/docker-compose.yml exec app node dist/cli.js migrate
```

---

## Production hardening checklist

- [ ] **Separate doc origin.** Serve docs from `d.example.com`, distinct from any
      trusted panel/app origin, so untrusted inline JS can't read app cookies.
      See the two-site block in [`deploy/Caddyfile`](../deploy/Caddyfile).
- [ ] **`COOKIE_SECURE=true`** (default) — cookies only over HTTPS.
- [ ] **`FRAME_ANCESTORS`** — keep `'none'` unless you intentionally embed docs.
- [ ] **Set `WRITE_TOKEN`** explicitly and `ALLOW_BOOTSTRAP=false` once set up.
- [ ] **`PRIVATE=1`** if reads should require the token too.
- [ ] **Backups** — `sqlite3 .backup` + `tar` the blobs, or `pg_dump` + S3
      lifecycle. See [DESIGN.md](./DESIGN.md#backup--restore).
- [ ] **Rate limits** — tune `RATE_LIMIT_MAX` / `RATE_LIMIT_WINDOW_MS` for your
      audience.

All knobs are documented in [`.env.example`](../.env.example).

---

## Troubleshooting

- **`docker compose up` hangs on `--wait`** → check `docker compose logs app`.
  Usually a bad `DATABASE_URL` (postgres profile) or a taken port 8080.
- **Caddy can't get a cert** → DNS A record not pointing at the box yet, or
  ports 80/443 blocked. `docker compose logs caddy` shows the ACME error.
- **`bootstrap` returns 409** → a token already exists (or `WRITE_TOKEN` is
  set). That's expected — bootstrap is one-shot. Use the token you saved, or
  set `WRITE_TOKEN`.
- **Publish 413** → doc exceeds `MAX_HTML_BYTES` (5 MiB default).
