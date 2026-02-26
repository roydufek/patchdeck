import React, { useState } from 'react'
import { hostKeyHealth } from '../utils/status.js'
import { formatTimestamp, formatFingerprintShort, truncateText } from '../utils/format.js'
import { timeAgo, fullDate } from '../utils/timeago.js'
import { trustModeLabel } from '../utils/labels.js'
import ConfirmDialog from './ConfirmDialog.jsx'
import { useToastContext } from './Toast.jsx'

/** Sortable package table for upgradable packages */
function PackageTable({ packages }) {
  const [sortKey, setSortKey] = useState('name')
  const [sortAsc, setSortAsc] = useState(true)

  function handleSort(key) {
    if (sortKey === key) {
      setSortAsc(v => !v)
    } else {
      setSortKey(key)
      setSortAsc(true)
    }
  }

  const sorted = [...packages].sort((a, b) => {
    const aVal = typeof a === 'string' ? a : (a[sortKey] || a.name || '')
    const bVal = typeof b === 'string' ? b : (b[sortKey] || b.name || '')
    const cmp = aVal.localeCompare(bVal)
    return sortAsc ? cmp : -cmp
  })

  const arrow = sortAsc ? ' ↑' : ' ↓'

  const cols = [
    { key: 'name', label: 'Package' },
    { key: 'current_version', label: 'Current' },
    { key: 'new_version', label: 'New' },
    { key: 'source', label: 'Source' },
  ]

  return (
    <details className="text-xs">
      <summary className="cursor-pointer text-gray-500 dark:text-zinc-500 hover:text-gray-700 dark:hover:text-zinc-300 transition-colors">Show packages</summary>
      <div className="mt-2 rounded-lg border border-gray-200 dark:border-zinc-800 overflow-hidden">
        <table className="w-full text-[11px]">
          <thead>
            <tr className="bg-gray-50 dark:bg-zinc-900/80 border-b border-gray-200 dark:border-zinc-800">
              {cols.map(col => (
                <th
                  key={col.key}
                  onClick={() => handleSort(col.key)}
                  className={`text-left px-2.5 py-1.5 font-medium text-gray-600 dark:text-zinc-400 cursor-pointer select-none hover:text-gray-800 dark:hover:text-zinc-200 transition-colors ${col.key !== 'name' ? 'font-mono' : ''}`}
                >
                  {col.label}{sortKey === col.key ? arrow : ''}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {sorted.map((pkg, i) => {
              const name = typeof pkg === 'string' ? pkg : (pkg.name || '')
              const cur = typeof pkg === 'object' ? (pkg.current_version || '') : ''
              const nv = typeof pkg === 'object' ? (pkg.new_version || '') : ''
              const src = typeof pkg === 'object' ? (pkg.source || '') : ''
              return (
                <tr key={name + i} className="border-b border-gray-100 dark:border-zinc-800/50 last:border-0 hover:bg-gray-50 dark:hover:bg-zinc-800/40 transition-colors">
                  <td className="px-2.5 py-1.5 text-gray-700 dark:text-zinc-300 font-medium truncate max-w-[200px]">{name}</td>
                  <td className="px-2.5 py-1.5 text-gray-500 dark:text-zinc-400 font-mono">{cur || '—'}</td>
                  <td className="px-2.5 py-1.5 text-emerald-400/90 font-mono">{nv || '—'}</td>
                  <td className="px-2.5 py-1.5 text-gray-500 dark:text-zinc-500 font-mono">{src || '—'}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </details>
  )
}

/** Human-readable audit event labels and icons */
function auditEventLabel(event) {
  switch (event) {
    case 'host_key_first_trust': return { label: 'First trust (TOFU)', icon: '🔑', tone: 'text-emerald-400' }
    case 'host_key_mismatch_blocked': return { label: 'Mismatch blocked', icon: '🚫', tone: 'text-red-400' }
    case 'host_key_rotation_accepted': return { label: 'Key rotation accepted', icon: '✅', tone: 'text-emerald-400' }
    case 'host_key_mismatch_denied': return { label: 'Key rotation denied', icon: '❌', tone: 'text-red-400' }
    case 'host_key_policy_pinned': return { label: 'Policy updated (Pinned)', icon: '📌', tone: 'text-blue-400' }
    default: return { label: event, icon: '📋', tone: 'text-gray-600 dark:text-zinc-400' }
  }
}

/** Human-readable audit note — cleans up internal strings */
function auditNoteLabel(note) {
  switch (note) {
    case 'tofu first successful trust': return 'Host key was automatically trusted on first connection (TOFU).'
    case 'pinned mode configured without fingerprint': return 'Pinned mode is set but no fingerprint was provided yet.'
    case 'presented fingerprint does not match pinned fingerprint': return 'The host presented a different key than the pinned fingerprint.'
    case 'tofu mismatch blocked pending operator decision': return 'The host key changed since it was first trusted. Awaiting operator review.'
    case 'operator updated host key policy': return 'An operator updated the host key pinning policy.'
    default: return note || ''
  }
}

/** Copyable fingerprint component */
function CopyableFingerprint({ label, value }) {
  const toast = useToastContext()
  if (!value) return null

  async function handleCopy(e) {
    e.stopPropagation()
    try {
      await navigator.clipboard.writeText(value)
      toast.addToast({ type: 'success', message: 'Fingerprint copied to clipboard.' })
    } catch {
      toast.addToast({ type: 'error', message: 'Failed to copy.' })
    }
  }

  return (
    <p className="text-gray-500 dark:text-zinc-500 flex items-center gap-1.5 flex-wrap">
      {label}:{' '}
      <code className="font-mono text-gray-700 dark:text-zinc-300 bg-gray-100 dark:bg-zinc-900/60 rounded px-1.5 py-0.5 text-[11px] select-all">{value}</code>
      <button
        onClick={handleCopy}
        className="text-gray-400 dark:text-zinc-600 hover:text-gray-600 dark:hover:text-zinc-300 transition-colors"
        title="Copy fingerprint"
      >
        📋
      </button>
    </p>
  )
}

export default function HostDetails({
  host: h, scan: snap, connectivity, connection, actionState, actionError,
  actionBusy, onUpdateHostOps, onUpdateHostKeyPolicy,
  onResolveHostKeyMismatch, onUpdateHostNotificationPrefs,
  onLoadHostKeyAudit, hostKeyAudit,
  onEdit, onDelete,
  onRestartServices, onReboot, onShutdown
}) {
  const [auditOpen, setAuditOpen] = useState(false)
  const [rawOutputOpen, setRawOutputOpen] = useState(false)
  const [selectedServices, setSelectedServices] = useState([])
  const [restartConfirm, setRestartConfirm] = useState(null) // 'reboot' | 'restart-services' | null

  const checksEnabled = h.checks_enabled !== false
  const hostKeyStatus = hostKeyHealth(h)
  const hostKeyMode = h.host_key_trust_mode || 'tofu'
  const trustedFingerprint = (h.host_key_trusted_fingerprint || h.host_key_pinned_fingerprint || '').trim()
  const pendingFingerprint = (h.host_key_pending_fingerprint || '').trim()

  const scanAction = actionState.scan || null
  const applyAction = actionState.apply || null
  const scanBusy = !!actionBusy[`${h.id}:scan`]
  const applyBusy = !!actionBusy[`${h.id}:apply`]

  const packageCount = Array.isArray(snap?.packages) ? snap.packages.length : 0
  const restartServicesCount = Array.isArray(snap?.needs_restart) ? snap.needs_restart.length : 0
  const restartServices = Array.isArray(snap?.needs_restart) ? snap.needs_restart : []
  const restartNeeded = !!(snap?.needs_reboot || restartServicesCount > 0)

  const rebootBusy = !!actionBusy[`${h.id}:reboot`]
  const restartServicesBusy = !!actionBusy[`${h.id}:restart-services`]

  const scanFailureNotificationsEnabled = h.notification_prefs?.scan_failure !== false

  // Raw output detection
  const hasRawOutput = !!(snap?.raw_output)

  return (
    <div className="border-t border-gray-200 dark:border-zinc-800/60 px-5 py-4 space-y-5" onClick={e => e.stopPropagation()}>
      {/* Connection & scan overview */}
      <div className="grid sm:grid-cols-3 gap-3 text-xs">
        <div className="rounded-lg border border-gray-200 dark:border-zinc-800 bg-gray-50 dark:bg-zinc-900/60 px-3 py-2.5">
          <p className="text-gray-500 dark:text-zinc-500 font-medium uppercase tracking-wider text-[10px] mb-1">Connection</p>
          <p className={`font-medium ${connection.ok ? 'text-emerald-500 dark:text-emerald-400' : 'text-red-500 dark:text-red-400'}`}>{connection.label}</p>
          <p className="text-gray-500 dark:text-zinc-500 mt-0.5 leading-snug">{connection.detail}</p>
          {connectivity?.checked_at && (
            <p className="text-gray-400 dark:text-zinc-600 mt-0.5" title={fullDate(connectivity.checked_at)}>{timeAgo(connectivity.checked_at)}</p>
          )}
        </div>
        <div className="rounded-lg border border-gray-200 dark:border-zinc-800 bg-gray-50 dark:bg-zinc-900/60 px-3 py-2.5">
          <p className="text-gray-500 dark:text-zinc-500 font-medium uppercase tracking-wider text-[10px] mb-1">Last scan</p>
          <p className="font-medium">
            {scanBusy ? 'Running…' : scanAction ? (scanAction.ok ? 'Succeeded' : 'Failed') : snap ? 'Data available' : 'Not yet run'}
          </p>
          <p className="text-gray-500 dark:text-zinc-500 mt-0.5" title={fullDate(scanAction?.at || snap?.updated_at || '')}>
            {scanAction?.at ? timeAgo(scanAction.at) : snap?.updated_at ? timeAgo(snap.updated_at) : '—'}
          </p>
          {scanAction?.summary && <p className="text-gray-500 dark:text-zinc-500 mt-0.5">{truncateText(scanAction.summary, 80)}</p>}
        </div>
        <div className="rounded-lg border border-gray-200 dark:border-zinc-800 bg-gray-50 dark:bg-zinc-900/60 px-3 py-2.5">
          <p className="text-gray-500 dark:text-zinc-500 font-medium uppercase tracking-wider text-[10px] mb-1">Last apply</p>
          <p className="font-medium">
            {applyBusy ? 'Running…' : applyAction ? (applyAction.ok ? 'Succeeded' : 'Failed') : 'Not yet run'}
          </p>
          <p className="text-gray-500 dark:text-zinc-500 mt-0.5" title={fullDate(applyAction?.at || '')}>
            {applyAction?.at ? timeAgo(applyAction.at) : '—'}
          </p>
          {applyAction?.summary && <p className="text-gray-500 dark:text-zinc-500 mt-0.5">{truncateText(applyAction.summary, 80)}</p>}
        </div>
      </div>

      {/* No scan data empty state */}
      {!snap && (
        <div className="rounded-xl border border-dashed border-gray-300 dark:border-zinc-700 p-6 text-center">
          <p className="text-2xl mb-2">🔍</p>
          <p className="text-sm text-gray-600 dark:text-zinc-400">No scan data</p>
          <p className="text-xs text-gray-400 dark:text-zinc-600 mt-1">Run a scan to check for updates</p>
        </div>
      )}

      {/* Packages */}
      {snap && (
        <div>
          <p className="text-xs font-medium text-gray-600 dark:text-zinc-400 mb-2">
            {packageCount > 0 ? `${packageCount} upgradable package${packageCount !== 1 ? 's' : ''}` : 'No upgradable packages'}
            {snap?.needs_reboot && <span className="text-amber-500 dark:text-amber-400 ml-2">· Reboot required</span>}
            {!snap?.needs_reboot && restartNeeded && <span className="text-amber-500 dark:text-amber-400 ml-2">· {restartServicesCount} service restart{restartServicesCount !== 1 ? 's' : ''} needed</span>}
          </p>
          {snap?.packages?.length > 0 && (
            <PackageTable packages={snap.packages} />
          )}
          {snap && !snap.packages?.length && (
            <p className="text-xs text-emerald-500/70">✓ All packages up to date</p>
          )}
        </div>
      )}

      {/* System info */}
      {snap && (snap.os_name || snap.uptime || snap.kernel) && (
        <div className="rounded-lg border border-gray-200 dark:border-zinc-800 bg-gray-50 dark:bg-zinc-900/60 px-4 py-3 text-xs space-y-1">
          <p className="text-gray-500 dark:text-zinc-500 font-medium uppercase tracking-wider text-[10px]">System info</p>
          <div className="grid grid-cols-2 gap-x-6 gap-y-1 text-gray-600 dark:text-zinc-400">
            {snap.os_name && (
              <div><span className="text-gray-400 dark:text-zinc-600">OS:</span> {snap.os_name}</div>
            )}
            {snap.kernel && (
              <div><span className="text-gray-400 dark:text-zinc-600">Kernel:</span> <span className="font-mono text-[11px]">{snap.kernel}</span></div>
            )}
            {snap.uptime && (
              <div><span className="text-gray-400 dark:text-zinc-600">Uptime:</span> {snap.uptime}</div>
            )}
          </div>
        </div>
      )}

      {/* Restart posture */}
      {snap && (
        <div className="text-xs text-gray-500 dark:text-zinc-500 space-y-3">
          <p>
            <span className="font-medium text-gray-600 dark:text-zinc-400">Restart posture:</span>{' '}
            {snap.needs_reboot
              ? (snap.reboot_reason
                ? `Reboot required — triggered by: ${snap.reboot_reason}`
                : 'Reboot required (/var/run/reboot-required present).')
              : restartServicesCount > 0
                ? `${restartServicesCount} service${restartServicesCount !== 1 ? 's' : ''} flagged by needrestart.`
                : snap.needrestart_found
                  ? 'No reboot or service restarts needed.'
                  : 'needrestart is not installed — service restart detection is unavailable.'}
          </p>

          {/* Reboot required */}
          {snap.needs_reboot && onReboot && (
            <div className="rounded-lg border border-red-300 dark:border-red-800/50 bg-red-50 dark:bg-red-950/20 px-4 py-3">
              <p className="text-red-600 dark:text-red-300 text-xs font-medium mb-1">System reboot required</p>
              {snap.reboot_reason && (
                <p className="text-red-500 dark:text-red-400/80 text-xs mb-2">Triggered by: {snap.reboot_reason}</p>
              )}
              <button
                onClick={() => setRestartConfirm('reboot')}
                disabled={rebootBusy}
                className="rounded-lg px-3 py-1.5 text-xs bg-red-600 text-white hover:bg-red-500 disabled:opacity-40 transition-colors"
              >
                {rebootBusy ? 'Rebooting…' : 'Reboot'}
              </button>
            </div>
          )}

          {/* Service restarts */}
          {restartServices.length > 0 && onRestartServices && (
            <div className="rounded-lg border border-gray-200 dark:border-zinc-700/60 bg-gray-50 dark:bg-zinc-900/60 px-4 py-3 space-y-2">
              <div className="flex items-center justify-between">
                <p className="text-gray-700 dark:text-zinc-300 text-xs font-medium">Services needing restart</p>
                <label className="flex items-center gap-1.5 text-[11px] text-gray-500 dark:text-zinc-500 cursor-pointer select-none">
                  <input
                    type="checkbox"
                    checked={selectedServices.length === restartServices.length && restartServices.length > 0}
                    onChange={e => {
                      setSelectedServices(e.target.checked ? [...restartServices] : [])
                    }}
                    className="h-3 w-3 rounded border-gray-300 dark:border-zinc-700 bg-gray-100 dark:bg-zinc-900 text-emerald-500 focus:ring-0 focus:ring-offset-0 accent-emerald-500"
                  />
                  Select all
                </label>
              </div>
              <div className="grid sm:grid-cols-2 gap-1">
                {restartServices.map(svc => {
                  const checked = selectedServices.includes(svc)
                  return (
                    <label key={svc} className="flex items-center gap-2 rounded px-2 py-1 hover:bg-gray-100 dark:hover:bg-zinc-800/50 cursor-pointer select-none transition-colors">
                      <input
                        type="checkbox"
                        checked={checked}
                        onChange={e => {
                          setSelectedServices(prev =>
                            e.target.checked ? [...prev, svc] : prev.filter(s => s !== svc)
                          )
                        }}
                        className="h-3 w-3 rounded border-gray-300 dark:border-zinc-700 bg-gray-100 dark:bg-zinc-900 text-emerald-500 focus:ring-0 focus:ring-offset-0 accent-emerald-500"
                      />
                      <span className="font-mono text-[11px] text-gray-700 dark:text-zinc-300 truncate">{svc}</span>
                    </label>
                  )
                })}
              </div>
              <button
                onClick={() => setRestartConfirm('restart-services')}
                disabled={selectedServices.length === 0 || restartServicesBusy}
                className="rounded-lg px-3 py-1.5 text-xs bg-emerald-600 text-white hover:bg-emerald-500 disabled:opacity-40 transition-colors"
              >
                {restartServicesBusy
                  ? 'Restarting…'
                  : selectedServices.length > 0
                    ? `Restart ${selectedServices.length} service${selectedServices.length !== 1 ? 's' : ''}`
                    : 'Restart selected'}
              </button>
            </div>
          )}

          {/* Confirm dialogs for restart/reboot */}
          <ConfirmDialog
            open={restartConfirm === 'reboot'}
            title="Reboot host"
            message={`Reboot ${h.name}? The host will be temporarily unavailable.`}
            confirmLabel="Reboot"
            confirmColor="red"
            busy={rebootBusy}
            onConfirm={() => {
              setRestartConfirm(null)
              onReboot(h.id)
            }}
            onCancel={() => setRestartConfirm(null)}
          />
          <ConfirmDialog
            open={restartConfirm === 'restart-services'}
            title="Restart services"
            message={`Restart ${selectedServices.length} service${selectedServices.length !== 1 ? 's' : ''} on ${h.name}?`}
            confirmLabel={`Restart ${selectedServices.length} service${selectedServices.length !== 1 ? 's' : ''}`}
            confirmColor="blue"
            busy={restartServicesBusy}
            onConfirm={() => {
              setRestartConfirm(null)
              onRestartServices(h.id, selectedServices)
            }}
            onCancel={() => setRestartConfirm(null)}
          />
        </div>
      )}

      {/* Raw output viewer */}
      {hasRawOutput && (
        <div>
          <button
            onClick={() => setRawOutputOpen(v => !v)}
            className="text-xs text-gray-500 dark:text-zinc-500 hover:text-gray-700 dark:hover:text-zinc-300 transition-colors"
          >
            {rawOutputOpen ? '▾ Hide' : '▸ Show'} raw output
          </button>
          {rawOutputOpen && (
            <div className="mt-2 rounded-lg border border-gray-200 dark:border-zinc-800 bg-gray-50 dark:bg-zinc-950 overflow-hidden">
              <pre className="px-3 py-2 text-[11px] font-mono text-gray-600 dark:text-zinc-400 max-h-64 overflow-auto whitespace-pre-wrap break-all leading-relaxed scrollbar-thin">
                {snap.raw_output}
              </pre>
            </div>
          )}
        </div>
      )}

      {/* SSH host key */}
      <div className="rounded-lg border border-gray-200 dark:border-zinc-800 bg-gray-50 dark:bg-zinc-900/60 px-4 py-3 text-xs space-y-1.5">
        <p className="text-gray-500 dark:text-zinc-500 font-medium uppercase tracking-wider text-[10px]">SSH host key</p>
        <p className={`font-medium ${hostKeyStatus.tone === 'good' ? 'text-emerald-500 dark:text-emerald-400' : hostKeyStatus.tone === 'warn' ? 'text-amber-500 dark:text-amber-400' : 'text-red-500 dark:text-red-400'}`}>
          {hostKeyStatus.label}
        </p>
        <p className="text-gray-500 dark:text-zinc-500">{hostKeyStatus.detail}</p>
        <div className="text-gray-400 dark:text-zinc-600 space-y-1">
          <p>Mode: <span className="text-gray-600 dark:text-zinc-400">{trustModeLabel(hostKeyMode)}</span></p>
          <CopyableFingerprint label="Trusted" value={trustedFingerprint || null} />
          {!trustedFingerprint && <p className="text-gray-400 dark:text-zinc-600">Trusted: <span className="text-gray-500 dark:text-zinc-500 italic">Not yet established</span></p>}
          <CopyableFingerprint label="Presented" value={pendingFingerprint || null} />
        </div>
      </div>

      {/* Operational settings */}
      <div className="space-y-3">
        <p className="text-xs font-medium text-gray-600 dark:text-zinc-400">Operational settings</p>
        <div className="grid sm:grid-cols-2 gap-x-6 gap-y-2 text-xs text-gray-600 dark:text-zinc-400">
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={checksEnabled}
              onChange={e => onUpdateHostOps(h, { checks_enabled: e.target.checked })}
              className="rounded border-gray-300 dark:border-zinc-600"
            />
            Host checks enabled
          </label>
          <label className="flex items-center gap-2">
            Host key mode
            <select
              className="rounded border border-gray-300 dark:border-zinc-700 bg-gray-100 dark:bg-zinc-800/50 px-2 py-1 text-xs text-gray-700 dark:text-zinc-300 focus:outline-none"
              value={hostKeyMode}
              onChange={e => onUpdateHostKeyPolicy(h, { host_key_trust_mode: e.target.value })}
            >
              <option value="tofu">TOFU</option>
              <option value="pinned">Pinned</option>
            </select>
          </label>
          <input
            className="rounded border border-gray-300 dark:border-zinc-700 bg-gray-100 dark:bg-zinc-800/50 px-2 py-1 text-xs text-gray-700 dark:text-zinc-300 placeholder-gray-400 dark:placeholder-zinc-600 focus:outline-none"
            placeholder="Pinned fingerprint"
            value={h.host_key_pinned_fingerprint || ''}
            onChange={e => onUpdateHostKeyPolicy(h, { host_key_pinned_fingerprint: e.target.value })}
          />
        </div>
        <p className="text-[11px] text-gray-400 dark:text-zinc-600">SSH host key verification is enforced and cannot be disabled during alpha.</p>
      </div>

      {/* Notification prefs */}
      <div className="space-y-3">
        <p className="text-xs font-medium text-gray-600 dark:text-zinc-400">Notification preferences</p>
        {!scanFailureNotificationsEnabled && (
          <div className="rounded-lg border border-amber-300 dark:border-amber-800/50 bg-amber-50 dark:bg-amber-950/20 px-3 py-2 text-xs text-amber-600 dark:text-amber-300">
            <p>Security alerts are muted for this host. Host key mismatch alerts won't be sent.</p>
            <button
              onClick={() => onUpdateHostNotificationPrefs(h, { scan_failure: true })}
              className="mt-1.5 rounded-md border border-amber-400 dark:border-amber-700/60 px-2.5 py-1 text-[11px] hover:bg-amber-100 dark:hover:bg-amber-900/30 transition-colors"
            >
              Re-enable security alerts
            </button>
          </div>
        )}
        <div className="grid sm:grid-cols-2 gap-x-6 gap-y-2 text-xs text-gray-600 dark:text-zinc-400">
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={h.notification_prefs?.updates_available !== false} onChange={e => onUpdateHostNotificationPrefs(h, { updates_available: e.target.checked })} className="rounded border-gray-300 dark:border-zinc-600" />
            Updates available
          </label>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={h.notification_prefs?.auto_apply_success !== false} onChange={e => onUpdateHostNotificationPrefs(h, { auto_apply_success: e.target.checked })} className="rounded border-gray-300 dark:border-zinc-600" />
            Auto-apply success
          </label>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={h.notification_prefs?.auto_apply_failure !== false} onChange={e => onUpdateHostNotificationPrefs(h, { auto_apply_failure: e.target.checked })} className="rounded border-gray-300 dark:border-zinc-600" />
            Auto-apply failure
          </label>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={h.notification_prefs?.scan_failure !== false} onChange={e => onUpdateHostNotificationPrefs(h, { scan_failure: e.target.checked })} className="rounded border-gray-300 dark:border-zinc-600" />
            Scan/connect failure
          </label>
        </div>
        <p className="text-[11px] text-gray-400 dark:text-zinc-600">Host key mismatch and fingerprint rejection alerts are routed through the scan/connect failure channel.</p>
      </div>

      {/* Audit trail */}
      <div>
        <button
          onClick={async () => {
            const open = !auditOpen
            setAuditOpen(open)
            if (open) await onLoadHostKeyAudit(h.id)
          }}
          className="text-xs text-gray-500 dark:text-zinc-500 hover:text-gray-700 dark:hover:text-zinc-300 transition-colors"
        >
          {auditOpen ? '▾ Hide' : '▸ Show'} host key audit trail
        </button>
        {auditOpen && (
          <div className="mt-2 space-y-1.5">
            {(!hostKeyAudit || hostKeyAudit.length === 0) ? (
              <p className="text-xs text-gray-400 dark:text-zinc-600">No audit events yet.</p>
            ) : (
              hostKeyAudit.map(ev => {
                const evInfo = auditEventLabel(ev.event)
                const noteText = auditNoteLabel(ev.note)
                return (
                  <div key={ev.id} className="rounded-lg bg-gray-100 dark:bg-zinc-800/60 px-3 py-2.5 text-xs space-y-1.5">
                    <div className="flex items-center gap-2">
                      <span>{evInfo.icon}</span>
                      <span className={`font-medium ${evInfo.tone}`}>{evInfo.label}</span>
                      <span className="text-gray-400 dark:text-zinc-600">·</span>
                      <span className="text-gray-500 dark:text-zinc-500" title={fullDate(ev.created_at)}>{timeAgo(ev.created_at)}</span>
                    </div>
                    <CopyableFingerprint label="Previous" value={ev.previous_fingerprint} />
                    <CopyableFingerprint label="New" value={ev.new_fingerprint} />
                    {noteText && <p className="text-gray-500 dark:text-zinc-500 text-[11px] leading-snug">{noteText}</p>}
                  </div>
                )
              })
            )}
          </div>
        )}
      </div>

      {/* Last action error */}
      {actionError && <p className="text-xs text-red-500 dark:text-red-400">{actionError}</p>}
    </div>
  )
}
