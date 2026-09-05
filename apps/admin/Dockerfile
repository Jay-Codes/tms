# TMS admin app — Next.js standalone build.
#
# Build context is the repo root (npm workspaces: the lockfile and the
# @tms/ui package both live above apps/):
#   docker build -f apps/admin/Dockerfile -t tms-admin .
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
# traced server lands in apps/admin/.next/standalone/apps/admin/server.js with a
# pruned node_modules tree at the standalone root.
RUN npm run build --workspace apps/admin

FROM node:24-alpine AS runtime
WORKDIR /app
RUN addgroup -g 10001 -S tms && adduser -u 10001 -S tms -G tms
ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    HOSTNAME=0.0.0.0 \
    PORT=3003

COPY --from=build --chown=tms:tms /repo/apps/admin/.next/standalone ./
COPY --from=build --chown=tms:tms /repo/apps/admin/.next/static ./apps/admin/.next/static
COPY --from=build --chown=tms:tms /repo/apps/admin/public ./apps/admin/public

USER tms
EXPOSE 3003
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=6 \
  CMD wget -qO- "http://127.0.0.1:$PORT/admin" >/dev/null || exit 1

CMD ["node", "apps/admin/server.js"]
