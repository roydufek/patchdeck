# Patchdeck v1 Architecture

## Components
- **web**: React SPA for host management, scan/apply actions, schedules, audit views.
- **api**: Go REST API handling auth, host inventory, encrypted credentials, SSH execution, scheduler.
- **sqlite**: embedded via file volume.
- **apprise CLI (inside api runtime image)**: direct URI notifications (`gotify://`, `discord://`, `mailto://`, etc.) without a separate relay service.

## Data flow
1. Admin logs in with username/password + TOTP.
2. JWT is issued for API calls.
3. Host credentials are encrypted before DB write.
4. Scan/apply request decrypts host secrets in memory only.
5. SSH command executes remotely and output is parsed + persisted.
6. Notification events execute via bundled Apprise CLI to the configured destination URL.

## Command model (Debian/Ubuntu)
- Scan:
  - `apt-get update`
  - `apt list --upgradable`
  - `/var/run/reboot-required` check
  - `needrestart -b` if installed
- Apply:
  - `DEBIAN_FRONTEND=noninteractive apt-get -y dist-upgrade`
- Remediation:
  - `systemctl restart <services...>`
  - `reboot` / `shutdown`

## Planned milestones
- M1: scaffold + auth + host CRUD + scan/apply endpoints
- M2: full UI forms and tables, needrestart parser hardening
- M3: scheduler cron parser + run history + audit log
- M4: RBAC/open-source polish, testing, known_hosts/pinning
