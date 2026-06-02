import { formatTimestamp, staleLabel } from './format.js'

export function isConnectionFailureMessage(message) {
  if (!message || typeof message !== 'string') return false
  const value = message.toLowerCase()
  return [
    'host key mismatch', 'fingerprint', 'possible mitm', 'ssh',
    'timed out', 'timeout', 'connection refused', 'unable to connect',
    'network', 'auth', 'permission denied', 'handshake',
    'dial tcp', 'no route to host'
  ].some(token => value.includes(token))
}

export function connectionHealth(host, snap, connectionError) {
  if (host.host_key_pending_fingerprint) {
    return { ok: false, label: 'Host key mismatch', detail: 'SSH key changed and needs operator review.' }
  }
  if (connectionError) {
    return { ok: false, label: 'Connection issue', detail: connectionError }
  }
  if (host.checks_enabled === false) {
    return { ok: false, label: 'Checks disabled', detail: 'Enable host checks to verify connectivity.' }
  }
  if (!snap?.updated_at) {
    return { ok: false, label: 'No scan yet', detail: 'Run a scan to establish baseline host health.' }
  }
  const freshness = staleLabel(snap.updated_at)
  if (freshness === 'stale') {
    return { ok: false, label: 'Stale status', detail: 'Last successful scan is older than 24 hours.' }
  }
  return { ok: true, label: 'Healthy', detail: 'Recent scan completed successfully.' }
}

export function isTimeoutMessage(message) {
  if (!message || typeof message !== 'string') return false
  const v = message.toLowerCase()
  return v.includes('timed out') || v.includes('timeout') || v.includes('i/o timeout')
}

// classifyConnError maps a raw connectivity error into a short, human cause shown as
// subtext under a red dot, so the operator knows WHICH problem it is.
export function classifyConnError(detail) {
  const v = (detail || '').toLowerCase()
  if (v.includes('host key') || v.includes('fingerprint') || v.includes('mitm')) return 'SSH host key mismatch'
  if (v.includes('authentication failed') || v.includes('permission denied') ||
      v.includes('no supported methods') || v.includes('unable to authenticate') ||
      v.includes('handshake')) return 'SSH key / auth rejected'
  if (isTimeoutMessage(detail)) return 'Connection timed out'
  if (v.includes('refused') || v.includes('no route') || v.includes('unreachable') ||
      v.includes('not found') || v.includes('lookup') || v.includes('reset')) return 'Host unreachable'
  return 'Connection failed'
}

// connectionIndicator drives the per-host status dot. Three states only:
//   pending — a live SSH check is in flight / not yet run (dot pulses yellow)
//   good    — the SSH check connected (solid green)
//   bad     — the SSH check failed: unreachable, timed out, or auth/key rejected
//             (solid red, with `reason` as subtext). Connectivity is its own signal
//             and is NOT inferred from scan freshness.
export function connectionIndicator(host, connectivity, fallbackError, snap) {
  if (host.host_key_pending_fingerprint) {
    return { ok: false, tone: 'bad', label: 'Disconnected', reason: 'SSH host key mismatch', detail: 'SSH host key changed and needs operator review — operations are blocked until you Accept or Deny the new fingerprint.' }
  }
  if (connectivity && connectivity.connected === true) {
    return {
      ok: true, tone: 'good', label: 'Connected', reason: '',
      detail: connectivity.checked_at ? `SSH check passed · ${formatTimestamp(connectivity.checked_at)}` : 'SSH connectivity check passed.',
    }
  }
  const hasResult = !!(connectivity && (connectivity.checked_at || connectivity.error || connectivity.connected === false))
  if (!hasResult && !fallbackError) {
    // No result yet (initial load or a check currently running) — neutral pending.
    return { ok: null, tone: 'pending', label: 'Checking…', reason: '', detail: 'SSH connectivity check in progress…' }
  }
  const detail = (connectivity && connectivity.error) || fallbackError || 'SSH connectivity check failed.'
  return { ok: false, tone: 'bad', label: 'Disconnected', reason: classifyConnError(detail), detail }
}

export function hostKeyHealth(host) {
  const mode = host.host_key_trust_mode || 'tofu'
  const trusted = (host.host_key_trusted_fingerprint || host.host_key_pinned_fingerprint || '').trim()
  if (host.host_key_pending_fingerprint) {
    return {
      tone: 'bad',
      label: 'Host key review required',
      detail: `Presented fingerprint differs from trusted ${mode} key. Host operations are blocked until reviewed.`
    }
  }
  if (mode === 'pinned' && !trusted) {
    return {
      tone: 'warn',
      label: 'Pinned key missing',
      detail: 'Pinned mode is selected but no fingerprint is set yet.'
    }
  }
  return {
    tone: 'good',
    label: mode === 'pinned' ? 'Pinned key verified' : 'TOFU key trusted',
    detail: mode === 'pinned'
      ? 'Using pinned SSH fingerprint verification for this host.'
      : 'Using TOFU SSH fingerprint verification for this host.'
  }
}

export function operationTone(mode, ok) {
  if (mode === 'scan') return ok ? 'good' : 'bad'
  if (mode === 'apply') return ok ? 'good' : 'bad'
  return 'warn'
}

export const HOST_ACTION_STATE_STORAGE_KEY = 'patchdeck.hostActionState.v3'

export function loadPersistedHostActionState() {
  if (typeof window === 'undefined') return {}

  const parseSnapshot = (state) => {
    if (!state || typeof state !== 'object') return null
    const at = typeof state.at === 'string' ? state.at : ''
    return {
      ok: !!state.ok,
      summary: typeof state.summary === 'string' ? state.summary : '',
      at,
      time: at ? Date.parse(at) : NaN
    }
  }

  const normalizeState = (parsed) => {
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    const cleaned = {}

    Object.entries(parsed).forEach(([hostId, state]) => {
      if (!state || typeof state !== 'object') return

      const scan = parseSnapshot(state.scan)
      const apply = parseSnapshot(state.apply)

      if (scan || apply) {
        const latestCandidates = [
          scan ? { mode: 'scan', ...scan } : null,
          apply ? { mode: 'apply', ...apply } : null
        ].filter(Boolean)

        const latestFromPayload = state.latest && (state.latest.mode === 'scan' || state.latest.mode === 'apply')
          ? parseSnapshot(state.latest)
          : null

        if (latestFromPayload) {
          latestCandidates.push({ mode: state.latest.mode, ...latestFromPayload })
        }

        const latest = latestCandidates.sort((a, b) => {
          const at = Number.isFinite(a.time) ? a.time : -1
          const bt = Number.isFinite(b.time) ? b.time : -1
          return bt - at
        })[0] || null

        cleaned[hostId] = {
          ...(scan ? { scan: { ok: scan.ok, summary: scan.summary, at: scan.at } } : {}),
          ...(apply ? { apply: { ok: apply.ok, summary: apply.summary, at: apply.at } } : {}),
          ...(latest ? { latest: { mode: latest.mode, ok: latest.ok, summary: latest.summary, at: latest.at } } : {})
        }
        return
      }

      if ((state.mode === 'scan' || state.mode === 'apply') && typeof state.summary === 'string') {
        const snapshot = {
          mode: state.mode,
          ok: !!state.ok,
          summary: state.summary,
          at: typeof state.at === 'string' ? state.at : ''
        }
        cleaned[hostId] = {
          [state.mode]: { ok: snapshot.ok, summary: snapshot.summary, at: snapshot.at },
          latest: snapshot
        }
      }
    })

    return cleaned
  }

  try {
    const keys = [HOST_ACTION_STATE_STORAGE_KEY, 'patchdeck.hostActionState.v2']
    for (const key of keys) {
      const raw = localStorage.getItem(key)
      if (!raw) continue
      const parsed = JSON.parse(raw)
      const normalized = normalizeState(parsed)
      if (Object.keys(normalized).length > 0) return normalized
    }
    return {}
  } catch {
    return {}
  }
}
