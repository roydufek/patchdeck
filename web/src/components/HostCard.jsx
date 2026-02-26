import React, { useState, useRef, useEffect } from 'react'
import { connectionIndicator, hostKeyHealth, isConnectionFailureMessage } from '../utils/status.js'
import { formatRelativeTime, formatTimestamp, truncateText, staleLabel } from '../utils/format.js'
import { timeAgo, fullDate } from '../utils/timeago.js'
import HostDetails from './HostDetails.jsx'
import ConfirmDialog from './ConfirmDialog.jsx'
import { useToastContext } from './Toast.jsx'

function RecoveryBanner({ recoveryMonitor, hostId }) {
  const [dismissed, setDismissed] = useState(false)
  const autoDismissRef = useRef(null)

  // Auto-dismiss recovered banner after 5s
  useEffect(() => {
    if (recoveryMonitor.status === 'recovered' && recoveryMonitor.hostId === hostId) {
      autoDismissRef.current = setTimeout(() => setDismissed(true), 5000)
      return () => clearTimeout(autoDismissRef.current)
    }
    setDismissed(false)
  }, [recoveryMonitor.status, recoveryMonitor.hostId, hostId])

  if (!recoveryMonitor || recoveryMonitor.hostId !== hostId) return null

  if (recoveryMonitor.status === 'monitoring') {
    const pct = recoveryMonitor.elapsed > 0 ? Math.min(100, (recoveryMonitor.elapsed / 180) * 100) : 0
    return (
      <div className="mx-3 mb-3 rounded-lg border border-amber-300 dark:border-amber-800/50 bg-amber-50 dark:bg-amber-950/30 px-4 py-3" onClick={e => e.stopPropagation()}>
        <div className="flex items-center gap-2 mb-2">
          <span className="relative flex h-2 w-2 flex-shrink-0">
            <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-amber-400 opacity-75" />
            <span className="relative inline-flex rounded-full h-2 w-2 bg-amber-500" />
          </span>
          <span className="text-xs text-amber-700 dark:text-amber-200 font-medium">Waiting for host to come back up…</span>
        </div>
        <div className="flex items-center gap-3 mb-1.5">
          <span className="text-[10px] text-amber-600 dark:text-amber-300/80">Attempt {recoveryMonitor.attempts} · {recoveryMonitor.elapsed}s elapsed</span>
        </div>
        <div className="h-1.5 bg-gray-200 dark:bg-zinc-800 rounded-full overflow-hidden">
          <div
            className="h-full bg-amber-500 rounded-full transition-all duration-1000"
            style={{ width: `${pct}%` }}
          />
        </div>
      </div>
    )
  }

  if (recoveryMonitor.status === 'recovered' && !dismissed) {
    return (
      <div className="mx-3 mb-3 rounded-lg border border-emerald-300 dark:border-emerald-800/50 bg-emerald-50 dark:bg-emerald-950/30 px-4 py-2.5 flex items-center justify-between" onClick={e => e.stopPropagation()}>
        <div className="flex items-center gap-2">
          <span className="text-emerald-500 dark:text-emerald-400 flex-shrink-0">✓</span>
          <span className="text-xs text-emerald-600 dark:text-emerald-300">Host recovered after {recoveryMonitor.elapsed}s</span>
        </div>
        <button
          onClick={() => { setDismissed(true); recoveryMonitor.reset() }}
          className="text-xs text-gray-400 dark:text-zinc-500 hover:text-gray-600 dark:hover:text-zinc-300 transition-colors ml-2"
        >
          ✕
        </button>
      </div>
    )
  }

  if (recoveryMonitor.status === 'timeout') {
    return (
      <div className="mx-3 mb-3 rounded-lg border border-amber-300 dark:border-amber-800/50 bg-amber-50 dark:bg-amber-950/30 px-4 py-2.5 flex items-center justify-between" onClick={e => e.stopPropagation()}>
        <div className="flex items-center gap-2">
          <span className="text-amber-500 dark:text-amber-400 flex-shrink-0">⚠</span>
          <span className="text-xs text-amber-600 dark:text-amber-300">Host hasn't responded after 3 minutes — may need a manual check</span>
        </div>
        <button
          onClick={() => recoveryMonitor.reset()}
          className="text-xs text-gray-400 dark:text-zinc-500 hover:text-gray-600 dark:hover:text-zinc-300 transition-colors ml-2"
        >
          ✕
        </button>
      </div>
    )
  }

  if (recoveryMonitor.status === 'error') {
    return (
      <div className="mx-3 mb-3 rounded-lg border border-red-300 dark:border-red-800/50 bg-red-50 dark:bg-red-950/30 px-4 py-2.5 flex items-center justify-between" onClick={e => e.stopPropagation()}>
        <div className="flex items-center gap-2">
          <span className="text-red-500 dark:text-red-400 flex-shrink-0">✗</span>
          <span className="text-xs text-red-600 dark:text-red-300">Recovery monitoring failed</span>
        </div>
        <button
          onClick={() => recoveryMonitor.reset()}
          className="text-xs text-gray-400 dark:text-zinc-500 hover:text-gray-600 dark:hover:text-zinc-300 transition-colors ml-2"
        >
          ✕
        </button>
      </div>
    )
  }

  return null
}

function PostApplyBanner({ prompt, hostId, onReboot, onRestartServices, onDismiss }) {
  if (!prompt || prompt.hostId !== hostId) return null

  if (prompt.type === 'reboot') {
    return (
      <div className="mx-3 mb-3 rounded-lg border border-blue-300 dark:border-blue-800/50 bg-blue-50 dark:bg-blue-950/30 px-4 py-2.5 flex items-center justify-between" onClick={e => e.stopPropagation()}>
        <div className="flex items-center gap-2">
          <span className="text-blue-500 dark:text-blue-400 flex-shrink-0">↻</span>
          <span className="text-xs text-blue-600 dark:text-blue-300">Updates applied. Host needs a reboot.</span>
        </div>
        <div className="flex items-center gap-2 ml-2">
          <button
            onClick={() => { onDismiss(); onReboot(hostId) }}
            className="rounded-md px-2.5 py-1 text-xs border border-blue-400 dark:border-blue-700/60 text-blue-600 dark:text-blue-200 hover:bg-blue-100 dark:hover:bg-blue-900/30 transition-colors"
          >
            Reboot now
          </button>
          <button
            onClick={onDismiss}
            className="text-xs text-gray-400 dark:text-zinc-500 hover:text-gray-600 dark:hover:text-zinc-300 transition-colors"
          >
            ✕
          </button>
        </div>
      </div>
    )
  }

  if (prompt.type === 'restart') {
    const count = Array.isArray(prompt.services) ? prompt.services.length : 0
    return (
      <div className="mx-3 mb-3 rounded-lg border border-blue-300 dark:border-blue-800/50 bg-blue-50 dark:bg-blue-950/30 px-4 py-2.5 flex items-center justify-between" onClick={e => e.stopPropagation()}>
        <div className="flex items-center gap-2">
          <span className="text-blue-500 dark:text-blue-400 flex-shrink-0">↻</span>
          <span className="text-xs text-blue-600 dark:text-blue-300">Updates applied. {count} service{count !== 1 ? 's' : ''} need restart.</span>
        </div>
        <div className="flex items-center gap-2 ml-2">
          <button
            onClick={() => { onDismiss(); onRestartServices(hostId, prompt.services) }}
            className="rounded-md px-2.5 py-1 text-xs border border-blue-400 dark:border-blue-700/60 text-blue-600 dark:text-blue-200 hover:bg-blue-100 dark:hover:bg-blue-900/30 transition-colors"
          >
            Restart now
          </button>
          <button
            onClick={onDismiss}
            className="text-xs text-gray-400 dark:text-zinc-500 hover:text-gray-600 dark:hover:text-zinc-300 transition-colors"
          >
            ✕
          </button>
        </div>
      </div>
    )
  }

  return null
}

function ScanAgeBadge({ scan }) {
  if (!scan?.updated_at) {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-gray-100 dark:bg-zinc-800 px-2 py-0.5 text-[10px] text-gray-500 dark:text-zinc-500">
        No data
      </span>
    )
  }
  const d = new Date(scan.updated_at)
  if (Number.isNaN(d.getTime())) return null
  const ageMs = Date.now() - d.getTime()
  const oneHour = 3_600_000
  const twentyFourHours = 86_400_000
  const seventyTwoHours = 259_200_000

  if (ageMs < oneHour) {
    // Fresh — green dot
    return (
      <span className="inline-flex items-center gap-1 text-[10px] text-gray-500 dark:text-zinc-500" title={`Scanned ${fullDate(scan.updated_at)}`}>
        <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />
        Fresh
      </span>
    )
  }
  if (ageMs < twentyFourHours) {
    // Normal — no badge
    return null
  }
  if (ageMs < seventyTwoHours) {
    // Stale amber
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-amber-50 dark:bg-amber-950/40 border border-amber-300 dark:border-amber-800/40 px-2 py-0.5 text-[10px] text-amber-600 dark:text-amber-400" title={`Scanned ${fullDate(scan.updated_at)}`}>
        Stale
      </span>
    )
  }
  // Very stale — red
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-red-50 dark:bg-red-950/40 border border-red-300 dark:border-red-800/40 px-2 py-0.5 text-[10px] text-red-600 dark:text-red-400" title={`Scanned ${fullDate(scan.updated_at)}`}>
      Stale
    </span>
  )
}

function StreamPanel({ host, mode, output, phase, progress, isStreaming, error, result, onClose }) {
  const scrollRef = useRef(null)
  const userScrolledRef = useRef(false)

  // Auto-scroll logic
  useEffect(() => {
    const el = scrollRef.current
    if (!el) return
    if (!userScrolledRef.current) {
      el.scrollTop = el.scrollHeight
    }
  }, [output])

  function handleScroll() {
    const el = scrollRef.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 32
    userScrolledRef.current = !atBottom
  }

  const finished = !isStreaming
  const failed = !!error
  const succeeded = finished && result && !error

  // Determine status text
  let statusText = ''
  if (isStreaming) {
    statusText = mode === 'scan' ? `Scanning ${host.name}...` : `Applying updates on ${host.name}...`
  } else if (succeeded) {
    if (mode === 'scan') {
      const count = Array.isArray(result.packages) ? result.packages.length : 0
      statusText = `Scan complete · ${count} update${count !== 1 ? 's' : ''} available`
    } else {
      const changed = typeof result.changed_packages === 'number' ? result.changed_packages : 0
      statusText = `Apply complete · ${changed} package${changed !== 1 ? 's' : ''} updated`
    }
  } else if (failed) {
    statusText = `${mode === 'scan' ? 'Scan' : 'Apply'} failed`
  }

  // Progress bar for apply
  const showProgress = mode === 'apply' && progress && typeof progress.percent === 'number'
  const pct = showProgress ? Math.min(100, Math.max(0, progress.percent)) : 0

  return (
    <div className="mx-3 mb-3 rounded-lg border border-gray-200 dark:border-zinc-700/50 bg-gray-50 dark:bg-zinc-950 overflow-hidden">
      {/* Header */}
      <div className="flex items-center justify-between px-3 py-2 border-b border-gray-200 dark:border-zinc-800/50">
        <div className="flex items-center gap-2 min-w-0">
          {isStreaming && (
            <span className="relative flex h-2 w-2 flex-shrink-0">
              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-blue-400 opacity-75" />
              <span className="relative inline-flex rounded-full h-2 w-2 bg-blue-500" />
            </span>
          )}
          {succeeded && <span className="text-emerald-500 dark:text-emerald-400 flex-shrink-0">✓</span>}
          {failed && <span className="text-red-500 dark:text-red-400 flex-shrink-0">✗</span>}
          <span className={`text-xs truncate ${succeeded ? 'text-emerald-600 dark:text-emerald-400' : failed ? 'text-red-600 dark:text-red-400' : 'text-gray-700 dark:text-zinc-300'}`}>
            {statusText}
          </span>
        </div>
        {finished && (
          <button
            onClick={onClose}
            className="ml-2 text-xs text-gray-400 dark:text-zinc-500 hover:text-gray-600 dark:hover:text-zinc-300 transition-colors flex-shrink-0"
          >
            Close
          </button>
        )}
      </div>

      {/* Terminal output */}
      <div
        ref={scrollRef}
        onScroll={handleScroll}
        className="px-3 py-2 max-h-48 overflow-y-auto font-mono text-xs leading-relaxed text-gray-600 dark:text-zinc-400 scrollbar-thin"
      >
        {output.map((line, i) => (
          <div key={i} className="whitespace-pre-wrap break-all">
            {line.text}
          </div>
        ))}
        {output.length === 0 && isStreaming && (
          <div className="text-gray-400 dark:text-zinc-600 italic">Connecting...</div>
        )}
      </div>

      {/* Progress bar for apply */}
      {showProgress && (
        <div className="px-3 pb-2">
          <div className="h-1.5 bg-gray-200 dark:bg-zinc-800 rounded-full overflow-hidden">
            <div
              className="h-full bg-blue-500 rounded-full transition-all duration-300"
              style={{ width: `${pct}%` }}
            />
          </div>
          <div className="flex items-center justify-between mt-1">
            <span className="text-[10px] text-gray-500 dark:text-zinc-500">
              {progress.message || progress.phase || ''}
            </span>
            <span className="text-[10px] text-gray-500 dark:text-zinc-500">{pct}%</span>
          </div>
        </div>
      )}

      {/* Error message */}
      {failed && (
        <div className="px-3 pb-2">
          <p className="text-xs text-red-500 dark:text-red-400">{error}</p>
        </div>
      )}

      {/* Summary when done */}
      {succeeded && (
        <div className="px-3 pb-2 border-t border-gray-200 dark:border-zinc-800/50 pt-2">
          <p className="text-xs text-emerald-600 dark:text-emerald-400/80">
            {succeeded && statusText}
          </p>
        </div>
      )}
    </div>
  )
}

export default function HostCard({
  host: h, scan: snap, connectivity, actionState, actionError,
  actionBusy, expanded, onToggleExpand,
  onScan, onApply, onRefreshConnectivity, onEdit, onDelete,
  onUpdateHostOps, onUpdateHostKeyPolicy, onResolveHostKeyMismatch,
  onUpdateHostNotificationPrefs, onLoadHostKeyAudit, hostKeyAudit,
  onRestartServices, onReboot, onShutdown,
  // Streaming props
  streamActive, streamMode: sMode, streamOutput, streamPhase, streamProgress,
  streamIsStreaming, streamError, streamResult, onCloseStream,
  // Recovery monitor
  recoveryMonitor,
  // Post-apply prompt
  postApplyPrompt, onDismissPostApplyPrompt,
  // Selection
  selected, onToggleSelect
}) {
  const [showMore, setShowMore] = useState(false)
  const [confirmDialog, setConfirmDialog] = useState(null) // { type, title, message, color, payload }
  const toast = useToastContext()

  const checksEnabled = h.checks_enabled !== false
  const applyAllowed = checksEnabled && !h.host_key_pending_fingerprint
  const hostKeyMismatchPending = !!h.host_key_pending_fingerprint

  // Connection
  const quickConnectivityError = connectivity && connectivity.connected === false
    ? (connectivity.error || 'Quick SSH check failed') : ''
  const scanAction = actionState.scan || null
  const connectionError = [
    quickConnectivityError,
    actionError,
    scanAction && !scanAction.ok ? scanAction.summary : ''
  ].find(msg => isConnectionFailureMessage(msg)) || ''
  const connection = connectionIndicator(h, connectivity, connectionError || actionError, snap)
  const connected = connection.ok

  // Package count
  const packageCount = Array.isArray(snap?.packages) ? snap.packages.length : 0

  // Restart
  const restartServicesCount = Array.isArray(snap?.needs_restart) ? snap.needs_restart.length : 0
  const restartNeeded = !!(snap?.needs_reboot || restartServicesCount > 0)

  // Busy
  const connectivityBusy = !!actionBusy[`${h.id}:connectivity`]
  const scanBusy = !!actionBusy[`${h.id}:scan`]
  const applyBusy = !!actionBusy[`${h.id}:apply`]
  const deleteBusy = !!actionBusy[`host:delete:${h.id}`]
  const anyBusy = scanBusy || applyBusy || deleteBusy

  // Blocked reasons
  const scanBlockedReason = !checksEnabled
    ? 'Host checks are disabled.'
    : hostKeyMismatchPending
      ? 'Blocked: SSH host key mismatch is pending review.'
      : ''
  const applyBlockedReason = !checksEnabled
    ? 'Apply requires checks to be enabled.'
    : hostKeyMismatchPending
      ? 'Blocked: SSH host key mismatch is pending review.'
      : ''

  // Latest action
  const applyAction = actionState.apply || null
  const latestAction = actionState.latest || [scanAction, applyAction]
    .filter(Boolean)
    .sort((a, b) => new Date(b.at).getTime() - new Date(a.at).getTime())[0]
  const lastActionError = latestAction && !latestAction.ok ? latestAction.summary : actionError

  // Status line
  let statusParts = []
  let updateCountColor = ''
  if (snap) {
    if (packageCount === 0) {
      updateCountColor = 'text-emerald-500'
    } else if (packageCount <= 10) {
      updateCountColor = 'text-amber-400'
    } else {
      updateCountColor = 'text-red-400'
    }
    statusParts.push(packageCount > 0 ? `${packageCount} update${packageCount !== 1 ? 's' : ''} pending` : 'Up to date')
    if (restartNeeded) {
      statusParts.push(snap.needs_reboot ? 'Reboot required' : `${restartServicesCount} service restart${restartServicesCount !== 1 ? 's' : ''}`)
    } else {
      statusParts.push('No restart needed')
    }
  } else {
    statusParts.push('No scan data')
  }

  // Scan age
  const scanAgeLabel = snap?.updated_at ? timeAgo(snap.updated_at) : null
  const scanAgeTitle = snap?.updated_at ? fullDate(snap.updated_at) : ''

  // Confirmation dialog handlers
  function handleApplyClick() {
    setConfirmDialog({
      type: 'apply',
      title: 'Apply updates',
      message: `Apply ${packageCount} pending update${packageCount !== 1 ? 's' : ''} to ${h.name}? This will run apt dist-upgrade.`,
      color: 'blue'
    })
  }

  function handleRebootClick() {
    setConfirmDialog({
      type: 'reboot',
      title: 'Reboot host',
      message: `Reboot ${h.name}? The host will be temporarily unavailable.`,
      color: 'red'
    })
  }

  function handleShutdownClick() {
    setConfirmDialog({
      type: 'shutdown',
      title: 'Shut down host',
      message: `Shut down ${h.name}? The host will go offline until manually powered on.`,
      color: 'red'
    })
  }

  function handleDeleteClick() {
    setConfirmDialog({
      type: 'delete',
      title: 'Delete host',
      message: `Delete ${h.name} (${h.address})? This also removes its scan history and scheduled jobs.`,
      color: 'red'
    })
  }

  function handleHostKeyAction(action) {
    const verb = action === 'accept' ? 'Accept' : 'Deny'
    setConfirmDialog({
      type: 'host-key',
      title: `${verb} SSH fingerprint`,
      message: action === 'accept'
        ? `Accept the new SSH fingerprint for ${h.name}? This will trust the presented key and unblock operations.`
        : `Deny the new SSH fingerprint for ${h.name}? Operations will stay blocked until a valid fingerprint is accepted.`,
      color: action === 'accept' ? 'blue' : 'red',
      payload: { action }
    })
  }

  async function handleConfirm() {
    const dialog = confirmDialog
    setConfirmDialog(null)
    if (!dialog) return
    if (dialog.type === 'apply') onApply()
    if (dialog.type === 'reboot') onReboot(h.id)
    if (dialog.type === 'shutdown') onShutdown(h.id)
    if (dialog.type === 'delete') {
      const result = await onDelete()
      if (result?.ok) {
        toast.addToast({ type: 'success', message: `${h.name} deleted.` })
      } else if (result?.error) {
        toast.addToast({ type: 'error', message: result.error, duration: 6000 })
      }
    }
    if (dialog.type === 'host-key') {
      const result = await onResolveHostKeyMismatch(h, dialog.payload.action)
      if (result?.ok) {
        toast.addToast({ type: 'success', message: `SSH fingerprint ${dialog.payload.action === 'accept' ? 'accepted' : 'denied'} for ${h.name}.` })
      } else if (result?.error) {
        toast.addToast({ type: 'error', message: result.error, duration: 6000 })
      }
    }
  }

  return (
    <div className={`rounded-xl border transition-colors ${selected ? 'border-emerald-700/60 bg-gray-50 dark:bg-zinc-900/60' : connected ? 'border-gray-200 dark:border-zinc-800 hover:border-gray-300 dark:hover:border-zinc-700' : 'border-gray-200 dark:border-zinc-800 hover:border-gray-300 dark:hover:border-zinc-700'} bg-white dark:bg-zinc-900/40`}>
      {/* Confirmation dialog */}
      <ConfirmDialog
        open={!!confirmDialog}
        title={confirmDialog?.title || ''}
        message={confirmDialog?.message || ''}
        confirmLabel={
          confirmDialog?.type === 'apply' ? 'Apply'
          : confirmDialog?.type === 'reboot' ? 'Reboot'
          : confirmDialog?.type === 'delete' ? 'Delete'
          : confirmDialog?.type === 'host-key' ? (confirmDialog?.payload?.action === 'accept' ? 'Accept' : 'Deny')
          : confirmDialog?.type === 'shutdown' ? 'Shut down'
          : 'Confirm'
        }
        confirmColor={confirmDialog?.color || 'red'}
        onConfirm={handleConfirm}
        onCancel={() => setConfirmDialog(null)}
      />

      {/* Compact view */}
      <div
        className="px-4 sm:px-5 py-3 sm:py-4 cursor-pointer select-none"
        onClick={onToggleExpand}
      >
        {/* Top row: checkbox + dot + name + tags */}
        <div className="flex items-center gap-2 sm:gap-3 min-w-0">
          {/* Selection checkbox */}
          {onToggleSelect && (
            <div onClick={e => e.stopPropagation()} className="flex-shrink-0">
              <input
                type="checkbox"
                checked={!!selected}
                onChange={onToggleSelect}
                className="rounded border-gray-300 dark:border-zinc-600 bg-gray-100 dark:bg-zinc-800 text-emerald-500 focus:ring-0 focus:ring-offset-0 cursor-pointer"
              />
            </div>
          )}

          {/* Connection dot */}
          <span
            className={`flex-shrink-0 h-2.5 w-2.5 rounded-full ${connected ? 'bg-emerald-500' : hostKeyMismatchPending ? 'bg-red-500' : 'bg-red-500'}`}
            title={connection.label}
          />

          {/* Host name */}
          <span className="font-medium text-sm truncate">{h.name}</span>

          {/* Tags (hidden on very small screens) */}
          {Array.isArray(h.tags) && h.tags.length > 0 && (
            <span className="hidden sm:flex items-center gap-1 flex-shrink-0">
              {h.tags.slice(0, 4).map((tag, i) => (
                <span key={i} className="inline-flex items-center rounded-full bg-gray-100 dark:bg-zinc-800 border border-gray-200 dark:border-zinc-700/50 px-1.5 py-px text-[10px] text-gray-600 dark:text-zinc-400 leading-tight">{tag}</span>
              ))}
              {h.tags.length > 4 && (
                <span className="text-[10px] text-gray-400 dark:text-zinc-600">+{h.tags.length - 4}</span>
              )}
            </span>
          )}

          {/* SSH address (hidden on mobile) */}
          <span className="hidden md:inline text-xs text-gray-500 dark:text-zinc-500 font-mono truncate">{h.ssh_user}@{h.address}:{h.port}</span>
        </div>

        {/* Status line */}
        <div className="flex items-center gap-2 mt-1.5 ml-0 sm:ml-[calc(0.625rem+0.75rem)]">
          <span className="text-xs text-gray-500 dark:text-zinc-500">
            {snap ? (
              <>
                <span className={updateCountColor}>{packageCount}</span>
                {packageCount > 0 ? ` update${packageCount !== 1 ? 's' : ''} pending` : ' — Up to date'}
                <span className="hidden sm:inline">
                  {' · '}
                  {restartNeeded
                    ? (snap.needs_reboot
                      ? <span className="text-amber-500 dark:text-amber-400 font-medium">{snap.reboot_reason
                          ? `⚠ Reboot required (${snap.reboot_reason})`
                          : '⚠ Reboot required'}</span>
                      : `${restartServicesCount} service restart${restartServicesCount !== 1 ? 's' : ''}`)
                    : 'No restart needed'}
                </span>
                {snap.os_name && (
                  <span className="hidden lg:inline"> · <span className="text-gray-500 dark:text-zinc-500">{snap.os_name}</span></span>
                )}
                {snap.uptime && (
                  <span className="hidden lg:inline"> · <span className="text-gray-400 dark:text-zinc-600">up {snap.uptime}</span></span>
                )}
              </>
            ) : 'No scan data'}
          </span>
          {restartNeeded && snap && (
            <span className={`sm:hidden text-[10px] font-medium ${snap.needs_reboot ? 'text-amber-500 dark:text-amber-400' : 'text-gray-500 dark:text-zinc-400'}`}>
              · {snap.needs_reboot ? '⚠ Reboot' : 'Restart'}
            </span>
          )}
          {scanAgeLabel && (
            <span className="text-[10px] text-gray-400 dark:text-zinc-600" title={scanAgeTitle}>· scanned {scanAgeLabel}</span>
          )}
          <ScanAgeBadge scan={snap} />
        </div>

        {/* Action buttons */}
        <div className="flex items-center gap-1.5 mt-2.5 ml-0 sm:ml-[calc(0.625rem+0.75rem)]" onClick={e => e.stopPropagation()}>
          <button
            onClick={onScan}
            disabled={!!scanBlockedReason || scanBusy || anyBusy}
            title={scanBlockedReason || 'Run scan'}
            className="rounded-lg px-3 py-1.5 text-xs border border-gray-300 dark:border-zinc-700 text-gray-700 dark:text-zinc-300 hover:text-gray-900 dark:hover:text-white hover:border-gray-400 dark:hover:border-zinc-500 disabled:opacity-30 disabled:hover:border-gray-300 dark:disabled:hover:border-zinc-700 transition-colors"
          >
            {scanBusy ? 'Scanning…' : 'Scan'}
          </button>
          {packageCount > 0 && (
          <button
            onClick={handleApplyClick}
            disabled={!!applyBlockedReason || applyBusy || anyBusy}
            title={applyBlockedReason || 'Apply updates'}
            className="rounded-lg px-3 py-1.5 text-xs border border-gray-300 dark:border-zinc-700 text-gray-700 dark:text-zinc-300 hover:text-gray-900 dark:hover:text-white hover:border-gray-400 dark:hover:border-zinc-500 disabled:opacity-30 disabled:hover:border-gray-300 dark:disabled:hover:border-zinc-700 transition-colors"
          >
            {applyBusy ? 'Applying…' : 'Apply'}
          </button>
          )}
          <button
            onClick={onRefreshConnectivity}
            disabled={connectivityBusy || anyBusy}
            title="Recheck connectivity"
            className="rounded-lg px-3 py-1.5 text-xs border border-gray-300 dark:border-zinc-700 text-gray-700 dark:text-zinc-300 hover:text-gray-900 dark:hover:text-white hover:border-gray-400 dark:hover:border-zinc-500 disabled:opacity-30 transition-colors"
          >
            {connectivityBusy ? '…' : '↻'}
          </button>
          <button
            onClick={() => setShowMore(v => !v)}
            title="More actions"
            className="rounded-lg px-2 py-1.5 text-xs border border-gray-300 dark:border-zinc-700 text-gray-500 dark:text-zinc-400 hover:text-gray-900 dark:hover:text-white hover:border-gray-400 dark:hover:border-zinc-500 transition-colors"
          >
            ···
          </button>
        </div>
      </div>

      {/* More actions dropdown */}
      {showMore && (
        <div className="px-5 pb-3 flex items-center gap-2 border-t border-gray-200 dark:border-zinc-800/50 pt-3" onClick={e => e.stopPropagation()}>
          <button
            onClick={() => { onEdit(); setShowMore(false) }}
            disabled={anyBusy}
            className="rounded-lg px-3 py-1.5 text-xs border border-gray-300 dark:border-zinc-700 text-gray-700 dark:text-zinc-300 hover:text-gray-900 dark:hover:text-white hover:border-gray-400 dark:hover:border-zinc-500 disabled:opacity-30 transition-colors"
          >
            Edit
          </button>
          <button
            onClick={() => { handleRebootClick(); setShowMore(false) }}
            disabled={!!scanBlockedReason || anyBusy}
            title={scanBlockedReason || 'Reboot host'}
            className="rounded-lg px-3 py-1.5 text-xs border border-amber-600/50 dark:border-amber-800/60 text-amber-600 dark:text-amber-400 hover:text-amber-500 dark:hover:text-amber-300 hover:border-amber-500 dark:hover:border-amber-700 disabled:opacity-30 transition-colors"
          >
            Reboot
          </button>
          <button
            onClick={() => { handleShutdownClick(); setShowMore(false) }}
            disabled={!!scanBlockedReason || anyBusy}
            title={scanBlockedReason || 'Shut down host'}
            className="rounded-lg px-3 py-1.5 text-xs border border-red-600/50 dark:border-red-800/60 text-red-500 dark:text-red-400 hover:text-red-400 dark:hover:text-red-300 hover:border-red-500 dark:hover:border-red-700 disabled:opacity-30 transition-colors"
          >
            Shut down
          </button>
          <button
            onClick={() => { handleDeleteClick(); setShowMore(false) }}
            disabled={anyBusy}
            className="rounded-lg px-3 py-1.5 text-xs border border-red-800/60 text-red-400 hover:text-red-300 hover:border-red-700 disabled:opacity-30 transition-colors"
          >
            {deleteBusy ? 'Deleting…' : 'Delete'}
          </button>
        </div>
      )}

      {/* Host key mismatch banner */}
      {hostKeyMismatchPending && (
        <div className="mx-5 mb-3 rounded-lg border border-amber-300 dark:border-amber-800/60 bg-amber-50 dark:bg-amber-950/30 px-4 py-3 text-xs text-amber-700 dark:text-amber-200" onClick={e => e.stopPropagation()}>
          <p className="font-medium mb-1">SSH host key mismatch — operations blocked</p>
          <p className="text-amber-600 dark:text-amber-300/80 mb-2">
            Trusted: <span className="font-mono">{(h.host_key_trusted_fingerprint || h.host_key_pinned_fingerprint || 'none').slice(0, 20)}…</span>
            {' '}→ Presented: <span className="font-mono">{(h.host_key_pending_fingerprint || '').slice(0, 20)}…</span>
          </p>
          <div className="flex items-center gap-2">
            <button
              onClick={() => handleHostKeyAction('accept')}
              className="rounded-md px-3 py-1 border border-amber-400 dark:border-amber-700/70 text-amber-700 dark:text-amber-200 hover:bg-amber-100 dark:hover:bg-amber-900/30 transition-colors"
            >
              Accept
            </button>
            <button
              onClick={() => handleHostKeyAction('deny')}
              className="rounded-md px-3 py-1 border border-amber-400 dark:border-amber-700/70 text-amber-700 dark:text-amber-200 hover:bg-amber-100 dark:hover:bg-amber-900/30 transition-colors"
            >
              Deny
            </button>
          </div>
        </div>
      )}

      {/* Recovery monitor banner */}
      <RecoveryBanner recoveryMonitor={recoveryMonitor} hostId={h.id} />

      {/* Post-apply prompt banner */}
      <PostApplyBanner
        prompt={postApplyPrompt}
        hostId={h.id}
        onReboot={onReboot}
        onRestartServices={onRestartServices}
        onDismiss={onDismissPostApplyPrompt}
      />

      {/* Stream panel */}
      {streamActive && (streamIsStreaming || streamOutput.length > 0 || streamError || streamResult) && (
        <StreamPanel
          host={h}
          mode={sMode}
          output={streamOutput}
          phase={streamPhase}
          progress={streamProgress}
          isStreaming={streamIsStreaming}
          error={streamError}
          result={streamResult}
          onClose={onCloseStream}
        />
      )}

      {/* Last action error */}
      {lastActionError && !expanded && !streamActive && (
        <div className="px-5 pb-3">
          <p className="text-xs text-red-500 dark:text-red-400 truncate">{lastActionError}</p>
        </div>
      )}

      {/* Expanded details */}
      {expanded && (
        <HostDetails
          host={h}
          scan={snap}
          connectivity={connectivity}
          connection={connection}
          actionState={actionState}
          actionError={actionError}
          actionBusy={actionBusy}
          onUpdateHostOps={onUpdateHostOps}
          onUpdateHostKeyPolicy={onUpdateHostKeyPolicy}
          onResolveHostKeyMismatch={onResolveHostKeyMismatch}
          onUpdateHostNotificationPrefs={onUpdateHostNotificationPrefs}
          onLoadHostKeyAudit={onLoadHostKeyAudit}
          hostKeyAudit={hostKeyAudit}
          onEdit={onEdit}
          onDelete={onDelete}
          onRestartServices={onRestartServices}
          onReboot={onReboot}
          onShutdown={onShutdown}
        />
      )}
    </div>
  )
}
