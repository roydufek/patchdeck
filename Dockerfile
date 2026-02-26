# syntax=docker/dockerfile:1.7

# --- Build frontend ---
FROM node:22-alpine AS web-build
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web .
RUN npm run build

# --- Build backend ---
FROM golang:1.22-alpine AS api-build
WORKDIR /src
RUN apk add --no-cache git
COPY api/go.mod ./api/go.mod
RUN cd api && go mod download
COPY api ./api
RUN cd api && CGO_ENABLED=0 go build -ldflags='-s -w' -o /out/patchdeck ./cmd/server

# --- Final runtime image ---
FROM python:3.12-alpine
WORKDIR /app
RUN apk add --no-cache ca-certificates \
    && pip install --no-cache-dir apprise \
    && apprise --version \
    && adduser -D -H -u 10001 patchdeck \
    && mkdir -p /data && chown patchdeck:patchdeck /data

COPY --from=api-build /out/patchdeck /app/patchdeck
COPY --from=web-build /web/dist /app/static

USER patchdeck
EXPOSE 6070
ENTRYPOINT ["/app/patchdeck"]
