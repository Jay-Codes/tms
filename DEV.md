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

## Notes

- Free ngrok URL changes on every restart — `make url` after each `make preview`.
- ngrok free shows a browser interstitial on first visit — click through, or send header `ngrok-skip-browser-warning: 1`.
- Presigned MinIO URLs are signed against `MINIO_PUBLIC_URL` (defaults to `APP_BASE_URL`), so QR PNGs open from a phone through the tunnel. Set it to the current ngrok URL after `make preview`; leave it at `http://localhost:8080` when there is no tunnel. The bucket paths above are forwarded to MinIO with Host and path unchanged so the V4 signature validates. Presigned GETs are method-bound: `curl -I` (HEAD) returns 403, a GET returns 200.
- Hot reload works through the tunnel; edit `apps/*/app/page.tsx` and the browser updates.
- First install: `make install` (npm workspaces).
