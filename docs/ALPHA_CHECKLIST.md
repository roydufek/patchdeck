# Patchdeck Alpha Readiness Checklist

Last updated: 2026-02-18 (late)

## Scope for tonight
Priority order from the alpha hardening track:

1. SSH host key verification UX + enforcement
2. Docker runtime validation of latest stack
3. UX/error-message polish (security + notifications)
4. README/docs checklist + known limitations

## Status summary

### 1) SSH host key verification UX + enforcement
- **Status:** ✅ DONE
- **Implemented:**
  - Default-on SSH host key verification in alpha.
  - TOFU trust-on-first-use flow.
  - Optional manual fingerprint pinning.
  - Hard-block behavior on host key mismatch.
  - Explicit accept/deny mismatch workflow.
  - Host key audit trail surfaced via API + UI.

### 2) Docker runtime validation of latest stack
- **Status:** ✅ DONE
- **Implemented:**
  - `deploy/validate-alpha-runtime.sh` smoke validator covering:
    - API health/startup path
    - Setup wizard status endpoint path
    - Host operations controls path
    - Scheduler create/list path
    - Notification test endpoint path

### 3) UX/error-message polish (security + notifications)
- **Status:** 🟡 IN PROGRESS (alpha polish pass)
- **Implemented so far:**
  - Clearer security-flow messaging around host key enforcement and mismatch decisions.
  - Improved notification test-path messages for runtime unavailable vs delivery failure.
  - More explicit operator guidance in error text for likely operator actions.
  - Per-host action-state persistence fix: local cache now preserves scan/apply snapshots independently across reloads (v2→v3 migration), so each host card keeps clear last-scan + last-apply context after refresh.
- Host cards now use an at-a-glance status rail (connection, last scan, last apply, latest result) so operators can triage each host without jumping to global/top-level widgets.

### 4) README/docs checklist + known limitations
- **Status:** ✅ DONE
- **Implemented:**
  - Alpha runtime validation instructions in README.
  - This alpha checklist document with implemented scope and gaps.
  - Security review updated to reflect shipped host-key controls and remaining production gates.

## Known alpha limitations

- Notification path is validated for reachability and transport outcomes, but delivery semantics remain provider-specific.
- Host key mismatch workflow is strict by design; emergency break-glass bypass is intentionally not available in alpha.
- API and UI test coverage is still below production expectations.
- SQLite is suitable for alpha/single-node use, not clustered HA production.
- Operational observability remains lightweight (intentional) and may need expansion for larger deployments.

## Remaining before production

- Expand integration tests around auth, scheduler, host-key edge cases, and notification outcomes.
- Add login rate-limiting/lockout controls and broader abuse defenses.
- Define key-rotation playbooks for encrypted host secrets.
- Document least-privilege SSH + sudo profiles with concrete hardened examples.
- Complete external security review + penetration checklist.
