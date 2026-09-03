<p align="center">
  <img src="api/cmd/server/spikeassets/logo.png" alt="Patchdeck" width="96" height="96" />
</p>

<h1 align="center">Patchdeck</h1>

<p align="center">
  <strong>The agentless patch dashboard for your homelab.</strong><br>
  Scan, apply, reboot, and schedule updates across your Debian &amp; Ubuntu boxes — over plain SSH, from one self-hosted pane of glass.
</p>

<p align="center">
  <a href="#quick-start">Quick Start</a> · <a href="#features">Features</a> · <a href="#configuration">Configuration</a> · <a href="#api">API</a> · <a href="#development">Development</a>
</p>

<p align="center">
  <a href="https://github.com/roydufek/patchdeck/actions/workflows/build-images.yml"><img alt="Build" src="https://github.com/roydufek/patchdeck/actions/workflows/build-images.yml/badge.svg" /></a>
  <a href="https://github.com/roydufek/patchdeck/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/release/roydufek/patchdeck?sort=semver&color=1f9268" /></a>
  <a href="https://github.com/roydufek/patchdeck/pkgs/container/patchdeck"><img alt="Container image" src="https://img.shields.io/badge/ghcr.io-patchdeck-2496ED?logo=docker&logoColor=white" /></a>
  <img alt="Go" src="https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white" />
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/roydufek/patchdeck?color=blue" /></a>
</p>

---

Keeping a homelab patched is death by a thousand `ssh box && sudo apt update && sudo apt upgrade`. Patchdeck gives you **one place to see what needs updating across every box** — your Proxmox VMs and LXC containers, your Raspberry Pis, that NUC in the closet, bare metal, anything you can reach over SSH — and to actually do something about it.

It's **agentless**: nothing to install on your hosts, no daemon phoning home. Patchdeck connects out over SSH, checks `apt`, and streams the output back live. It runs as a **single container** right next to the rest of your self-hosted stack, encrypts every credential at rest, and has **no cloud, no telemetry, and no account** — your fleet's details never leave the box.

<p align="center">
  <img src="docs/screenshots/dashboard-dark-desktop.png" alt="Patchdeck dashboard" width="840" />
</p>

<details>
<summary align="center"><strong>More screenshots — host detail, settings, schedules, activity, light &amp; mobile</strong></summary>
<br>
<p align="center"><em>Dashboard — light</em><br><img src="docs/screenshots/dashboard-light-desktop.png" alt="Dashboard (light)" width="820" /></p>
<p align="center"><em>Host detail — packages, reboot/restart, per-host settings</em><br><img src="docs/screenshots/host-detail-dark-desktop.png" alt="Host detail" width="820" /></p>
<p align="center"><em>Settings — notifications, SSO, tokens, 2FA</em><br><img src="docs/screenshots/settings-dark-desktop.png" alt="Settings" width="820" /></p>
<p align="center"><em>Schedules — cron scan/apply per host, tag, or fleet</em><br><img src="docs/screenshots/schedules-dark-desktop.png" alt="Schedules" width="820" /></p>
<p align="center"><em>Activity log</em><br><img src="docs/screenshots/activity-dark-desktop.png" alt="Activity log" width="820" /></p>
<p align="center"><em>Mobile</em><br>
  <img src="docs/screenshots/dashboard-dark-mobile.png" alt="Dashboard (mobile)" width="260" />
  &nbsp;&nbsp;
  <img src="docs/screenshots/host-detail-light-mobile.png" alt="Host detail (mobile)" width="260" />
</p>
</details>

### Fits the way you already run things

- **Point it at your fleet over SSH** — password or key auth, per host. First connection to a host pauses so **you review and approve its SSH key** before Patchdeck ever uses it.
- **Behind your reverse proxy** — Traefik, Caddy, Nginx Proxy Manager. Serves HTTPS on its own by default; flip one env var to let your proxy terminate TLS.
- **Through your notification stack** — native alerts to Gotify, ntfy, Discord, Telegram, Slack, Pushover, email, or a webhook. No Apprise/python container to babysit.
- **With your homelab login** — optional single sign-on via any OIDC provider (Authentik, Authelia, Keycloak, Pocket ID, …), or a local admin with TOTP two-factor.
- **On your phone or your desktop** — the UI is responsive and theme-aware (system / light / dark), so a quick "what needs patching?" check works from the couch.

## Features

- **Agentless scanning** — connects out over SSH; nothing to deploy or maintain on your hosts
- **One-click patching** — apply `apt` updates with real-time streaming output, right in the browser
- **Intelligent service restart** — one **Restart services** action picks the right method per unit (a clean restart where it's safe, needrestart's coordinated handler where a naive restart would sever the session). Anything that comes back still flagged is **learned** and moved to a **reboot-required** bucket on its own — so you never have to guess which services actually need a reboot. A reboot (via Patchdeck or out-of-band) clears the learned set automatically.
- **Reboot detection &amp; scoped fleet reboot** — surfaces `reboot-required` plus the exact services needing attention, split into restart-now vs reboot-required. **Reboot all** reboots only the hosts that need it — and never the host Patchdeck runs on, which it detects automatically (you can protect any other host too), each with its own "waiting for it to come back" watch
- **First-connection host-key approval** — every new host's SSH key is captured and **held for your approval** before it's trusted; a changed key later pauses operations for re-approval (fail-closed, no silent trust)
- **Live, filterable dashboard** — hosts that **need attention** are pinned on top and **re-file themselves the moment a scan changes their status** (no reload); filter by facet (updates · reboot/restart · healthy), search by name or tag, and optionally **group by tag** (tag hosts by role/site/environment)
- **Scheduled maintenance** — cron schedules targeting a single host, a tag, several hosts, or the whole fleet, with per-job run history (last/next run, per-host outcomes)
- **Bulk actions** — scan, apply, or reboot across the fleet, each host streaming independently on its own card; concurrency-capped (`PATCHDECK_BULK_CONCURRENCY`) with an optional apply stagger (`PATCHDECK_APPLY_STAGGER_SECONDS`) so a large fleet doesn't bounce everything at once
- **Activity log** — a timeline of every scan, apply, restart, reboot, and change, with configurable retention and CSV export
- **Notifications** — native (no external dependency) alerts to Gotify, ntfy, Discord, Telegram, Slack, Pushover, email (SMTP), and webhooks — global and **per-host overrides**
- **Single sign-on** — optional OIDC (Authorization Code + PKCE) alongside local password login
- **Two-factor auth** — optional TOTP with one-time recovery codes on the local admin
- **API tokens** — programmatic access with `Bearer` auth
- **Encrypted secrets** — AES-256-GCM at rest for every SSH credential
- **HTTPS by default** — auto-generated self-signed cert out of the box; one env var to defer to your reverse proxy

## Quick Start

### Prerequisites

- Docker &amp; Docker Compose
- SSH access to your target hosts (password or key auth)

### 1. Create your project directory

```bash
mkdir patchdeck && cd patchdeck
```

### 2. Create your `.env` file

```bash
# Generate the one required secret (encrypts SSH credentials at rest)
echo "PATCHDECK_MASTER_KEY=$(openssl rand -hex 32)" > .env
```

### 3. Create your `compose.yaml`

```yaml
services:
  patchdeck:
    image: ghcr.io/roydufek/patchdeck:latest
    container_name: patchdeck
    restart: unless-stopped
    ports:
      - "6070:6070"
    environment:
      PUID: 1000                                          # set to match your host user
      PGID: 1000                                          # set to match your host group
      PATCHDECK_PORT: 6070
      PATCHDECK_MASTER_KEY: ${PATCHDECK_MASTER_KEY}
      #TZ: America/Los_Angeles                             # timezone for scheduled jobs (default UTC)
      #PATCHDECK_TLS: true                                 # default; set false if behind a reverse proxy
      #PATCHDECK_DB_PATH: /data/patchdeck.db               # default, optional
      #PATCHDECK_SSH_TIMEOUT_SECONDS: 20                   # default, optional — SSH dial timeout
      #PATCHDECK_EXEC_TIMEOUT_SECONDS: 600                 # default, optional — max wall-clock for a remote command
      #PATCHDECK_CONNECTIVITY_TIMEOUT_SECONDS: 15          # default, optional — live connectivity-check timeout
      #PATCHDECK_APPRISE_URL: gotifys://gotify.example.com/TOKEN    # optional — default notification destination
    volumes:
      - ./data:/data
```

### 4. Start

```bash
docker compose up -d
```

Patchdeck will be available at `https://localhost:6070` (or `https://<your-server-ip>:6070`).

> **Note:** Patchdeck serves HTTPS by default with an auto-generated self-signed certificate, so your browser will warn on first visit — that's expected. Behind a reverse proxy (Traefik, Caddy, Nginx Proxy Manager) set `PATCHDECK_TLS=false` so you don't double-encrypt.

### 5. Create your admin account

Open the web UI and create the first admin account. You can turn on TOTP two-factor and wire up OIDC single sign-on afterwards in **Settings**.

### 6. Add hosts

Hit **+** to add a server's SSH details. Patchdeck encrypts the credentials at rest, then walks you through **approving the host's SSH key** on first connect before it scans.

## Building from Source

```bash
git clone https://github.com/roydufek/patchdeck.git
cd patchdeck
cp .env.example .env    # then edit PATCHDECK_MASTER_KEY
docker compose up -d --build
```

## Stack

| Component | Technology |
|-----------|-----------|
| Backend | Go 1.25 — Chi router + pure-Go SQLite (WAL), native Go scheduler &amp; notifications |
| Frontend | **Server-rendered** Go `html/template` + [HTMX](https://htmx.org) + SSE — no Node, no bundler, no build step |
| Runtime | A single ~30 MB static Go binary on Alpine; assets embedded — no interpreter, tiny CVE surface |
| Deployment | Docker Compose (one container, one SQLite file) |

> Patchdeck's UI is rendered by the Go binary itself — there is **no JavaScript build pipeline**, no Node, no bundler. The whole thing is one container and one `./data` folder to back up.

## Configuration

All configuration is via environment variables. Only `PATCHDECK_MASTER_KEY` is required — everything else has sensible defaults.

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `PATCHDECK_MASTER_KEY` | ✅ | — | 32+ char secret; an AES-256 key is derived from it (HKDF) to encrypt SSH credentials at rest |
| `PUID` / `PGID` | | `1000` | User/group ID for the container process (linuxserver.io convention) |
| `TZ` | | `UTC` | Timezone for **scheduled jobs** (IANA name, e.g. `America/Los_Angeles`). Cron fires in this zone; tzdata is embedded, so no extra package needed |
| `PATCHDECK_DB_PATH` | | `/data/patchdeck.db` | SQLite database path inside the container |
| `PATCHDECK_SSH_TIMEOUT_SECONDS` | | `20` | SSH dial/handshake timeout |
| `PATCHDECK_EXEC_TIMEOUT_SECONDS` | | `600` | Max wall-clock for a single remote command (scan/apply) |
| `PATCHDECK_CONNECTIVITY_TIMEOUT_SECONDS` | | `15` | Timeout for the live dashboard connectivity check |
| `PATCHDECK_BULK_CONCURRENCY` | | `4` | Max hosts a fleet-wide action (scan/apply/reboot all) drives at once; `<1` = no cap |
| `PATCHDECK_APPLY_STAGGER_SECONDS` | | `0` | Minimum gap between successive apply-all starts (blast-radius safety); `0` = off |
| `PATCHDECK_APPRISE_TIMEOUT_SECONDS` | | `10` | Notification delivery (HTTP/SMTP) timeout |
| `PATCHDECK_APPRISE_URL` | | — | Default notification destination URL (see [Notifications](#notifications)) |
| `PATCHDECK_TLS` | | `true` | Enable HTTPS with an auto-generated self-signed cert; set `false` if behind a reverse proxy |
| `PATCHDECK_TLS_CERT` / `PATCHDECK_TLS_KEY` | | `/data/tls/…` | Paths to a custom TLS cert/key (auto-generated if missing) |

## Architecture

```
        Browser  (phone / desktop)
           │  :6070 HTTPS
┌──────────▼───────────────────┐
│  Patchdeck  (one container)   │
│  ┌─────────────────────────┐  │
│  │ Go server                │  │
│  │  · JSON API + token auth │  │
│  │  · server-rendered UI    │  │   html/template + HTMX + SSE
│  │  · SQLite (WAL)          │  │
│  │  · native notifications  │  │
│  │  · cron scheduler        │  │
│  └───────────┬─────────────┘  │
└──────────────┼────────────────┘
               │ outbound SSH
     ┌─────────▼──────────┐
     │  your Linux hosts   │   VMs · LXC · Pis · bare metal
     └────────────────────┘
```

## Security

- **Credentials encrypted at rest** — AES-256-GCM; the key is derived from `PATCHDECK_MASTER_KEY` via HKDF-SHA256, and secrets are never returned by the API
- **Fail-closed SSH host keys** — first connection is held for **operator approval**; a later key change blocks all operations until you approve or reject it (no silent trust-on-first-use)
- **Cookie sessions** — httpOnly, Secure, SameSite=Lax, server-side store with a sliding 7-day expiry (no JWT, no token in URLs)
- **Login brute-force lockout** — per-IP sliding-window lockout after repeated failures
- **TOTP two-factor** — optional, with one-time recovery codes; plus optional OIDC SSO
- **Command-injection guards** — strict validation of service names and power actions sent over SSH
- **Parameterized SQL**, **bcrypt** password hashing, per-host scan/apply rate limiting, and a full **audit trail**
- **Minimal runtime** — a static Go binary on Alpine, no interpreter or JS runtime in the image; both `trivy` and `grype` gate every release for fixable HIGH/CRITICAL CVEs

## Notifications

Patchdeck delivers alerts (updates available, apply success/failure, scan failure) **natively in Go** — no python or Apprise dependency in the image. Configure a single destination URL in **Settings → Notifications** (or via `PATCHDECK_APPRISE_URL`), with optional **per-host overrides**. URLs follow the familiar Apprise-style scheme, so most existing URLs keep working.

| Service | URL format | Notes |
|---------|-----------|-------|
| **Gotify** | `gotifys://host/TOKEN` | `gotify://` = http, `gotifys://` = https. `?priority=N` optional |
| **ntfy** | `ntfys://host/topic` or `ntfy://topic` | topic-only uses ntfy.sh. `user:pass@` or `?token=` for auth |
| **Discord** | `discord://webhook_id/webhook_token` | from a Discord channel webhook URL |
| **Telegram** | `tgram://bot_token/chat_id` | one chat id per URL |
| **Slack** | `slack://TokenA/TokenB/TokenC[/channel]` | incoming-webhook tokens |
| **Pushover** | `pover://user_key@app_token` | `?priority=N` optional |
| **Email (SMTP)** | `mailtos://user:pass@smtp.host[:port]/?to=you@example.com` | `mailtos://` = TLS (465), `mailto://` = STARTTLS (587). Explicit SMTP host required |
| **Webhook** | `jsons://host/path` or `forms://host/path` | `json*` posts JSON `{title,message,…}`, `form*` posts form data |

> One destination per instance — use your notification backend (an ntfy topic, a webhook) for fan-out. Hit **Send test** to verify a URL before saving.

## API

The web UI authenticates via an httpOnly session cookie. For automation, create an API token in **Settings** and send it as `Authorization: Bearer pd_...`.

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/login` | Authenticate (sets session cookie) |
| `GET` | `/api/hosts` | List all hosts |
| `POST` | `/api/hosts` | Add a host |
| `POST` | `/api/hosts/:id/scan` | Scan a host for updates (SSE stream) |
| `POST` | `/api/hosts/:id/apply` | Apply updates (SSE stream) |
| `POST` | `/api/hosts/:id/power` | Reboot or shutdown |
| `GET` | `/api/activity` · `/api/activity/export` | Activity log (paginated) · CSV export |
| `GET/POST` | `/api/jobs` · `/api/jobs/:id/runs` | Scheduled jobs · run history |
| `GET/PUT` | `/api/settings/*` | Notification, OIDC, audit, and token settings |

## Development

No Node, no bundler — the UI is Go templates + HTMX served by the binary. Editing the front end means editing `.html` templates and rebuilding the binary.

```bash
cd api
go build ./...
go test ./...
go run ./cmd/server    # needs PATCHDECK_MASTER_KEY set
```

Templates and static assets live under `api/cmd/server/` and are embedded at build time (`//go:embed`), so the compiled binary is fully self-contained.

## Roadmap

- [ ] Apply preview / dry-run (`apt-get -s`) with disk-space and reboot prechecks
- [ ] Package holds (`apt-mark`) and security-only update mode
- [ ] Auto-reboot within scheduled maintenance windows
- [ ] Fleet patch-status report / export
- [ ] Notification delivery log (per-attempt success/failure)
- [ ] RPM/dnf support (RHEL, Fedora, Rocky)
- [ ] Dashboard metrics and charts

## License

[MIT](LICENSE)

## Contributing

Issues and pull requests welcome.
