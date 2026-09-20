# DEPLOY.md — Server deploy (API + edge; frontends on Vercel)

What runs on the server: the Go API and the edge proxy, from
`docker-compose.deploy.yml`. The three Next.js apps deploy to Vercel and are
not part of this. Postgres, Redis and MinIO are either **reused** from a
compose project already on the box (default) or **run by this stack**
(`INFRA=own`).

Everything goes through Make (TOOLING.md):

| Target | Does |
|---|---|
| `make docker-deploy` | Pull api + edge from Docker Hub, create buckets, bring them up (reuse infra) |
| `make docker-deploy INFRA=own` | Same, plus own postgres/redis/minio under the `infra` profile |
| `make docker-deploy TAG=…` | Same, both images pinned to that tag (rollback = older tag) |
| `make docker-pull` | Just pull the images |
| `make docker-builder` | Create the x86_64 buildx builder (once per laptop) |
| `make docker-publish [TAG=…] [PUSH=0]` | Build api + edge for `linux/amd64`, push to Docker Hub |
| `make docker-logs [SERVICE=api]` | Tail logs |
| `make docker-ps` | Containers + health |
| `make docker-restart` | Restart api + edge |
| `make docker-down` | Remove api + edge (add `INFRA=own` to stop infra too; volumes stay) |
| `make docker-buckets` | (Re)create the MinIO buckets |
| `make docker-migrate` | One-off `api migrate up` (boot does this already) |
| `make docker-psql` | psql into `DATABASE_URL` |
| `make docker-network` | Create `INFRA_NETWORK` if missing (run by deploy) |
| `make server-setup [DOMAIN=…] [EMAIL=…]` | nginx site + Let's Encrypt cert in front of the edge (default `api.tms.kuzo.co.tz`) |
| `make server-check` | `curl https://DOMAIN/api/v1/healthz` |

All targets take `ENV_FILE=…` (default `.env.deploy`).

## 1. One-time setup on the server

```bash
git clone <repo> /opt/tms && cd /opt/tms
cp .env.deploy.example .env.deploy
```

Edit `.env.deploy`. The keys that matter:

| Key | Set to |
|---|---|
| `ENDUSER_BASE_URL`, `TENANT_BASE_URL` | `https://tms.kuzo.co.tz`, `https://lms.kuzo.co.tz` — renter and landlord app origins; SMS/QR/invite links are built on them |
| `CORS_ALLOWED_ORIGINS` | The three app origins, comma-separated |
| `COOKIE_SAME_SITE=none`, `COOKIE_SECURE=1` | Session cookies must cross sites |
| `MINIO_PUBLIC_URL` | `https://api.tms.kuzo.co.tz` — the API host (presigned URLs are signed against it) |
| `TMS_EDGE_PORT` | Loopback port for the host reverse proxy. Free on this box: 3300 |
| `INFRA_NETWORK` | Network the API joins (see reuse vs own below) |
| `DATABASE_URL`, `REDIS_URL`, `MINIO_ENDPOINT`, `MINIO_ROOT_USER/PASSWORD` | Where the infra is |
| `NIDA_ENC_KEY`, `ADMIN_PASSWORD`, `POSTGRES_PASSWORD`, `MINIO_ROOT_PASSWORD` | Real secrets (`openssl rand -hex 32`) |
| `BEEM_*` | Real SMS. Blank = OTP codes land in `make docker-logs` |

### Reuse mode (default): existing postgres / redis / minio

The `kyohuo` project on the box already runs `postgres:17.4-alpine`,
`redis:7.4.2-alpine` and `minio` with no published ports. The API reaches them
by joining that project's network and using the compose service names.

1. Find the network: `docker network ls` → e.g. `kyohuo_default`. Put it in
   `INFRA_NETWORK`.
2. Postgres: dedicated role + database (never share the other app's DB):
   ```bash
   docker exec -it kyohuo-postgres-1 psql -U <superuser> -c "CREATE USER tms WITH PASSWORD '<pw>';"
   docker exec -it kyohuo-postgres-1 psql -U <superuser> -c "CREATE DATABASE tms OWNER tms;"
   ```
   Then `DATABASE_URL=postgres://tms:<pw>@postgres:5432/tms?sslmode=disable`.
   Migrations use `pgcrypto`; if `CREATE EXTENSION` fails for a non-superuser,
   run `CREATE EXTENSION IF NOT EXISTS pgcrypto;` in the `tms` database as the
   superuser once.
3. Redis: pick an unused DB index, `REDIS_URL=redis://redis:6379/1`. Sessions,
   rate limits, notification queue live here; only losing them is degraded,
   not fatal.
4. MinIO: a service account for the API (not the shared root):
   ```bash
   docker exec -it kyohuo-minio-1 sh -c 'mc alias set local http://127.0.0.1:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" && mc admin user add local tms <secret> && mc admin policy attach local readwrite --user tms'
   ```
   `MINIO_ENDPOINT=minio:9000`, `MINIO_ROOT_USER=tms`, `MINIO_ROOT_PASSWORD=<secret>`.
   Bucket names are fixed (`branding qrcodes kyc signatures receipts proofs`);
   they must not already exist for another app on that server.
   `make docker-deploy` creates them.

Service names (`postgres`, `redis`, `minio`) are the other project's compose
service names, resolvable only from inside its network — check with
`docker compose -p kyohuo ps`. If they differ, use the container names
(`kyohuo-postgres-1`) as hosts instead.

Caveat: the reused containers are owned by the other project. Their
`docker compose down` takes the TMS database with it, and their upgrades are
not coordinated with ours. Own mode avoids that for the price of three more
containers.

### Own mode: `INFRA=own`

Leave `INFRA_NETWORK=tms-infra`, `DATABASE_URL`/`REDIS_URL`/`MINIO_ENDPOINT`
pointing at `postgres`/`redis`/`minio`, and make `POSTGRES_*` /
`MINIO_ROOT_*` match. Nothing is published to the host; data lives in named
volumes `tms_pgdata`, `tms_redisdata`, `tms_miniodata`.

## 2. Images: built and published from a laptop only

The server never compiles. It pulls `josephchuchu/tms-api` and
`josephchuchu/tms-edge` from Docker Hub (`API_IMAGE` / `EDGE_IMAGE` in
`.env.deploy`, `pull_policy: always`). Images are `linux/amd64`, tagged with
the short git sha (or `TAG=`) plus `latest`.

Once per laptop:

```bash
make docker-builder     # x86_64 buildx builder "tms-x86" (docker-container driver)
make docker-login
```

Then, from the commit you want to ship:

```bash
make docker-publish                 # api + edge -> :<sha> and :latest
make docker-publish TAG=v1.2.0      # explicit tag
make docker-publish PUSH=0          # build only, loaded into local docker
```

`IMAGE_REPO=` changes the namespace/registry prefix, `PLATFORM=` the target.
Both Dockerfiles cross-compile from the builder's native platform
(`--platform=$BUILDPLATFORM`, `GOARCH=$TARGETARCH`), so an amd64 build on
Apple Silicon does not run under emulation (~4 min cold, seconds warm). Layer
cache lives in the registry as `…:buildcache`.

There is no build in CI. `.github/workflows/deploy.yml` is a manual "Run
workflow" that SSHes to the server and runs `make docker-deploy TAG=<tag>`
for a tag you already pushed; it needs vars `DEPLOY_HOST` (`DEPLOY_USER`,
`DEPLOY_PATH`) and secret `DEPLOY_SSH_KEY`. Running the same command over
SSH by hand is equivalent.

## 3. Deploy / update

```bash
make docker-deploy            # pull API_IMAGE/EDGE_IMAGE (.env.deploy, default :latest)
make docker-deploy TAG=a1b2c3d   # pin both images to one tag for this rollout
make docker-deploy INFRA=own  # either of the above + own postgres/redis/minio
```

Rollback is the same command with an older tag. Order: network → (own infra
up, healthy) → pull images → buckets → api up
(migrates in-process, seeds first admin) → edge up. Re-run the same command
after `git pull` to roll a new version; `--wait` fails the target if the API
healthcheck never passes, in which case `make docker-logs SERVICE=api`.

Check:

```bash
curl -s http://127.0.0.1:3300/api/v1/healthz
```

Expect `{"status":"ok","db":"ok","redis":"ok","minio":"ok"}`.

## 4. Ingress: nginx + Let's Encrypt (`make server-setup`)

The edge listens on `127.0.0.1:TMS_EDGE_PORT` only. On the server, once
`DOMAIN` resolves to it:

```bash
make server-setup                                  # api.tms.kuzo.co.tz, EMAIL from ADMIN_EMAIL
make server-setup DOMAIN=api.example.com EMAIL=ops@example.com
make server-check                                  # curl https://DOMAIN/api/v1/healthz
```

What it does (Debian/Ubuntu, `sudo` when not root):

1. `server-deps` — `apt install nginx certbot python3-certbot-nginx`, enable nginx.
2. `server-nginx` — renders `deploy/nginx/site.conf.template` with `DOMAIN`
   and `TMS_EDGE_PORT` (from `.env.deploy`) into
   `/etc/nginx/sites-available/<domain>.conf`, links it into
   `sites-enabled`, `nginx -t`, reload. Plain HTTP, proxying everything to the
   edge with `Host` and `X-Forwarded-*` set, 25 MB uploads, 120 s timeouts.
3. `server-cert` — `certbot --nginx -d DOMAIN --redirect`: adds the 443 block
   and 80 → 443 redirect to that same file, then a `renew --dry-run`. Renewal
   is `certbot.timer`, installed by the package.

Re-running is safe: the site file is rewritten and certbot keeps a valid cert
(`--keep-until-expiring`). If the box already runs Caddy or another nginx on
80/443, skip this and add the equivalent of the template to it:
`reverse_proxy 127.0.0.1:3300` with Host unchanged.

Then in `.env.deploy`: `MINIO_PUBLIC_URL=https://<domain>` (presigned URLs
are signed against it and validate on the Host header). `make docker-restart`
if the API was already up.

## 5. Vercel side: three apps on their own domains

| App | Domain | Vercel root dir | Env |
|---|---|---|---|
| renters (`apps/enduser`) | `tms.kuzo.co.tz` | `apps/enduser` | `NEXT_PUBLIC_API_URL=https://api.tms.kuzo.co.tz`, `NEXT_PUBLIC_BASE_PATH=` (empty) |
| landlords (`apps/tenant`) | `lms.kuzo.co.tz` | `apps/tenant` | same |
| admins (`apps/admin`) | `tms-admin.kuzo.co.tz` | `apps/admin` | same |

Three Vercel projects from one repo (framework Next.js, install from the
monorepo root so `@tms/ui` resolves). Each app's `.env.example` lists its
variables. `NEXT_PUBLIC_BASE_PATH` must be set **empty** on Vercel: unset
means the dev default (`/enduser` etc.), and the app would serve from
`tms.kuzo.co.tz/enduser/…`.

How cross-origin works:

- **API calls**: `lib/api.ts` prefixes `NEXT_PUBLIC_API_URL`, every request
  is `credentials: 'include'`. The API answers CORS for the origins in
  `CORS_ALLOWED_ORIGINS` (exact match, credentialed, preflight handled before
  routing) and sets its `tms_r` / `tms_o` / `tms_a` cookies with
  `SameSite=None; Secure` (`COOKIE_SAME_SITE=none`, `COOKIE_SECURE=1`). Safari
  ITP and Brave may still block third-party cookies by default: all four
  hosts share the `kuzo.co.tz` site, so they count as same-site and are not
  affected, which is the reason to keep them under one registrable domain.
- **Uploads / images**: presigned MinIO URLs point at `MINIO_PUBLIC_URL`
  (the API host). `<img>` is fine cross-origin; the presigned PUT from the
  browser is a CORS request answered by MinIO itself, whose default
  `MINIO_API_CORS_ALLOW_ORIGIN=*` allows it.
- **Links in SMS / QR**: built server-side from `ENDUSER_BASE_URL` and
  `TENANT_BASE_URL`, so they land on the right domain with no basePath.
- **PWA**: manifest, icons and the service worker scope all derive from
  `NEXT_PUBLIC_BASE_PATH` (`app/manifest.ts`, `lib/basePath.ts`,
  `public/sw.js` reads its own URL), so each app installs from its domain root.

## 6. Known limits

- **`ENV=prod` does not boot.** `notify.EmailProviderFor` has no production
  provider and refuses `prod`. Run `ENV=dev` until one exists. Consequences:
  session cookies are not `Secure`, and with `BEEM_API_KEY` blank OTPs go to
  the container log.
- Backups are not automated. Own mode: `docker run --rm --network tms-infra
  postgres:17-alpine pg_dump "$DATABASE_URL" > tms.sql` plus a copy of the
  `tms_miniodata` volume.
