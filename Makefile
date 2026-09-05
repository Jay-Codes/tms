.DEFAULT_GOAL := help

DEVDIR := .dev
NGROK_API := http://127.0.0.1:4040/api/tunnels

.PHONY: help preview dev stop url install up down

help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "} {printf "  make %-10s %s\n", $$1, $$2}'

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
