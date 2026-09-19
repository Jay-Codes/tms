# TMS Go API — multi-stage build.
#
# Build context is the repo root:
#   docker build -f backend/Dockerfile -t tms-api .
#
# Stage 1 compiles a static binary (CGO off) so the runtime image needs no
# libc. Stage 2 is alpine rather than distroless purely so compose can run an
# HTTP healthcheck with busybox wget — distroless has no shell or wget.
# The build stage runs on the builder's own platform and cross-compiles for
# the target (BUILDPLATFORM/TARGETARCH are set by buildx), so an x86_64 image
# built on Apple Silicon does not go through emulation.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS=linux TARGETARCH=amd64
WORKDIR /src

# Dependencies first: this layer is cached until go.mod/go.sum change.
COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./
# Migrations are embedded (backend/migrations + //go:embed), so the binary
# carries the schema — no migration files in the runtime image.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -u 10001 tms
COPY --from=build /out/api /usr/local/bin/api

USER tms
ENV API_PORT=8081
EXPOSE 8081

# MIGRATE_ON_START=1 makes the entrypoint apply pending migrations before
# serving (handled inside cmd/api, so a single process still owns the DB).
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=6 \
  CMD wget -qO- "http://127.0.0.1:${API_PORT}/api/v1/healthz" >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/api"]
