# TMS enduser app — Next.js standalone build.
#
# Build context is the repo root (npm workspaces: the lockfile and the
# @tms/ui package both live above apps/):
#   docker build -f apps/enduser/Dockerfile -t tms-enduser .
FROM node:24-alpine AS deps
WORKDIR /repo
# Manifests only, so the install layer is cached until a package.json changes.
COPY package.json package-lock.json ./
COPY apps/enduser/package.json apps/enduser/
COPY apps/tenant/package.json apps/tenant/
COPY apps/admin/package.json apps/admin/
COPY packages/ui/package.json packages/ui/
RUN npm ci

FROM node:24-alpine AS build
WORKDIR /repo
ENV NEXT_TELEMETRY_DISABLED=1
COPY --from=deps /repo/node_modules ./node_modules
COPY . .
# next.config.js sets output:'standalone' + outputFileTracingRoot=/repo, so the
# traced server lands in apps/enduser/.next/standalone/apps/enduser/server.js with a
# pruned node_modules tree at the standalone root.
RUN npm run build --workspace apps/enduser

FROM node:24-alpine AS runtime
WORKDIR /app
RUN addgroup -g 10001 -S tms && adduser -u 10001 -S tms -G tms
ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    HOSTNAME=0.0.0.0 \
    PORT=3001

COPY --from=build --chown=tms:tms /repo/apps/enduser/.next/standalone ./
COPY --from=build --chown=tms:tms /repo/apps/enduser/.next/static ./apps/enduser/.next/static
COPY --from=build --chown=tms:tms /repo/apps/enduser/public ./apps/enduser/public

USER tms
EXPOSE 3001
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=6 \
  CMD wget -qO- "http://127.0.0.1:$PORT/enduser" >/dev/null || exit 1

CMD ["node", "apps/enduser/server.js"]
