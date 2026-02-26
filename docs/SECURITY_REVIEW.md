# Patchdeck Security Review (v1 preflight)

## Threat model highlights
- Secrets at rest theft (DB/file exfil)
- Credential misuse via API compromise
- MITM risk on SSH transport
- Privilege escalation through unchecked command execution
- Session theft/JWT leakage

## Controls in current scaffold
- AES-GCM encryption for host secret blobs
- Password hashing via bcrypt
- TOTP enforcement on login
- JWT-based API auth
- Minimal container images
- No plaintext secrets in response payloads
- Mandatory SSH host key verification in alpha (default-on)
- TOFU first-trust + optional manual pinning for host fingerprints
- Hard-block on host key mismatch with explicit accept/deny workflow
- Host key decision/audit trail surfaced for operator review

## Gaps to close before production
1. **Rate limiting + lockout**
   - Brute-force controls on `/api/login`.
2. **Secret management hardening**
   - Support Docker secrets and key rotation workflow.
3. **CSRF/session posture**
   - If cookie auth is introduced, add CSRF protection and secure cookie flags.
4. **Input restrictions**
   - Strong validation for hostnames, ports, cron expressions, service names.
5. **Least privilege guidance**
   - Document sudoers profile for patch-only operations.
6. **Deep test coverage**
   - Add integration tests for auth, encryption, scheduler, and SSH host-key mismatch workflows.

## Sudo profile recommendation (example)
Use a dedicated user with constrained sudoers rules for apt/systemctl/reboot only.

## 2FA recovery
- v1 should provide one-time recovery codes encrypted at rest.

## Security release gate
Before `v1.0.0`, require:
- audit log implementation (including host-key decision history)
- integration tests for auth + encryption + SSH execution paths
- brute-force protections on auth endpoints
- external review checklist completion
