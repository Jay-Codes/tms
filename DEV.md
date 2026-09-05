# DEV.md — Live Dev Preview Setup

Three minimal Next.js CSR "hello" apps + Go reverse proxy + ngrok tunnel for live remote preview. All orchestration via Make (see TOOLING.md).

## Layout

```
apps/enduser   → Next.js dev server :3001, basePath /enduser
apps/tenant    → Next.js dev server :3002, basePath /tenant
apps/admin     → Next.js dev server :3003, basePath /admin
proxy/         → Go reverse proxy :8080 (routes by path prefix)
.dev/          → runtime pids, logs, built proxy binary (gitignored)
```

The proxy maps:

| Public path | Target |
|-------------|--------|
| `/enduser`  | http://localhost:3001 |
| `/tenant`   | http://localhost:3002 |
| `/admin`    | http://localhost:3003 |
| `/api`      | http://localhost:8081 (Go API) |
| `/branding`, `/qrcodes`, `/kyc`, `/signatures` | http://localhost:9000 (MinIO buckets) |
| `/`         | landing page with links |

Each app sets `basePath` in `next.config.js` so assets/HMR resolve behind the proxy; `allowedDevOrigins` allows ngrok hosts.

## Commands

One command runs everything (three dev servers, proxy, ngrok) in the background and prints the public URL:

```bash
make preview
```

Others:

```bash
make stop
```

```bash
make dev
```

```bash
make url
```

```bash
make help
```

- `make dev` — local only (no tunnel), http://localhost:8080
- `make url` — reprint the current ngrok public URL
- `make stop` — kill dev servers, proxy, ngrok (also runs automatically at the start of `dev`/`preview`, so re-running `make preview` is a clean restart)
- Logs: `.dev/*.log`; pids: `.dev/*.pid`

## Backend (Go API, :8081)

The API runs as a host process next to the apps; the proxy forwards `/api` to it.

| Target | Does |
|--------|------|
| `make api` | Build and (re)start the API on :8081 in the background |
| `make api-stop` | Stop only the API — apps, proxy and ngrok keep running |
| `make api-restart` | Rebuild + restart the API |
| `make api-log` | Tail the last 100 lines of `.dev/api.log` |
| `make proxy-restart` | Rebuild + restart only the proxy (:8080) |

Health: `curl localhost:8081/api/v1/healthz` → `{"status":"ok","db":"ok","redis":"ok","minio":"ok"}`. Only a down database yields 503; Redis and MinIO down is degraded-but-serving.

## Database

| Target | Does |
|--------|------|
| `make migrate` | Apply pending migrations (`api migrate up`) |
| `make migrate-down` | Roll back exactly one migration |
| `make test-db` | Create the `tms_test` database used by handler tests (idempotent) |
| `make sqlc` | Regenerate `backend/internal/db/sqlc` |

Migrations are embedded in the binary (`backend/migrations` + `go:embed`), so the API image carries the schema with it. In a compose deploy the API applies them itself at boot (`MIGRATE_ON_START=1`) — there is no separate migrate step to run.

The first platform admin is seeded on startup from `ADMIN_EMAIL` / `ADMIN_PASSWORD` when no `platform_admin` row exists yet; leave either blank to disable seeding.

## Deployed stack (containers)

`make deploy` builds and runs the same three apps + API + proxy as images under the compose `full` profile — see [README.md](README.md#deploy-full-stack-compose). It shares the dev Postgres/Redis/MinIO volumes, so the data is the same data. `make tls-selfsigned` writes a cert/key pair into `.dev/tls` (mounted read-only at `/etc/tms/tls` in the proxy container); set `TLS_CERT_FILE` and `TLS_KEY_FILE` to those paths to make the proxy serve 443 and redirect 80 → 443.

## Notes

- Free ngrok URL changes on every restart — `make url` after each `make preview`. **The new URL has to go into `.env` as both `APP_BASE_URL` and `MINIO_PUBLIC_URL`, then `make api-restart`.** Skipping it leaves QR targets and presigned links pointing at the dead tunnel: the QR PNG 403s and `/enduser/u/{code}` opens the wrong origin.
- ngrok free shows a browser interstitial on first visit — click through, or send header `ngrok-skip-browser-warning: 1`.
- Presigned MinIO URLs are signed against `MINIO_PUBLIC_URL` (defaults to `APP_BASE_URL`), so QR PNGs open from a phone through the tunnel. Set it to the current ngrok URL after `make preview`; leave it at `http://localhost:8080` when there is no tunnel. The bucket paths above are forwarded to MinIO with Host and path unchanged so the V4 signature validates. Presigned GETs are method-bound: `curl -I` (HEAD) returns 403, a GET returns 200.
- Hot reload works through the tunnel; edit `apps/*/app/page.tsx` and the browser updates.
- First install: `make install` (npm workspaces).

## Seed, UAT and hardening

```bash
make seed
```

```bash
make seed-demo
```

```bash
make loadtest
```

```bash
make test-isolation
```

- `make seed` — load-test org `Load Test Estates` (`load@tms.local` / `password123`); `make seed -- -reset` style flags via `go run ./backend/cmd/seed -reset`.
- `make seed-demo` — JJnE Rentals demo data (adds owner `demo@jjne.test` / `password123`); prints unit codes for QR testing.
- `make test-race` — race detector on the concurrency-sensitive packages.
- UAT script: `docs/UAT.md`. OTPs in dev: `make api-log` (look for `sms_body`).
