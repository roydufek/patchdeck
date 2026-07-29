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

// Units patchdeck won't restart with a naive `systemctl restart` — a live restart of the
// D-Bus broker or logind severs the session bus and locks out SSH. Mirrors the backend's
// isRiskyRestartUnit; used to give the correct "reboot to apply" guidance (vs the container-shim
// explanation) when one of these stays flagged after a restart attempt.
const RISKY_RESTART_UNITS = new Set([
  'dbus', 'dbus.service', 'dbus.socket',
  'dbus-broker', 'dbus-broker.service',
  'systemd-logind', 'systemd-logind.service',
])
const isRiskyRestartUnit = (svc) => RISKY_RESTART_UNITS.has(String(svc).trim().toLowerCase())

export default function HostDetails({
  host: h, scan: snap, connectivity, connection, actionState, actionError,
  actionBusy, onUpdateHostOps, onUpdateHostKeyPolicy,
  onResolveHostKeyMismatch, onUpdateHostNotificationPrefs,
  onLoadHostKeyAudit, hostKeyAudit,
  onEdit, onDelete,
  onRestartServices, onRestartAll, onReboot, onShutdown
}) {
  const [auditOpen, setAuditOpen] = useState(false)
  const [rawOutputOpen, setRawOutputOpen] = useState(false)
  const [selectedServices, setSelectedServices] = useState([])
  const [restartedServices, setRestartedServices] = useState([]) // last submitted for restart — to detect units that don't clear
  const [restartConfirm, setRestartConfirm] = useState(null) // 'reboot' | 'restart-services' | 'restart-all' | null
  const [advancedRestartOpen, setAdvancedRestartOpen] = useState(false)

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
  const deferredPackages = Array.isArray(snap?.deferred_packages) ? snap.deferred_packages : []
  const restartServicesCount = Array.isArray(snap?.needs_restart) ? snap.needs_restart.length : 0
  const restartServices = Array.isArray(snap?.needs_restart) ? snap.needs_restart : []
  const restartNeeded = !!(snap?.needs_reboot || restartServicesCount > 0)
  // Units that needrestart's coordinated pass will skip as unsafe to restart live — pre-deselected
  // in the advanced list and labelled, mirroring needrestart's own interactive screen.
  const safeRestartServices = restartServices.filter(s => !isRiskyRestartUnit(s))
  const needrestartInstalled = snap?.needrestart_found !== false

  const rebootBusy = !!actionBusy[`${h.id}:reboot`]
  const restartServicesBusy = !!actionBusy[`${h.id}:restart-services`]

  // Services the user restarted that are STILL flagged once the follow-up scan settled.
  // Two distinct reasons, surfaced with different guidance (gated on no action in flight so
  // they only appear after the re-scan has refreshed needs_restart):
  //   - risky units (dbus/dbus-broker/systemd-logind): patchdeck refused a destructive live
  //     restart because the host had no needrestart coordinated handler — a reboot is required.
  //   - everything else (e.g. containerd): the restart ran, but needrestart still flags it
  //     because long-lived child processes (container shims) keep the old binary mapped.
  const restartResidual = restartedServices.filter(s => restartServices.includes(s))
  const rebootRequiredResidual = restartResidual.filter(isRiskyRestartUnit)
  const shimResidual = restartResidual.filter(s => !isRiskyRestartUnit(s))
  const showRebootRequired = rebootRequiredResidual.length > 0 && !scanBusy && !restartServicesBusy
  const showShimResidual = shimResidual.length > 0 && !scanBusy && !restartServicesBusy

  const scanFailureNotificationsEnabled = h.notification_prefs?.scan_failure !== false

  // Raw output detection
  const hasRawOutput = !!(snap?.raw_output)

  return (
    <div className="border-t border-gray-200 dark:border-zinc-800/60 px-5 py-4 space-y-5" onClick={e => e.stopPropagation()}>
      {/* Connection & scan overview */}
      <div className="grid sm:grid-cols-3 gap-3 text-xs">
        <div className="rounded-lg border border-gray-200 dark:border-zinc-800 bg-gray-50 dark:bg-zinc-900/60 px-3 py-2.5">
          <p className="text-gray-500 dark:text-zinc-500 font-medium uppercase tracking-wider text-[10px] mb-1">Connection</p>
          <p className={`font-medium ${connection.tone === 'good' ? 'text-emerald-500 dark:text-emerald-400' : connection.tone === 'pending' ? 'text-amber-500 dark:text-amber-400' : 'text-red-500 dark:text-red-400'}`}>{connection.label}</p>
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

      {/* Deferred (phased) updates — upgradable but won't be installed by Apply yet */}
      {snap && deferredPackages.length > 0 && (
        <div className="rounded-lg border border-gray-200 dark:border-zinc-800 bg-gray-50 dark:bg-zinc-900/60 px-4 py-3 space-y-2">
          <div>
            <p className="text-xs font-medium text-gray-600 dark:text-zinc-400">
              {deferredPackages.length} deferred update{deferredPackages.length !== 1 ? 's' : ''}
              {deferredPackages.every(p => p.defer_reason === 'phased') ? ' (phased)' : ''}
            </p>
            <p className="text-[11px] text-gray-400 dark:text-zinc-500 mt-0.5">
              Upgradable, but held back for now — Ubuntu rolls these out gradually (phased). They'll install automatically once the rollout reaches this host; they're not counted as pending and "Apply" won't force them.
            </p>
          </div>
          <PackageTable packages={deferredPackages} />
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

          {/* needrestart not installed — recommend it (detection + safe restart both depend on it) */}
          {!needrestartInstalled && (
            <div className="rounded-lg border border-blue-300/60 dark:border-blue-500/30 bg-blue-50 dark:bg-blue-500/10 px-4 py-3 text-xs space-y-1.5">
              <p className="font-medium text-blue-700 dark:text-blue-300">needrestart isn’t installed on this host</p>
              <p className="text-blue-700/90 dark:text-blue-300/80 leading-relaxed">
                Patchdeck relies on <span className="font-mono">needrestart</span> to detect which services need
                restarting after an upgrade — and to restart them safely (coordinated, and skipping units that would
                break your session). Install it for full functionality:
              </p>
              <p className="font-mono text-[11px] text-blue-800 dark:text-blue-200 bg-blue-100/60 dark:bg-blue-500/10 rounded px-2 py-1 inline-block">apt install needrestart</p>
            </div>
          )}

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

          {/* Service restarts — needrestart-driven: one safe coordinated action, granular under Advanced */}
          {restartServices.length > 0 && (onRestartAll || onRestartServices) && (
            <div className="rounded-lg border border-gray-200 dark:border-zinc-700/60 bg-gray-50 dark:bg-zinc-900/60 px-4 py-3 space-y-3">
              <div>
                <p className="text-gray-700 dark:text-zinc-300 text-xs font-medium">
                  {restartServicesCount} service{restartServicesCount !== 1 ? 's' : ''} flagged by needrestart
                </p>
                <p className="text-[11px] text-gray-500 dark:text-zinc-500 mt-0.5 leading-relaxed">
                  Restart them the way needrestart would after an upgrade — coordinated, in dependency order,
                  automatically skipping units that can’t be safely restarted live (those need a reboot).
                </p>
              </div>

              {/* Primary: coordinated safe restart via needrestart -r a */}
              {onRestartAll && safeRestartServices.length > 0 && (
                <button
                  onClick={() => setRestartConfirm('restart-all')}
                  disabled={restartServicesBusy}
                  className="rounded-lg px-3 py-1.5 text-xs bg-emerald-600 text-white hover:bg-emerald-500 disabled:opacity-40 transition-colors"
                >
                  {restartServicesBusy ? 'Restarting…' : 'Restart services safely'}
                </button>
              )}

              {/* Advanced: pick individual services (risky ones excluded from select-all + labelled) */}
              {onRestartServices && (
                <div className="pt-1 border-t border-gray-200/70 dark:border-zinc-700/50">
                  <button
                    onClick={() => setAdvancedRestartOpen(v => !v)}
                    className="text-[11px] text-gray-500 dark:text-zinc-500 hover:text-gray-700 dark:hover:text-zinc-300 transition-colors"
                  >
                    {advancedRestartOpen ? '▾' : '▸'} Advanced — restart individual services
                  </button>
                  {advancedRestartOpen && (
                    <div className="mt-2 space-y-2">
                      <div className="flex items-start justify-between gap-2">
                        <p className="text-[11px] text-gray-500 dark:text-zinc-500 leading-relaxed">
                          Risky units (dbus/logind) are excluded from “select all safe” — restarting them live can
                          lock out SSH; Patchdeck routes them through needrestart’s coordinated handler or a reboot.
                        </p>
                        <label className="flex items-center gap-1.5 text-[11px] text-gray-500 dark:text-zinc-500 cursor-pointer select-none shrink-0">
                          <input
                            type="checkbox"
                            checked={safeRestartServices.length > 0 && selectedServices.length === safeRestartServices.length && selectedServices.every(s => !isRiskyRestartUnit(s))}
                            onChange={e => setSelectedServices(e.target.checked ? [...safeRestartServices] : [])}
                            className="h-3 w-3 rounded border-gray-300 dark:border-zinc-700 bg-gray-100 dark:bg-zinc-900 text-emerald-500 focus:ring-0 focus:ring-offset-0 accent-emerald-500"
                          />
                          Select all safe
                        </label>
                      </div>
                      <div className="grid sm:grid-cols-2 gap-1">
                        {restartServices.map(svc => {
                          const checked = selectedServices.includes(svc)
                          const risky = isRiskyRestartUnit(svc)
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
                              {risky && (
                                <span className="text-[10px] text-amber-600 dark:text-amber-400/90 shrink-0">coordinated · may need reboot</span>
                              )}
                            </label>
                          )
                        })}
                      </div>
                      <button
                        onClick={() => setRestartConfirm('restart-services')}
                        disabled={selectedServices.length === 0 || restartServicesBusy}
                        className="rounded-lg px-3 py-1.5 text-xs border border-gray-300 dark:border-zinc-600 text-gray-700 dark:text-zinc-300 hover:bg-gray-100 dark:hover:bg-zinc-800 disabled:opacity-40 transition-colors"
                      >
                        {restartServicesBusy
                          ? 'Restarting…'
                          : selectedServices.length > 0
                            ? `Restart ${selectedServices.length} selected`
                            : 'Restart selected'}
                      </button>
                    </div>
                  )}
                </div>
              )}
            </div>
          )}

          {/* Risky unit (dbus/logind) that patchdeck refused to restart live — reboot to apply */}
          {showRebootRequired && (
            <div className="rounded-lg border border-amber-300/70 dark:border-amber-500/30 bg-amber-50 dark:bg-amber-500/10 px-4 py-3 text-xs space-y-1.5">
              <p className="font-medium text-amber-700 dark:text-amber-300">
                Reboot required to apply: <span className="font-mono">{rebootRequiredResidual.join(', ')}</span>
              </p>
              <p className="text-amber-700/90 dark:text-amber-300/80 leading-relaxed">
                Patchdeck did <span className="font-medium">not</span> restart {rebootRequiredResidual.length > 1 ? 'these' : 'this'} live:
                restarting the D-Bus bus or <span className="font-mono">systemd-logind</span> on a running host severs the
                login-session manager and would lock out SSH. This host has no needrestart coordinated-restart
                handler for {rebootRequiredResidual.length > 1 ? 'them' : 'it'}, so a reboot is the safe way to apply the update.
              </p>
              <button
                onClick={() => setRestartConfirm('reboot')}
                disabled={rebootBusy}
                className="mt-1 rounded-lg px-3 py-1.5 text-xs border border-amber-400/60 text-amber-700 dark:text-amber-300 hover:bg-amber-100 dark:hover:bg-amber-500/20 disabled:opacity-40 transition-colors"
              >
                {rebootBusy ? 'Rebooting…' : 'Reboot host to apply'}
              </button>
            </div>
          )}

          {/* Explain a service that stays flagged after a successful restart (e.g. containerd shims) */}
          {showShimResidual && (
            <div className="rounded-lg border border-amber-300/70 dark:border-amber-500/30 bg-amber-50 dark:bg-amber-500/10 px-4 py-3 text-xs space-y-1.5">
              <p className="font-medium text-amber-700 dark:text-amber-300">
                Still flagged after restart: <span className="font-mono">{shimResidual.join(', ')}</span>
              </p>
              <p className="text-amber-700/90 dark:text-amber-300/80 leading-relaxed">
                The service was restarted, but needrestart still flags it. This is expected for units
                like <span className="font-mono">containerd</span>: each running container keeps a
                long-lived shim process mapping the old binary, and restarting the service can't
                replace those. Recycle the affected containers, or reboot the host to fully clear it.
              </p>
              <button
                onClick={() => setRestartConfirm('reboot')}
                disabled={rebootBusy}
                className="mt-1 rounded-lg px-3 py-1.5 text-xs border border-amber-400/60 text-amber-700 dark:text-amber-300 hover:bg-amber-100 dark:hover:bg-amber-500/20 disabled:opacity-40 transition-colors"
              >
                {rebootBusy ? 'Rebooting…' : 'Reboot host to clear'}
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
              setRestartedServices(selectedServices)
              onRestartServices(h.id, selectedServices)
            }}
            onCancel={() => setRestartConfirm(null)}
          />
          <ConfirmDialog
            open={restartConfirm === 'restart-all'}
            title="Restart services safely"
            message={`Run needrestart's coordinated restart on ${h.name}? It restarts the ${safeRestartServices.length} safe service${safeRestartServices.length !== 1 ? 's' : ''} in dependency order and skips units that can't be safely restarted live (those need a reboot).`}
            confirmLabel="Restart safely"
            confirmColor="emerald"
            busy={restartServicesBusy}
            onConfirm={() => {
              setRestartConfirm(null)
              onRestartAll(h.id)
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
