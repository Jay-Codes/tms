.DEFAULT_GOAL := help

DEVDIR := .dev
, := ,
NGROK_API := http://127.0.0.1:4040/api/tunnels

.PHONY: help preview dev stop url install up down \
        api api-stop api-restart api-log proxy-restart \
        migrate migrate-down test-db sqlc build test test-isolation test-race lint \
        seed seed-demo loadtest

help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "} {printf "  make %-14s %s\n", $$1, $$2}'

install: ## Install all dependencies (npm workspaces)
	npm install

up: ## Start infra (postgres, redis, minio + buckets) via docker compose
	docker compose up -d --wait postgres redis minio
	docker compose up minio-init

down: ## Stop docker compose infra (data volumes preserved)
	docker compose down

dev: stop ## Start the three Next.js apps + Go proxy (local only, no tunnel)
	@mkdir -p $(DEVDIR)
	@echo "starting enduser (:3001), tenant (:3002), admin (:3003)..."
	@nohup npm run dev --workspace apps/enduser > $(DEVDIR)/enduser.log 2>&1 & echo $$! > $(DEVDIR)/enduser.pid
	@nohup npm run dev --workspace apps/tenant  > $(DEVDIR)/tenant.log  2>&1 & echo $$! > $(DEVDIR)/tenant.pid
	@nohup npm run dev --workspace apps/admin   > $(DEVDIR)/admin.log   2>&1 & echo $$! > $(DEVDIR)/admin.pid
	@echo "building + starting proxy (:8080)..."
	@cd proxy && go build -o ../$(DEVDIR)/proxy-bin .
	@nohup $(DEVDIR)/proxy-bin > $(DEVDIR)/proxy.log 2>&1 & echo $$! > $(DEVDIR)/proxy.pid
	@echo "local preview: http://localhost:8080  (/enduser /tenant /admin)"

preview: dev ## Everything in `dev` + ngrok tunnel; prints public URL
	@nohup ngrok http 8080 --log stdout --log-format json > $(DEVDIR)/ngrok.log 2>&1 & echo $$! > $(DEVDIR)/ngrok.pid
	@$(MAKE) --no-print-directory url

url: ## Print the current ngrok public URL
	@i=0; while :; do \
		u=$$(curl -s $(NGROK_API) 2>/dev/null | python3 -c "import sys,json; ts=json.load(sys.stdin).get('tunnels',[]); print(ts[0]['public_url'] if ts else '')" 2>/dev/null); \
		[ -n "$$u" ] && echo "public URL: $$u" && break; \
		i=$$((i+1)); [ $$i -gt 20 ] && echo "ngrok tunnel not up — check $(DEVDIR)/ngrok.log" && exit 1; \
		sleep 1; \
	done
	@echo "routes: /enduser  /tenant  /admin"

apps-restart: ## Restart only the three Next.js dev servers (keeps proxy, ngrok, api)
	@mkdir -p $(DEVDIR)
	@for app in enduser tenant admin; do \
		f=$(DEVDIR)/$$app.pid; \
		if [ -f "$$f" ]; then pkill -P "$$(cat $$f)" 2>/dev/null; kill "$$(cat $$f)" 2>/dev/null; rm -f "$$f"; fi; \
	done; true
	@for port in 3001 3002 3003; do lsof -ti tcp:$$port | xargs kill 2>/dev/null; true; done; sleep 1
	@rm -rf apps/enduser/.next apps/tenant/.next apps/admin/.next
	@nohup npm run dev --workspace apps/enduser > $(DEVDIR)/enduser.log 2>&1 & echo $$! > $(DEVDIR)/enduser.pid
	@nohup npm run dev --workspace apps/tenant  > $(DEVDIR)/tenant.log  2>&1 & echo $$! > $(DEVDIR)/tenant.pid
	@nohup npm run dev --workspace apps/admin   > $(DEVDIR)/admin.log   2>&1 & echo $$! > $(DEVDIR)/admin.pid
	@echo "restarted enduser (:3001), tenant (:3002), admin (:3003)"

stop: ## Stop dev servers, proxy, and ngrok
	@for f in $(DEVDIR)/*.pid; do \
		[ -f "$$f" ] && kill "$$(cat $$f)" 2>/dev/null; rm -f "$$f"; \
	done; true
	@for port in 3001 3002 3003 8080 4040; do \
		lsof -ti tcp:$$port | xargs kill 2>/dev/null; true; \
	done; true
	@echo "stopped."

# --- backend (Go API, :8081) ---

api: api-stop ## Build and run the Go API (:8081) in the background
	@mkdir -p $(DEVDIR)
	@cd backend && go build -o ../$(DEVDIR)/api-bin ./cmd/api
	@set -a; [ -f .env ] && . ./.env; set +a; \
		nohup $(DEVDIR)/api-bin > $(DEVDIR)/api.log 2>&1 & echo $$! > $(DEVDIR)/api.pid
	@echo "api on :8081 — health: curl localhost:8081/api/v1/healthz — logs: make api-log"

api-stop: ## Stop the Go API only (leaves apps, proxy and ngrok running)
	@[ -f $(DEVDIR)/api.pid ] && kill "$$(cat $(DEVDIR)/api.pid)" 2>/dev/null; rm -f $(DEVDIR)/api.pid; true
	@lsof -ti tcp:8081 | xargs kill 2>/dev/null; true

api-restart: api ## Rebuild and restart the Go API

api-log: ## Tail the last 100 lines of the API log
	@tail -n 100 $(DEVDIR)/api.log

proxy-restart: ## Rebuild and restart only the dev proxy (:8080)
	@mkdir -p $(DEVDIR)
	@[ -f $(DEVDIR)/proxy.pid ] && kill "$$(cat $(DEVDIR)/proxy.pid)" 2>/dev/null; rm -f $(DEVDIR)/proxy.pid; true
	@cd proxy && go build -o ../$(DEVDIR)/proxy-bin .
	@nohup $(DEVDIR)/proxy-bin > $(DEVDIR)/proxy.log 2>&1 & echo $$! > $(DEVDIR)/proxy.pid
	@echo "proxy restarted on :8080"

# --- database ---

migrate: ## Apply pending database migrations
	@set -a; [ -f .env ] && . ./.env; set +a; \
		cd backend && go run ./cmd/api migrate up

migrate-down: ## Roll back exactly one migration
	@set -a; [ -f .env ] && . ./.env; set +a; \
		cd backend && go run ./cmd/api migrate down

test-db: ## Create the tms_test database used by handler tests (idempotent)
	@docker compose exec -T postgres sh -c 'psql -U $${POSTGRES_USER:-tms} -tc "SELECT 1 FROM pg_database WHERE datname = '"'"'tms_test'"'"'" | grep -q 1 || createdb -U $${POSTGRES_USER:-tms} tms_test'
	@echo "test database ready: tms_test (host port 5433)"

sqlc: ## Regenerate sqlc query code into backend/internal/db/sqlc
	@cd backend/internal/db && \
		if command -v sqlc >/dev/null 2>&1; then sqlc generate; \
		else echo "note: sqlc binary not found — running via 'go run' (needs network)"; \
		     go run github.com/sqlc-dev/sqlc/cmd/sqlc@latest generate; fi
	@echo "sqlc: generated."

# --- seeding / load pass (Phase 8) ---

# SEED_ARGS passes flags through, e.g. `make seed SEED_ARGS=-reset`.
SEED_ARGS ?=
# LOADTEST_ARGS likewise, e.g. `make loadtest LOADTEST_ARGS="-duration 60s"`.
LOADTEST_ARGS ?=

seed: ## Seed the load-test org (5 properties, 50 units, 40 renters); idempotent
	@set -a; [ -f .env ] && . ./.env; set +a; \
		cd backend && go run ./cmd/seed $(SEED_ARGS)

seed-demo: ## Top up the JJnE Rentals demo org so every FLOWS screen has data
	@set -a; [ -f .env ] && . ./.env; set +a; \
		cd backend && go run ./cmd/seed -demo $(SEED_ARGS)

loadtest: ## Hammer the hot endpoints for 20s (needs `make seed` and a running API)
	@set -a; [ -f .env ] && . ./.env; set +a; \
		cd backend && go run ./cmd/loadtest $(LOADTEST_ARGS)

# --- build / test / lint ---

build: ## Build backend, proxy and all Next.js apps (next builds are slow)
	@cd backend && go build ./...
	@mkdir -p $(DEVDIR) && cd proxy && go build -o ../$(DEVDIR)/proxy-bin .
	@NEXT_DIST_DIR=.next-build npm run build --workspaces --if-present

# -p 1 runs one package at a time: every DB-backed package truncates the SAME
# tms_test database between tests, so packages running in parallel wipe each
# other's rows and fail at random.
test: ## Run backend Go tests and workspace tests (includes the isolation suite)
	@cd backend && TEST_DATABASE_URL="$${TEST_DATABASE_URL:-postgres://tms:tms_dev@localhost:5433/tms_test?sslmode=disable}" go test -p 1 ./...
	@npm test --workspaces --if-present

# The SPEC §8 suite on its own, for the tight loop while adding a route. The
# route-census test needs no database, so it still fails usefully (with the list
# of uncovered routes) on a machine with no compose stack running.
test-isolation: ## Run only the org/renter/admin isolation suite (SPEC §8)
	@cd backend && TEST_DATABASE_URL="$${TEST_DATABASE_URL:-postgres://tms:tms_dev@localhost:5433/tms_test?sslmode=disable}" \
		go test -v -count=1 ./internal/httpserver/ \
		-run 'TestIsolationSuiteCoversEveryRoute|TestCrossOrgIsolationSuite|TestCrossRenterIsolationSuite|TestAdminRoutesRefuseTenantSessions|TestPart2CrossOrgProbes|TestRenterCannotReadAnotherRentersLanguage'

# The three packages that run goroutines of their own: the HTTP handlers, the
# notification scheduler/worker pool, and the payment allocator. -race makes
# each test far slower, so this is a separate target rather than part of
# `make test`: the run takes ~2m15s (httpserver ~2m of it), against ~10s plain.
test-race: ## Run the concurrent packages under the race detector
	@cd backend && TEST_DATABASE_URL="$${TEST_DATABASE_URL:-postgres://tms:tms_dev@localhost:5433/tms_test?sslmode=disable}" \
		go test -race -p 1 -count=1 ./internal/httpserver/... ./internal/notify/... ./internal/payment/...

i18n-check: ## Verify sw/en dictionaries share the same keys (enduser + tenant)
	@for a in enduser tenant; do if [ -d apps/$$a/i18n ]; then node packages/ui/scripts/i18n-check.mjs apps/$$a/i18n || exit 1; fi; done

lint: ## Lint backend (golangci-lint, falls back to go vet) and frontends
	@cd backend && \
		if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; \
		else echo "note: golangci-lint not installed — falling back to 'go vet ./...'"; \
		     echo "      install: brew install golangci-lint"; \
		     go vet ./...; fi
	@npm run lint --workspaces --if-present
	@$(MAKE) --no-print-directory i18n-check

# --- deployment (full compose profile) ---

.PHONY: images deploy deploy-down logs tls-selfsigned

images: ## Build all deployable images (api, 3 apps, proxy)
	docker compose --profile full build

deploy: images ## Build images and bring the full stack up (proxy on $PROXY_HTTP_PORT, default 80)
	docker compose --profile full up -d --wait api enduser tenant admin proxy
	@echo "full stack up — proxy on http://localhost:$${PROXY_HTTP_PORT:-80}  (/enduser /tenant /admin /api)"

# Stops and removes only the application containers. Postgres, Redis and MinIO
# are deliberately left running: they are shared with the dev loop and hold the
# data volumes.
deploy-down: ## Stop and remove the full-profile app containers (infra stays up)
	docker compose --profile full stop api enduser tenant admin proxy
	docker compose --profile full rm -f api enduser tenant admin proxy
	@echo "full-profile services removed; postgres/redis/minio still running."

logs: ## Tail logs from the full-profile services
	docker compose --profile full logs -f --tail=100

# --- server deploy (API + edge only; frontends on Vercel) ---
#
# docker-compose.deploy.yml. ENV_FILE holds every value (copy
# .env.deploy.example). INFRA=reuse (default) expects postgres/redis/minio to
# exist on INFRA_NETWORK already; INFRA=own runs them under the `infra` profile.
#
# Images are never built on the server: `make docker-deploy` pulls API_IMAGE /
# EDGE_IMAGE (from ENV_FILE) that `make docker-publish` pushed from a laptop.
#
#   make docker-deploy                 # reuse existing infra
#   make docker-deploy INFRA=own       # bring up own postgres/redis/minio too
#   make docker-deploy TAG=abc1234     # pin both images to a tag for this rollout

.PHONY: docker-network docker-pull docker-buckets docker-deploy docker-down \
        docker-logs docker-ps docker-restart docker-migrate docker-psql

ENV_FILE ?= .env.deploy
INFRA ?= reuse
DEPLOY_COMPOSE := ENV_FILE=$(ENV_FILE) docker compose -f docker-compose.deploy.yml --env-file $(ENV_FILE)
ifeq ($(INFRA),own)
DEPLOY_COMPOSE += --profile infra
endif

# Reads a key from ENV_FILE (deploy targets never source it into the shell,
# so secrets stay out of `make -n` output).
deploy-env = $$(grep -E '^$(1)=' $(ENV_FILE) | tail -n1 | cut -d= -f2-)

docker-network: ## Create INFRA_NETWORK (from ENV_FILE, default tms-infra) if missing
	@[ -f $(ENV_FILE) ] || { echo "missing $(ENV_FILE) — cp .env.deploy.example $(ENV_FILE) and edit"; exit 1; }
	@n=$(call deploy-env,INFRA_NETWORK); n=$${n:-tms-infra}; 		docker network inspect "$$n" >/dev/null 2>&1 || docker network create "$$n"; 		echo "network: $$n"

# `make docker-deploy TAG=...` pins both images for that invocation
# (API_IMAGE/EDGE_IMAGE in ENV_FILE otherwise). Exported so compose
# interpolation sees them; shell env beats --env-file in compose.
ifeq ($(origin TAG),command line)
export API_IMAGE  = $(IMAGE_REPO)/tms-api:$(TAG)
export EDGE_IMAGE = $(IMAGE_REPO)/tms-edge:$(TAG)
endif

docker-pull: ## Pull the api and edge images from the registry
	$(DEPLOY_COMPOSE) pull api edge

docker-buckets: docker-network ## Create the MinIO buckets on MINIO_ENDPOINT (own or reused MinIO)
	$(DEPLOY_COMPOSE) --profile init run --rm --no-deps minio-init

docker-deploy: docker-network ## Pull + run api and edge from the registry (INFRA=own adds postgres/redis/minio; TAG= pins)
ifeq ($(INFRA),own)
	$(DEPLOY_COMPOSE) up -d --wait postgres redis minio
endif
	$(DEPLOY_COMPOSE) pull api edge
	@$(MAKE) --no-print-directory docker-buckets ENV_FILE=$(ENV_FILE) INFRA=$(INFRA)
	$(DEPLOY_COMPOSE) up -d --wait api edge
	@p=$(call deploy-env,TMS_EDGE_PORT); echo "deployed — edge on http://127.0.0.1:$${p:-3300}  (/api, /healthz, bucket prefixes)"
	@$(DEPLOY_COMPOSE) images api edge

docker-restart: ## Restart api + edge without rebuilding
	$(DEPLOY_COMPOSE) restart api edge

docker-down: ## Stop and remove api + edge (own infra and volumes untouched); INFRA=own stops infra too
	$(DEPLOY_COMPOSE) down

docker-logs: ## Tail deployed service logs (SERVICE=api to narrow)
	$(DEPLOY_COMPOSE) logs -f --tail=100 $(SERVICE)

docker-ps: ## Show deployed containers and health
	$(DEPLOY_COMPOSE) ps

docker-migrate: ## Run `api migrate up` once against DATABASE_URL (normally automatic at boot)
	$(DEPLOY_COMPOSE) run --rm --no-deps api migrate up

docker-psql: ## psql into DATABASE_URL via the postgres image (works for own or reused Postgres)
	@n=$(call deploy-env,INFRA_NETWORK); docker run --rm -it --network "$${n:-tms-infra}" postgres:17-alpine psql "$(call deploy-env,DATABASE_URL)"

# --- image publish (Docker Hub, linux/amd64) ---
#
# Local builds run on an x86_64 buildx builder (`make docker-builder`, once)
# so the images match the server no matter what the laptop is. This is the
# only place images are built; the server (`make docker-deploy`) only pulls.
#
#   make docker-publish                 # api + edge -> $(IMAGE_REPO)/tms-{api,edge}:$(TAG) and :latest
#   make docker-publish TAG=v1.2.0      # explicit tag (default: short git sha)
#   make docker-publish PUSH=0          # build only, load into local docker
#   make docker-publish IMAGE_REPO=me   # another Docker Hub namespace / registry prefix
#   make docker-publish CACHE=1         # also export the build cache to the registry
#
# CACHE=1 pushes the full buildkit cache (golang base + module cache, >1 GB)
# as $(IMAGE)/…:buildcache after the image. It makes CI cold builds faster but
# on a home uplink the upload takes far longer than the build, so it is off
# by default; local rebuilds use the builder's own cache anyway.

.PHONY: docker-builder docker-publish docker-publish-api docker-publish-edge docker-login

IMAGE_REPO ?= josephchuchu
TAG        ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
PLATFORM   ?= linux/amd64
PUSH       ?= 1
CACHE      ?= 0
BUILDER    ?= tms-x86
API_IMAGE_NAME  := $(IMAGE_REPO)/tms-api
EDGE_IMAGE_NAME := $(IMAGE_REPO)/tms-edge
ifeq ($(PUSH),1)
BUILDX_OUT := --push
else
BUILDX_OUT := --load
endif

docker-builder: ## Create + select the x86_64 buildx builder (once per machine)
	@docker buildx inspect $(BUILDER) >/dev/null 2>&1 || \
		docker buildx create --name $(BUILDER) --driver docker-container --platform $(PLATFORM) --bootstrap
	@docker buildx use $(BUILDER)
	@docker buildx inspect $(BUILDER) | grep -E "^Name|Platforms"

docker-login: ## docker login to Docker Hub (interactive; CI uses secrets instead)
	docker login

docker-publish-api: ## Build + push the API image for $(PLATFORM)
	docker buildx build --platform $(PLATFORM) -f backend/Dockerfile \
		-t $(API_IMAGE_NAME):$(TAG) -t $(API_IMAGE_NAME):latest \
		--cache-from type=registry,ref=$(API_IMAGE_NAME):buildcache \
		$(if $(filter 1,$(CACHE)),--cache-to type=registry$(,)ref=$(API_IMAGE_NAME):buildcache$(,)mode=max) \
		--label org.opencontainers.image.revision=$(TAG) \
		$(BUILDX_OUT) .

docker-publish-edge: ## Build + push the edge proxy image for $(PLATFORM)
	docker buildx build --platform $(PLATFORM) -f proxy/Dockerfile \
		-t $(EDGE_IMAGE_NAME):$(TAG) -t $(EDGE_IMAGE_NAME):latest \
		--cache-from type=registry,ref=$(EDGE_IMAGE_NAME):buildcache \
		$(if $(filter 1,$(CACHE)),--cache-to type=registry$(,)ref=$(EDGE_IMAGE_NAME):buildcache$(,)mode=max) \
		--label org.opencontainers.image.revision=$(TAG) \
		$(BUILDX_OUT) .

docker-publish: docker-publish-api docker-publish-edge ## Build + push api and edge images (TAG, PUSH=0 to only build)
	@echo "published $(API_IMAGE_NAME):$(TAG) and $(EDGE_IMAGE_NAME):$(TAG) (+ :latest) for $(PLATFORM)"
	@echo "server: make docker-deploy TAG=$(TAG)   (or :latest with plain make docker-deploy)"

# --- server ingress: nginx + certbot (run ON the server, Debian/Ubuntu) ---
#
#   make server-setup                          # nginx site + Let's Encrypt for api.tms.kuzo.co.tz
#   make server-setup DOMAIN=api.example.com EMAIL=ops@example.com
#
# DOMAIN must already resolve to this server. EMAIL (Let's Encrypt expiry
# notices) defaults to ADMIN_EMAIL from ENV_FILE. The edge port comes from
# TMS_EDGE_PORT in ENV_FILE (default 3300). Renewal is the certbot systemd
# timer the package installs; nothing else to schedule.

.PHONY: server-setup server-deps server-nginx server-cert server-check

DOMAIN ?= api.tms.kuzo.co.tz
EMAIL  ?= $(shell grep -E '^ADMIN_EMAIL=' $(ENV_FILE) 2>/dev/null | tail -n1 | cut -d= -f2-)
SUDO   ?= $(shell [ "$$(id -u)" = 0 ] || echo sudo)
NGINX_SITE := /etc/nginx/sites-available/$(DOMAIN).conf

server-deps: ## apt install nginx, certbot and the nginx plugin (idempotent)
	@command -v nginx >/dev/null && command -v certbot >/dev/null && echo "nginx + certbot present" || \
		( $(SUDO) apt-get update -qq && $(SUDO) DEBIAN_FRONTEND=noninteractive apt-get install -y -qq nginx certbot python3-certbot-nginx )
	@$(SUDO) systemctl enable --now nginx >/dev/null

server-nginx: server-deps ## Render deploy/nginx/site.conf.template for DOMAIN -> sites-enabled, reload nginx
	@port=$(call deploy-env,TMS_EDGE_PORT); port=$${port:-3300}; \
		DOMAIN=$(DOMAIN) TMS_EDGE_PORT=$$port envsubst '$$DOMAIN $$TMS_EDGE_PORT' \
		< deploy/nginx/site.conf.template | $(SUDO) tee $(NGINX_SITE) >/dev/null; \
		echo "wrote $(NGINX_SITE) -> 127.0.0.1:$$port"
	@$(SUDO) mkdir -p /var/www/html
	@$(SUDO) ln -sfn $(NGINX_SITE) /etc/nginx/sites-enabled/$(DOMAIN).conf
	@$(SUDO) nginx -t && $(SUDO) systemctl reload nginx
	@echo "nginx: http://$(DOMAIN) -> edge"

server-cert: ## Issue/renew the Let's Encrypt cert for DOMAIN via certbot --nginx (adds 443 + redirect)
	@[ -n "$(EMAIL)" ] || { echo "EMAIL required (or set ADMIN_EMAIL in $(ENV_FILE)): make server-cert EMAIL=you@example.com"; exit 1; }
	$(SUDO) certbot --nginx -d $(DOMAIN) -m $(EMAIL) --agree-tos --no-eff-email --non-interactive --redirect --keep-until-expiring
	@$(SUDO) nginx -t && $(SUDO) systemctl reload nginx
	@$(SUDO) certbot renew --dry-run -q && echo "renewal: certbot.timer ok"

server-setup: server-nginx server-cert ## nginx ingress + TLS for DOMAIN (default api.tms.kuzo.co.tz); then make server-check
	@echo "ingress ready: https://$(DOMAIN)  (set MINIO_PUBLIC_URL=https://$(DOMAIN) in $(ENV_FILE) if not already)"

server-check: ## Hit the public healthz through nginx + TLS
	@curl -fsS https://$(DOMAIN)/api/v1/healthz && echo || { echo "healthz failed — is the stack up? make docker-ps"; exit 1; }

tls-selfsigned: ## Generate a self-signed cert/key pair into .dev/tls for the proxy
	@mkdir -p $(DEVDIR)/tls
	@openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
		-keyout $(DEVDIR)/tls/key.pem -out $(DEVDIR)/tls/cert.pem \
		-subj "/CN=$${TLS_CN:-localhost}" \
		-addext "subjectAltName=DNS:$${TLS_CN:-localhost},DNS:localhost,IP:127.0.0.1" 2>/dev/null
	@echo "wrote $(DEVDIR)/tls/cert.pem and $(DEVDIR)/tls/key.pem"
	@echo "enable TLS: set TLS_CERT_FILE=/etc/tms/tls/cert.pem TLS_KEY_FILE=/etc/tms/tls/key.pem in .env, then make deploy"
