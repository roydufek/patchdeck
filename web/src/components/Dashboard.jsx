import React, { useMemo, useState, useCallback, useEffect, useRef } from 'react'
import HostCard from './HostCard.jsx'
import ConfirmDialog from './ConfirmDialog.jsx'
import Spinner from './Spinner.jsx'
import { connectionIndicator, isConnectionFailureMessage } from '../utils/status.js'
import { useToastContext } from './Toast.jsx'
import { API } from '../api.js'

function useAutoRefresh(onRefresh) {
  const [enabled, setEnabled] = useState(() => {
    try { return localStorage.getItem('patchdeck.autoRefresh') === 'true' } catch { return false }
  })
  const [lastRefreshed, setLastRefreshed] = useState(() => Date.now())
  const [, setTick] = useState(0)

  // Persist preference
  useEffect(() => {
    try { localStorage.setItem('patchdeck.autoRefresh', String(enabled)) } catch {}
  }, [enabled])

  // Tick every 10s to keep "X ago" text fresh
  useEffect(() => {
    const id = setInterval(() => setTick(t => t + 1), 10000)
    return () => clearInterval(id)
  }, [])

  // Poll every 30s when enabled
  useEffect(() => {
    if (!enabled) return
    const id = setInterval(() => {
      onRefresh()
      setLastRefreshed(Date.now())
    }, 30000)
    return () => clearInterval(id)
  }, [enabled, onRefresh])

  const toggle = useCallback(() => setEnabled(v => !v), [])
  const markRefreshed = useCallback(() => setLastRefreshed(Date.now()), [])

  // Relative time string — depends on lastRefreshed and tick
  const agoText = (() => {
    const diffMs = Date.now() - lastRefreshed
    if (diffMs < 5000) return 'just now'
    if (diffMs < 60000) return `${Math.floor(diffMs / 1000)}s ago`
    if (diffMs < 3600000) return `${Math.floor(diffMs / 60000)}m ago`
    return `${Math.floor(diffMs / 3600000)}h ago`
  })()

  return { enabled, toggle, lastRefreshed, markRefreshed, agoText }
}

export default function Dashboard({
  hosts, scanByHost, connectivityByHost, hostActionState, hostActionError,
  actionBusy, onScan, onScanBulk, onApply, onRefreshConnectivity, onDeleteHost,
  onEditHost, onAddHost, onExpandHost,
  hostDetailsOpen, onToggleDetails,
  // pass-through for HostDetails
  onUpdateHostOps, onUpdateHostKeyPolicy, onResolveHostKeyMismatch,
  onUpdateHostNotificationPrefs, onLoadHostKeyAudit, hostKeyAuditByHost,
  // restart/reboot
  onRestartServices, onReboot, onShutdown,
  error, loading,
  // Streaming props
  onScanStream, onApplyStream, onCloseStream,
  streamHostId, streamMode, streamOutput, streamPhase, streamProgress,
  streamIsStreaming, streamError, streamResult,
  // Recovery monitor
  recoveryMonitor,
  // Post-apply prompt
  postApplyPrompt, onDismissPostApplyPrompt,
  // Refresh callback for auto-refresh & keyboard shortcut
  onRefreshAll,
  // Lightweight scan-only refresh used between bulk scan steps
  onRefreshScans,
  // Auth token for API calls
  token
}) {
  const [hostFilter, setHostFilter] = useState('')
  const [hostStatusFilter, setHostStatusFilter] = useState('all')
  const [filtersOpen, setFiltersOpen] = useState(false)

  // Tag state
  const [allTags, setAllTags] = useState([])
  const [selectedTags, setSelectedTags] = useState([]) // OR filter
  const [groupByTag, setGroupByTag] = useState(false)

  // Fetch tags from API
  useEffect(() => {
    if (!token) return
    fetch(`${API}/tags`, { headers: { Authorization: `Bearer ${token}` } })
      .then(resp => resp.ok ? resp.json() : [])
      .then(tags => {
        if (Array.isArray(tags)) setAllTags(tags.sort())
      }).catch(() => {})
  }, [token, hosts])

  // Auto-refresh & manual refresh
  const doRefresh = useCallback(() => {
    if (onRefreshAll) onRefreshAll()
  }, [onRefreshAll])
  const autoRefresh = useAutoRefresh(doRefresh)

  // Keyboard shortcut: 'r' to refresh
  useEffect(() => {
    function handleKeyDown(e) {
      if (e.key === 'r' && !e.ctrlKey && !e.metaKey && !e.altKey) {
        const tag = (e.target.tagName || '').toLowerCase()
        if (tag === 'input' || tag === 'textarea' || tag === 'select' || e.target.isContentEditable) return
        e.preventDefault()
        doRefresh()
        autoRefresh.markRefreshed()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [doRefresh, autoRefresh])

  // Selection state
  const [selectedHostIds, setSelectedHostIds] = useState(new Set())

  // Bulk action state
  const [bulkConfirm, setBulkConfirm] = useState(null) // { type: 'scan'|'apply'|'reboot', hostIds: [...] }
  const [bulkProgress, setBulkProgress] = useState(null) // { type, current, total }
  const bulkBusy = bulkProgress !== null

  const toast = useToastContext()

  // Compute operational state for each host
  const hostOperationalStateById = useMemo(() => {
    const state = new Map()
    hosts.forEach(h => {
      const connectivity = connectivityByHost[h.id]
      const quickConnectivityError = connectivity && connectivity.connected === false ? (connectivity.error || 'Quick SSH check failed') : ''
      const snap = scanByHost.get(h.id)
      const connection = connectionIndicator(h, connectivity, quickConnectivityError || (hostActionError?.[h.id] || ''), snap)
      const packageCount = Array.isArray(snap?.packages) ? snap.packages.length : 0
      const restartServicesCount = Array.isArray(snap?.needs_restart) ? snap.needs_restart.length : 0
      const restartNeeded = !!(snap?.needs_reboot || restartServicesCount > 0)
      const checksEnabled = h.checks_enabled !== false
      const applyAllowed = checksEnabled && !h.host_key_pending_fingerprint

      state.set(h.id, {
        connected: connection.ok,
        needsAttention: !connection.ok,
        pendingUpdates: packageCount,
        restartNeeded,
        checksEnabled,
        applyAllowed,
        needsReboot: !!snap?.needs_reboot
      })
    })
    return state
  }, [hosts, connectivityByHost, hostActionError, scanByHost])

  // Text filter
  const textFilteredHosts = useMemo(() => {
    const q = hostFilter.trim().toLowerCase()
    if (!q) return hosts
    return hosts.filter(h => [h.name, h.address, h.ssh_user].some(v => (v || '').toLowerCase().includes(q)))
  }, [hosts, hostFilter])

  // Status filter
  const statusFilteredHosts = useMemo(() => {
    if (hostStatusFilter === 'all') return textFilteredHosts
    return textFilteredHosts.filter(h => {
      const state = hostOperationalStateById.get(h.id)
      if (!state) return false
      if (hostStatusFilter === 'connected') return state.connected
      if (hostStatusFilter === 'attention') return state.needsAttention
      if (hostStatusFilter === 'updates') return state.pendingUpdates > 0
      if (hostStatusFilter === 'restart') return state.restartNeeded
      return true
    })
  }, [textFilteredHosts, hostStatusFilter, hostOperationalStateById])

  // Tag filter (OR logic)
  const filteredHosts = useMemo(() => {
    if (selectedTags.length === 0) return statusFilteredHosts
    return statusFilteredHosts.filter(h => {
      const hostTags = Array.isArray(h.tags) ? h.tags : []
      return selectedTags.some(t => hostTags.includes(t))
    })
  }, [statusFilteredHosts, selectedTags])

  // Summary
  const summary = useMemo(() => {
    return hosts.reduce((acc, h) => {
      const state = hostOperationalStateById.get(h.id)
      if (!state) return acc
      if (state.connected) acc.connected += 1
      else acc.needsAttention += 1
      acc.pendingUpdates += state.pendingUpdates
      if (state.restartNeeded) acc.restartNeededHosts += 1
      return acc
    }, { connected: 0, needsAttention: 0, pendingUpdates: 0, restartNeededHosts: 0 })
  }, [hosts, hostOperationalStateById])

  // Filter options
  const filterOptions = useMemo(() => {
    let connected = 0, attention = 0, updates = 0, restart = 0
    textFilteredHosts.forEach(h => {
      const state = hostOperationalStateById.get(h.id)
      if (!state) return
      if (state.connected) connected += 1
      if (state.needsAttention) attention += 1
      if (state.pendingUpdates > 0) updates += 1
      if (state.restartNeeded) restart += 1
    })
    return [
      { key: 'all', label: 'All', count: textFilteredHosts.length },
      { key: 'attention', label: 'Needs attention', count: attention },
      { key: 'connected', label: 'Connected', count: connected },
      { key: 'updates', label: 'Pending updates', count: updates },
      { key: 'restart', label: 'Needs restart', count: restart },
    ]
  }, [textFilteredHosts, hostOperationalStateById])

  const filtersActive = hostFilter.trim().length > 0 || hostStatusFilter !== 'all' || selectedTags.length > 0

  // Grouped hosts for group-by-tag view
  const groupedHosts = useMemo(() => {
    if (!groupByTag) return null
    const groups = new Map()
    filteredHosts.forEach(h => {
      const hostTags = Array.isArray(h.tags) && h.tags.length > 0 ? h.tags : [null]
      hostTags.forEach(tag => {
        const key = tag || '__untagged__'
        if (!groups.has(key)) groups.set(key, [])
        groups.get(key).push(h)
      })
    })
    // Sort group keys alphabetically, untagged at the end
    const sorted = [...groups.entries()].sort((a, b) => {
      if (a[0] === '__untagged__') return 1
      if (b[0] === '__untagged__') return -1
      return a[0].localeCompare(b[0])
    })
    return sorted
  }, [groupByTag, filteredHosts])

  // Selection helpers
  const filteredHostIds = useMemo(() => new Set(filteredHosts.map(h => h.id)), [filteredHosts])
  const visibleSelectedIds = useMemo(() => {
    const s = new Set()
    selectedHostIds.forEach(id => { if (filteredHostIds.has(id)) s.add(id) })
    return s
  }, [selectedHostIds, filteredHostIds])

  const allVisibleSelected = filteredHosts.length > 0 && visibleSelectedIds.size === filteredHosts.length
  const someVisibleSelected = visibleSelectedIds.size > 0

  function toggleHost(hostId) {
    setSelectedHostIds(prev => {
      const next = new Set(prev)
      if (next.has(hostId)) next.delete(hostId)
      else next.add(hostId)
      return next
    })
  }

  function toggleAllVisible() {
    if (allVisibleSelected) {
      // Deselect all visible
      setSelectedHostIds(prev => {
        const next = new Set(prev)
        filteredHosts.forEach(h => next.delete(h.id))
        return next
      })
    } else {
      // Select all visible
      setSelectedHostIds(prev => {
        const next = new Set(prev)
        filteredHosts.forEach(h => next.add(h.id))
        return next
      })
    }
  }

  function clearSelection() {
    setSelectedHostIds(new Set())
  }

  // Determine which selected hosts are eligible for each bulk action
  const bulkEligible = useMemo(() => {
    const ids = [...visibleSelectedIds]
    const scannable = ids.filter(id => {
      const st = hostOperationalStateById.get(id)
      return st?.checksEnabled
    })
    const applyable = ids.filter(id => {
      const st = hostOperationalStateById.get(id)
      return st?.applyAllowed && st?.pendingUpdates > 0
    })
    const rebootable = ids.filter(id => {
      const st = hostOperationalStateById.get(id)
      return st?.checksEnabled
    })
    return { scannable, applyable, rebootable }
  }, [visibleSelectedIds, hostOperationalStateById])

  // Build host name lookup for confirm messages
  const hostNameById = useMemo(() => {
    const m = new Map()
    hosts.forEach(h => m.set(h.id, h.name || h.address))
    return m
  }, [hosts])

  // Bulk action handlers
  const runBulk = useCallback(async (type, hostIds) => {
    setBulkConfirm(null)
    const total = hostIds.length
    if (total === 0) return

    setBulkProgress({ type, current: 0, total })
    let successCount = 0

    for (let i = 0; i < hostIds.length; i++) {
      setBulkProgress({ type, current: i + 1, total })
      try {
        if (type === 'scan') {
          // Use skipReload variant — refresh scans after each host so cards update
          // progressively as results come in, rather than all at once at the end.
          await (onScanBulk || onScan)(hostIds[i])
          if (onRefreshScans) await onRefreshScans()
        } else if (type === 'apply') {
          await onApply(hostIds[i])
        } else if (type === 'reboot') {
          await onReboot(hostIds[i])
        }
        successCount++
      } catch (e) {
        // continue to next host
      }
    }

    setBulkProgress(null)

    // Final full reload after all bulk scans complete
    if (type === 'scan' && onRefreshAll) onRefreshAll()

    const labels = { scan: 'Scanned', apply: 'Applied updates to', reboot: 'Rebooted' }
    toast.addToast({
      type: successCount === total ? 'success' : 'info',
      message: `${labels[type]} ${successCount}/${total} host${total !== 1 ? 's' : ''}`
    })
  }, [onScan, onScanBulk, onRefreshScans, onApply, onReboot, onRefreshAll, toast])

  // Confirmation labels
  function bulkConfirmConfig(type, ids) {
    const hostNames = ids.map(id => hostNameById.get(id) || id)
    const listPreview = hostNames.length <= 5
      ? hostNames.join(', ')
      : hostNames.slice(0, 4).join(', ') + ` + ${hostNames.length - 4} more`

    if (type === 'scan') return {
      title: `Scan ${ids.length} host${ids.length !== 1 ? 's' : ''}`,
      message: `Scan the following hosts for updates?\n\n${listPreview}`,
      label: 'Scan All',
      color: 'blue'
    }
    if (type === 'apply') return {
      title: `Apply updates to ${ids.length} host${ids.length !== 1 ? 's' : ''}`,
      message: `Apply pending updates to the following hosts? This will run apt dist-upgrade.\n\n${listPreview}`,
      label: 'Apply All',
      color: 'blue'
    }
    if (type === 'reboot') return {
      title: `Reboot ${ids.length} host${ids.length !== 1 ? 's' : ''}`,
      message: `Reboot the following hosts? They will be temporarily unavailable.\n\n${listPreview}`,
      label: 'Reboot All',
      color: 'red'
    }
    return { title: '', message: '', label: 'Confirm', color: 'blue' }
  }

  const confirmCfg = bulkConfirm ? bulkConfirmConfig(bulkConfirm.type, bulkConfirm.hostIds) : null

  return (
    <div>
      {/* Bulk confirm dialog */}
      <ConfirmDialog
        open={!!bulkConfirm}
        title={confirmCfg?.title || ''}
        message={confirmCfg?.message || ''}
        confirmLabel={confirmCfg?.label || 'Confirm'}
        confirmColor={confirmCfg?.color || 'blue'}
        onConfirm={() => bulkConfirm && runBulk(bulkConfirm.type, bulkConfirm.hostIds)}
        onCancel={() => setBulkConfirm(null)}
      />

      {/* Summary metrics */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-8">
        <div className="rounded-xl border border-gray-200 dark:border-zinc-800 bg-white dark:bg-zinc-900/40 px-5 py-4">
          <p className="text-xs text-gray-500 dark:text-zinc-500 font-medium uppercase tracking-wider">Connected</p>
          <p className="text-2xl font-semibold mt-1">{summary.connected}</p>
        </div>
        <div className="rounded-xl border border-gray-200 dark:border-zinc-800 bg-white dark:bg-zinc-900/40 px-5 py-4">
          <p className="text-xs text-gray-500 dark:text-zinc-500 font-medium uppercase tracking-wider">Needs attention</p>
          <p className="text-2xl font-semibold mt-1">{summary.needsAttention}</p>
        </div>
        <div className="rounded-xl border border-gray-200 dark:border-zinc-800 bg-white dark:bg-zinc-900/40 px-5 py-4">
          <p className="text-xs text-gray-500 dark:text-zinc-500 font-medium uppercase tracking-wider">Pending updates</p>
          <p className="text-2xl font-semibold mt-1">{summary.pendingUpdates}</p>
        </div>
        <div className="rounded-xl border border-gray-200 dark:border-zinc-800 bg-white dark:bg-zinc-900/40 px-5 py-4">
          <p className="text-xs text-gray-500 dark:text-zinc-500 font-medium uppercase tracking-wider">Need restart</p>
          <p className="text-2xl font-semibold mt-1">{summary.restartNeededHosts}</p>
        </div>
      </div>

      {/* Header + actions */}
      <div className="flex items-center justify-between mb-4 gap-3 flex-wrap">
        <div className="flex items-center gap-3">
          <h2 className="text-lg font-semibold">Hosts</h2>
          <span className="text-xs text-gray-500 dark:text-zinc-500">{hosts.length} total</span>
          <span className="text-[10px] text-gray-400 dark:text-zinc-600">Last refreshed: {autoRefresh.agoText}</span>
        </div>
        <div className="flex items-center gap-2">
          {bulkBusy && (
            <span className="text-xs text-blue-400 animate-pulse">
              {bulkProgress.type === 'scan' ? 'Scanning' : bulkProgress.type === 'apply' ? 'Applying' : 'Rebooting'} {bulkProgress.current}/{bulkProgress.total}…
            </span>
          )}

          {/* Auto-refresh toggle */}
          <button
            onClick={autoRefresh.toggle}
            className={`inline-flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-xs transition-colors ${
              autoRefresh.enabled
                ? 'border-emerald-700/60 text-emerald-400 hover:border-emerald-600'
                : 'border-gray-300 dark:border-zinc-700 text-gray-500 dark:text-zinc-400 hover:text-gray-700 dark:hover:text-zinc-200'
            }`}
            title={autoRefresh.enabled ? 'Auto-refresh is on (every 30s) — click to disable' : 'Enable auto-refresh (every 30s)'}
          >
            {autoRefresh.enabled && (
              <span className="relative flex h-2 w-2 flex-shrink-0">
                <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
                <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500" />
              </span>
            )}
            Auto-refresh
          </button>

          {/* Manual refresh */}
          <button
            onClick={() => { doRefresh(); autoRefresh.markRefreshed() }}
            className="rounded-lg border border-gray-300 dark:border-zinc-700 px-3 py-1.5 text-xs text-gray-500 dark:text-zinc-400 hover:text-gray-700 dark:hover:text-zinc-200 transition-colors"
            title="Refresh now (keyboard shortcut: r)"
          >
            ↻
          </button>

          {filtersActive && (
            <button
              onClick={() => { setHostFilter(''); setHostStatusFilter('all') }}
              className="rounded-lg border border-gray-300 dark:border-zinc-700 px-3 py-1.5 text-xs text-gray-500 dark:text-zinc-400 hover:text-gray-700 dark:hover:text-zinc-200 transition-colors"
            >
              Clear filters
            </button>
          )}
          <button
            onClick={() => setFiltersOpen(v => !v)}
            className="rounded-lg border border-gray-300 dark:border-zinc-700 px-3 py-1.5 text-xs text-gray-500 dark:text-zinc-400 hover:text-gray-700 dark:hover:text-zinc-200 transition-colors"
          >
            {filtersOpen ? 'Hide filters' : 'Filters'}
          </button>
          <button
            onClick={onAddHost}
            className="rounded-lg px-3 py-1.5 bg-gray-900 dark:bg-white text-white dark:text-zinc-900 text-sm font-medium hover:bg-gray-800 dark:hover:bg-zinc-200 transition-colors"
          >
            + Add host
          </button>
        </div>
      </div>

      {/* Filter bar */}
      {(filtersOpen || filtersActive) && (
        <div className="mb-4 space-y-3">
          <input
            className="w-full max-w-xs rounded-lg border border-gray-300 dark:border-zinc-700 bg-gray-100/50 dark:bg-zinc-800/50 px-3 py-2 text-sm placeholder-gray-400 dark:placeholder-zinc-500 focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
            placeholder="Search hosts…"
            value={hostFilter}
            onChange={e => setHostFilter(e.target.value)}
          />
          <div className="flex items-center gap-2 flex-wrap">
            {filterOptions.map(opt => {
              const selected = opt.key === hostStatusFilter
              return (
                <button
                  key={opt.key}
                  onClick={() => setHostStatusFilter(opt.key)}
                  className={`inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-xs transition-colors
                    ${selected
                      ? 'border-gray-400 dark:border-zinc-400 bg-gray-100 dark:bg-zinc-800 text-gray-900 dark:text-white'
                      : 'border-gray-300 dark:border-zinc-700 text-gray-500 dark:text-zinc-500 hover:text-gray-700 dark:hover:text-zinc-300 hover:border-gray-400 dark:hover:border-zinc-600'
                    }`}
                >
                  {opt.label}
                  <span className={`text-[10px] ${selected ? 'text-gray-600 dark:text-zinc-300' : 'text-gray-400 dark:text-zinc-600'}`}>{opt.count}</span>
                </button>
              )
            })}
          </div>
        </div>
      )}

      {/* Selection toolbar */}
      {filteredHosts.length > 0 && (
        <div className="flex items-center gap-3 mb-3 flex-wrap">
          <label className="flex items-center gap-2 text-xs text-gray-500 dark:text-zinc-400 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={allVisibleSelected}
              ref={el => { if (el) el.indeterminate = someVisibleSelected && !allVisibleSelected }}
              onChange={toggleAllVisible}
              className="rounded border-gray-300 dark:border-zinc-600 bg-gray-100 dark:bg-zinc-800 text-emerald-500 focus:ring-0 focus:ring-offset-0 cursor-pointer"
            />
            {allVisibleSelected ? 'Deselect all' : someVisibleSelected ? `${visibleSelectedIds.size} selected` : 'Select all'}
          </label>

          {someVisibleSelected && (
            <>
              <span className="text-gray-300 dark:text-zinc-700">|</span>
              <button
                onClick={() => setBulkConfirm({ type: 'scan', hostIds: bulkEligible.scannable })}
                disabled={bulkBusy || bulkEligible.scannable.length === 0}
                className="rounded-lg border border-gray-300 dark:border-zinc-700 px-3 py-1 text-xs text-gray-700 dark:text-zinc-300 hover:text-gray-900 dark:hover:text-white hover:border-gray-400 dark:hover:border-zinc-500 disabled:opacity-30 transition-colors"
                title={bulkEligible.scannable.length === 0 ? 'No selected hosts are eligible for scanning' : `Scan ${bulkEligible.scannable.length} host(s)`}
              >
                Scan selected ({bulkEligible.scannable.length})
              </button>
              <button
                onClick={() => setBulkConfirm({ type: 'apply', hostIds: bulkEligible.applyable })}
                disabled={bulkBusy || bulkEligible.applyable.length === 0}
                className="rounded-lg border border-blue-800/60 px-3 py-1 text-xs text-blue-400 hover:text-blue-300 hover:border-blue-700 disabled:opacity-30 transition-colors"
                title={bulkEligible.applyable.length === 0 ? 'No selected hosts have pending updates with apply policy' : `Apply updates to ${bulkEligible.applyable.length} host(s)`}
              >
                Apply selected ({bulkEligible.applyable.length})
              </button>
              <button
                onClick={() => setBulkConfirm({ type: 'reboot', hostIds: bulkEligible.rebootable })}
                disabled={bulkBusy || bulkEligible.rebootable.length === 0}
                className="rounded-lg border border-red-800/60 px-3 py-1 text-xs text-red-400 hover:text-red-300 hover:border-red-700 disabled:opacity-30 transition-colors"
                title={bulkEligible.rebootable.length === 0 ? 'No selected hosts are eligible for reboot' : `Reboot ${bulkEligible.rebootable.length} host(s)`}
              >
                Reboot selected ({bulkEligible.rebootable.length})
              </button>
              <button
                onClick={clearSelection}
                className="text-xs text-gray-500 dark:text-zinc-500 hover:text-gray-700 dark:hover:text-zinc-300 transition-colors"
              >
                Clear
              </button>
            </>
          )}
        </div>
      )}

      {/* Host list */}
      {loading && hosts.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-16 gap-3">
          <Spinner size="lg" />
          <p className="text-sm text-gray-500 dark:text-zinc-500">Loading hosts…</p>
        </div>
      ) : filteredHosts.length === 0 ? (
        <div className="rounded-xl border border-dashed border-gray-300 dark:border-zinc-700 p-8 text-center">
          {hosts.length === 0 ? (
            <>
              <p className="text-3xl mb-3">🖥️</p>
              <p className="text-gray-700 dark:text-zinc-300 mb-1">No hosts added yet</p>
              <p className="text-sm text-gray-400 dark:text-zinc-600 mb-4">Click "Add Host" to get started.</p>
            </>
          ) : (
            <>
              <p className="text-gray-600 dark:text-zinc-400 mb-1">No hosts match current filters.</p>
              <p className="text-sm text-gray-400 dark:text-zinc-600 mb-4">Try adjusting your search or filter criteria.</p>
            </>
          )}
          <button
            onClick={onAddHost}
            className="rounded-lg px-4 py-2 bg-gray-900 dark:bg-white text-white dark:text-zinc-900 text-sm font-medium hover:bg-gray-800 dark:hover:bg-zinc-200 transition-colors"
          >
            + Add host
          </button>
        </div>
      ) : (
        <div className="space-y-2">
          {filteredHosts.map(h => (
            <HostCard
              key={h.id}
              host={h}
              scan={scanByHost.get(h.id)}
              connectivity={connectivityByHost[h.id]}
              actionState={hostActionState[h.id] || {}}
              actionError={hostActionError?.[h.id] || ''}
              actionBusy={actionBusy}
              expanded={!!hostDetailsOpen?.[h.id]}
              onToggleExpand={() => onToggleDetails(h.id)}
              onScan={() => onScanStream ? onScanStream(h.id) : onScan(h.id, 'scan')}
              onApply={() => onApplyStream ? onApplyStream(h.id) : onApply(h.id, 'apply')}
              onRefreshConnectivity={() => onRefreshConnectivity(h.id)}
              onEdit={() => onEditHost(h)}
              onDelete={async () => onDeleteHost(h)}
              onUpdateHostOps={onUpdateHostOps}
              onUpdateHostKeyPolicy={onUpdateHostKeyPolicy}
              onResolveHostKeyMismatch={onResolveHostKeyMismatch}
              onUpdateHostNotificationPrefs={onUpdateHostNotificationPrefs}
              onLoadHostKeyAudit={onLoadHostKeyAudit}
              hostKeyAudit={hostKeyAuditByHost?.[h.id]}
              onRestartServices={onRestartServices}
              onReboot={onReboot}
              onShutdown={onShutdown}
              streamActive={streamHostId === h.id}
              streamMode={streamHostId === h.id ? streamMode : null}
              streamOutput={streamHostId === h.id ? streamOutput : []}
              streamPhase={streamHostId === h.id ? streamPhase : null}
              streamProgress={streamHostId === h.id ? streamProgress : null}
              streamIsStreaming={streamHostId === h.id ? streamIsStreaming : false}
              streamError={streamHostId === h.id ? streamError : null}
              streamResult={streamHostId === h.id ? streamResult : null}
              onCloseStream={onCloseStream}
              recoveryMonitor={recoveryMonitor}
              postApplyPrompt={postApplyPrompt}
              onDismissPostApplyPrompt={onDismissPostApplyPrompt}
              selected={selectedHostIds.has(h.id)}
              onToggleSelect={() => toggleHost(h.id)}
            />
          ))}
        </div>
      )}

      {error && <p className="text-sm text-red-500 dark:text-red-400 mt-4">{error}</p>}
    </div>
  )
}
