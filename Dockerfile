# syntax=docker/dockerfile:1.7

# --- Build frontend ---
FROM node:22-alpine AS web-build
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web .
RUN npm run build

# --- Build backend ---
FROM golang:1.25-alpine AS api-build
WORKDIR /src
RUN apk add --no-cache git
COPY api/go.mod ./api/go.mod
RUN cd api && go mod download
COPY api ./api
RUN cd api && CGO_ENABLED=0 go build -ldflags='-s -w' -o /out/patchdeck ./cmd/server

# --- Final runtime image ---
# Plain Alpine — no python/Apprise. Notifications are delivered natively by the Go
# binary, so the runtime carries no interpreter (and none of its CVE surface).
FROM alpine:3.22
WORKDIR /app

# ca-certificates: outbound HTTPS for notifications. su-exec: privilege drop in the
# entrypoint. `apk upgrade` pulls the latest patched base packages (e.g. libcrypto3) so a
# stale base-image snapshot can't carry a fixable CVE past the CI security gate.
# The patchdeck user is created here; the entrypoint adjusts UID/GID at runtime.
RUN apk upgrade --no-cache \
    && apk add --no-cache ca-certificates su-exec \
    && addgroup -g 1000 patchdeck \
    && adduser -D -H -u 1000 -G patchdeck patchdeck \
    && mkdir -p /data && chown patchdeck:patchdeck /data

COPY --from=api-build /out/patchdeck /app/patchdeck
COPY --from=web-build /web/dist /app/static
COPY entrypoint.sh /app/entrypoint.sh

EXPOSE 6070
ENTRYPOINT ["/app/entrypoint.sh"]
