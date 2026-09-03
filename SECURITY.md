# Security Policy

Patchdeck connects to your servers over SSH and stores their credentials, so its security matters. Thank you for helping keep it — and the people running it — safe.

## Reporting a vulnerability

**Please don't open a public issue for security vulnerabilities.**

Report privately through GitHub's [**Report a vulnerability**](https://github.com/roydufek/patchdeck/security/advisories/new) form (the repo's **Security** tab → **Advisories**). I'll acknowledge the report as quickly as I can, work with you on a fix, and — if you'd like — credit you in the release notes.

Helpful things to include:

- A description of the issue and its impact
- Steps to reproduce (a proof of concept if you have one)
- The affected version(s)

## Supported versions

Patchdeck ships as a rolling release — security fixes land on `latest` (`ghcr.io/roydufek/patchdeck:latest`). Please run a recent version before reporting.

| Version | Supported |
|---------|-----------|
| 2.5.x   | ✅ |
| < 2.5   | Please upgrade |

## Design notes (where to look)

Patchdeck aims to be safe by default:

- **Credentials are encrypted at rest** — AES-256-GCM, with the key derived from `PATCHDECK_MASTER_KEY` via HKDF-SHA256; secrets are never returned by the API.
- **Agentless** — nothing is installed on managed hosts; Patchdeck only holds an outbound SSH client.
- **No cloud, no telemetry** — your fleet's details never leave the container.
- **SSH host keys are approved on first connection**, and a changed key pauses operations until you re-approve (fail-closed).

Findings in those areas — credential handling, the auth/session layer, SSH host-key verification, or command injection into the remote shell — are especially valuable.
