# Patchdeck

**Agentless patch management dashboard for Debian & Ubuntu servers.**

Patchdeck gives you a single pane of glass over your Linux fleet's patch status — scan for updates, apply them, restart services, and schedule recurring maintenance. No agents to install on your hosts — just SSH.

Built for homelabbers, sysadmins, and small teams who want visibility without enterprise complexity.

![License](https://img.shields.io/badge/license-MIT-blue)

## Features

- **Agentless scanning** — connects over SSH, no agents to deploy or maintain
- **One-click patching** — apply `apt` updates with real-time streaming output
- **Service restart & reboot** — restart specific services or reboot/shutdown hosts from the UI
- **Reboot detection** — surfaces `/var/run/reboot-required` with package-level detail
- **Scheduled maintenance** — cron-based schedules with multi-host and tag-group targeting
- **Host tagging & grouping** — organize hosts by environment, role, or location
- **Activity audit log** — full timeline of scans, applies, reboots, and config changes with configurable retention and CSV export
- **Notifications** — Apprise-powered alerts to Gotify, Telegram, Discord, email, and [80+ services](https://github.com/caronc/apprise)
- **SSH host key verification** — TOFU + manual pinning with full audit trail
- **API tokens** — programmatic access with `Bearer` auth
- **Dark & light themes** — system preference detection with manual toggle
- **Mobile responsive** — works on phones and tablets
- **Two-factor auth** — optional TOTP (Google Authenticator, Authy, etc.) on admin login
- **Encrypted secrets** — AES-GCM at rest for all SSH credentials

## Screenshots

*Coming soon*

## Quick Start

### Prerequisites

- Docker & Docker Compose
- SSH access to your target hosts (password or key auth)

### 1. Clone & configure

```bash
git clone https://github.com/roydufek/patchdeck.git
cd patchdeck
cp .env.example .env
```

Edit `.env` and set strong random secrets:

```bash
# Generate secure keys
openssl rand -hex 32  # Use output for PATCHDECK_MASTER_KEY
openssl rand -hex 32  # Use output for PATCHDECK_JWT_SECRET
```

### 2. Start

**Option A: Build from source**

```bash
docker compose up -d --build
```

**Option B: Pull pre-built images from GHCR** (no build required)

```bash
docker compose -f docker-compose.ghcr.yml up -d
```

Patchdeck will be available at `http://localhost:6070`.

### 3. Create your admin account

Open the web UI and complete the setup wizard to create your admin account with optional TOTP two-factor auth.

### 4. Add hosts

Click **Add Host** and enter your server's SSH connection details. Patchdeck encrypts all credentials at rest.

## Stack

| Component | Technology |
|-----------|-----------|
| Backend | Go (Chi router + SQLite) |
| Frontend | React 18 + Vite + Tailwind CSS |
| Reverse proxy | Caddy |
| Notifications | Apprise CLI (bundled in API image) |
| Deployment | Docker Compose |

## Configuration

All configuration is via environment variables in `.env`:

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `PATCHDECK_MASTER_KEY` | ✅ | — | 32+ char hex string for AES-GCM credential encryption |
| `PATCHDECK_JWT_SECRET` | ✅ | — | 32+ char hex string for JWT signing |
| `PATCHDECK_SSH_TIMEOUT_SECONDS` | | `20` | SSH connection timeout |
| `PATCHDECK_APPRISE_TIMEOUT_SECONDS` | | `10` | Notification delivery timeout |
| `PATCHDECK_APPRISE_URL` | | — | Default Apprise destination URL |
| `REGISTRATION_ENABLED` | | `true` | Set `false` to disable new account registration |

## Architecture

```
┌─────────────┐     ┌─────────────┐
│   Browser    │────▶│  Caddy :6070│
└─────────────┘     └──────┬──────┘
                           │
                    ┌──────▼──────┐
                    │  React SPA  │
                    └──────┬──────┘
                           │ /api/*
                    ┌──────▼──────┐
                    │  Go API     │
                    │  + SQLite   │
                    │  + Apprise  │
                    │  + Scheduler│
                    └──────┬──────┘
                           │ SSH
                    ┌──────▼──────┐
                    │ Your hosts  │
                    └─────────────┘
```

## Security

- **Credentials encrypted at rest** — AES-GCM with a 32-byte master key
- **Password hashing** — bcrypt
- **JWT auth** — HS256 with 12-hour TTL
- **TOTP two-factor** — optional time-based one-time password on login
- **SSH host key verification** — TOFU with optional manual pinning; mismatches block operations until resolved
- **Parameterized SQL** — no raw string interpolation
- **Rate limiting** — 30-second per-host cooldown on scan/apply
- **Audit trail** — all operations logged with retention policy

For a detailed security review, see [docs/SECURITY_REVIEW.md](docs/SECURITY_REVIEW.md).

## API

Patchdeck exposes a REST API. Authenticate with either a JWT (from login) or an API token (`Bearer pd_...`).

Key endpoints:

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/login` | Authenticate (returns JWT) |
| `GET` | `/api/hosts` | List all hosts |
| `POST` | `/api/hosts` | Add a host |
| `POST` | `/api/hosts/:id/scan` | Scan a host for updates (SSE stream) |
| `POST` | `/api/hosts/:id/apply` | Apply updates (SSE stream) |
| `POST` | `/api/hosts/:id/power` | Reboot or shutdown |
| `GET` | `/api/activity` | Activity log (paginated) |
| `GET` | `/api/activity/export` | Export activity as CSV |
| `GET/POST` | `/api/jobs` | List/create scheduled jobs |
| `GET/PUT` | `/api/settings/*` | Notification, audit, and token settings |

Full API documentation is planned for a future release.

## Development

```bash
# Backend
cd api
go build ./...
go test ./...

# Frontend
cd web
npm install
npm run dev    # Dev server with HMR
npm run build  # Production build
```

## Roadmap

- [ ] Webhooks for external integrations
- [ ] Role-based access control (RBAC)
- [ ] Multi-user management
- [ ] RPM/dnf support (RHEL, Fedora, Rocky)
- [ ] Notification log with delivery status
- [ ] Dashboard metrics and charts

## License

[MIT](LICENSE)

## Contributing

Patchdeck is in early development. Issues and pull requests are welcome.
