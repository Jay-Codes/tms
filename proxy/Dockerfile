# TMS edge proxy — multi-stage build.
#
# Build context is the repo root:
#   docker build -f proxy/Dockerfile -t tms-proxy .
#
# The proxy has no module dependencies, so the build stage is a single compile.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY proxy/go.mod ./
COPY proxy/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/proxy .

FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates \
 && adduser -D -u 10001 tms
COPY --from=build /out/proxy /usr/local/bin/proxy

USER tms
# Upstreams and listen addresses come from the environment; the defaults in
# proxy/main.go keep `make dev` (host processes on localhost, :8080) working.
ENV PROXY_HTTP_ADDR=:80
EXPOSE 80 443

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=5 \
  CMD wget -qO- "http://127.0.0.1${PROXY_HTTP_ADDR}/healthz" >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/proxy"]
