.DEFAULT_GOAL := help

DEVDIR := .dev
NGROK_API := http://127.0.0.1:4040/api/tunnels

.PHONY: help preview dev stop url install up down \
        api api-stop api-restart api-log proxy-restart \
        migrate migrate-down test-db sqlc build test lint

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

# --- build / test / lint ---

build: ## Build backend, proxy and all Next.js apps (next builds are slow)
	@cd backend && go build ./...
	@mkdir -p $(DEVDIR) && cd proxy && go build -o ../$(DEVDIR)/proxy-bin .
	@npm run build --workspaces --if-present

# -p 1 runs one package at a time: every DB-backed package truncates the SAME
# tms_test database between tests, so packages running in parallel wipe each
# other's rows and fail at random.
test: ## Run backend Go tests and workspace tests
	@cd backend && TEST_DATABASE_URL="$${TEST_DATABASE_URL:-postgres://tms:tms_dev@localhost:5433/tms_test?sslmode=disable}" go test -p 1 ./...
	@npm test --workspaces --if-present

lint: ## Lint backend (golangci-lint, falls back to go vet) and frontends
	@cd backend && \
		if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; \
		else echo "note: golangci-lint not installed — falling back to 'go vet ./...'"; \
		     echo "      install: brew install golangci-lint"; \
		     go vet ./...; fi
	@npm run lint --workspaces --if-present
