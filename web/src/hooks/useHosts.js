import { useState, useEffect, useMemo, useCallback, useRef } from 'react'
import { useQuery, useQueries, useQueryClient, keepPreviousData } from '@tanstack/react-query'
import { createAuthedFetch, apiErrorMessage } from '../api.js'
import { loadPersistedHostActionState, HOST_ACTION_STATE_STORAGE_KEY } from '../utils/status.js'
import { useActionStream } from './useStream.js'
import { useRecoveryMonitor } from './useRecoveryMonitor.js'

// Build a connectivity map keyed by host id, filling unknown hosts with a neutral
// "no result yet" entry so the UI can distinguish "not checked" from "failed".
function buildConnectivityMap(hostRows, connectivityRows) {
  const byHost = (Array.isArray(connectivityRows) ? connectivityRows : []).reduce((acc, row) => {
    if (row && row.host_id) acc[row.host_id] = row
    return acc
  }, {})
  return (Array.isArray(hostRows) ? hostRows : []).reduce((acc, host) => {
    if (!host || !host.id) return acc
    acc[host.id] = byHost[host.id] || { host_id: host.id, connected: null, checked_at: '', error: '' }
    return acc
  }, {})
}

export function useHosts(token, clearToken) {
  const queryClient = useQueryClient()
  const [error, setError] = useState('')
  const [actionBusy, setActionBusy] = useState({})
  const [hostActionError, setHostActionError] = useState({})
  const [hostActionState, setHostActionState] = useState(() => loadPersistedHostActionState())
  const [hostKeyAuditByHost, setHostKeyAuditByHost] = useState({})

  // Streaming support
  const stream = useActionStream()
  const [streamHostId, setStreamHostId] = useState(null)
  const [streamMode, setStreamMode] = useState(null)
  const streamCompletionHandled = useRef(false)
  // Tracks deferred follow-up scans (post-apply / post-recovery) so they can be
  // cancelled on logout/reset — otherwise they fire against a torn-down session
  // and surface a spurious "session expired".
  const followupTimers = useRef([])
  const scheduleFollowup = useCallback((fn, ms) => {
    const id = setTimeout(() => {
      followupTimers.current = followupTimers.current.filter(t => t !== id)
      fn()
    }, ms)
    followupTimers.current.push(id)
  }, [])

  // Recovery monitor
  const recoveryMonitor = useRecoveryMonitor()

  // Post-apply prompt
  const [postApplyPrompt, setPostApplyPrompt] = useState(null)

  const tokenRef = useRef(token)
  useEffect(() => { tokenRef.current = token }, [token])

  const authedFetch = useMemo(() => {
    if (!token) return null
    return createAuthedFetch(token, clearToken)
  }, [token, clearToken])

  // ── Server state via TanStack Query — single source of truth per resource.
  // keepPreviousData means a refetch keeps showing the last good data instead of
  // flashing empty, and the cache dedupes concurrent reads. Connectivity is NOT
  // polled on an interval (the old per-render live-SSH checks were the main source
  // of status-color flicker); it refreshes on mount, after actions, and on demand.
  const enabled = !!authedFetch
  const hostsQuery = useQuery({
    queryKey: ['hosts'],
    enabled,
    refetchInterval: 30000,
    placeholderData: keepPreviousData,
    queryFn: async () => {
      const r = await authedFetch('/hosts')
      if (!r.ok) throw new Error('Failed to load hosts')
      return r.json()
    },
  })
  const scansQuery = useQuery({
    queryKey: ['scans'],
    enabled,
    refetchInterval: 30000,
    placeholderData: keepPreviousData,
    queryFn: async () => {
      const r = await authedFetch('/scans')
      if (!r.ok) throw new Error('Failed to load scans')
      return r.json()
    },
  })
  // Connectivity is checked PER HOST so each status dot resolves independently — a slow
  // or rebooting host (which burns the full timeout) no longer blocks every other host's
  // result behind one response. Each host owns its ['connectivity', hostId] cache entry.
  // Not polled on an interval (it refreshes on mount, after actions, and on demand).
  const connectivityHostIds = (hostsQuery.data ?? []).map(h => h.id)
  const connectivityResults = useQueries({
    queries: connectivityHostIds.map(hostId => ({
      queryKey: ['connectivity', hostId],
      enabled,
      staleTime: Infinity,
      queryFn: async () => {
        const r = await authedFetch(`/hosts/${hostId}/connectivity`)
        if (!r.ok) throw new Error('Failed to run host connectivity check')
        return r.json().catch(() => null)
      },
    })),
  })

  const hosts = hostsQuery.data ?? []
  const scans = scansQuery.data ?? []
  const loading = hostsQuery.isFetching || scansQuery.isFetching

  const scanByHost = useMemo(() => {
    const m = new Map()
    scans.forEach(s => m.set(s.host_id, s))
    return m
  }, [scans])

  // Derive the connectivity map from the per-host query results + current hosts. The
  // signature keeps the memo (and the map's identity) stable across renders unless a
  // host's connectivity actually changed.
  const connectivityRows = connectivityResults.map(q => q.data).filter(Boolean)
  const connectivityChecking = connectivityResults.some(q => q.isFetching)
  const connectivitySig = connectivityRows
    .map(r => `${r.host_id}:${r.connected}:${r.uptime_seconds || 0}:${r.error || ''}`)
    .join('|')
  const connectivityByHost = useMemo(
    () => buildConnectivityMap(hosts, connectivityRows),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [hosts, connectivitySig]
  )

  // Surface query load failures as the banner error, and clear that banner once the
  // queries recover — but only the query-error messages, so action errors are preserved.
  useEffect(() => {
    if (hostsQuery.isError) setError('Failed to load hosts')
    else if (scansQuery.isError) setError('Failed to load scans')
    else setError(prev => (prev === 'Failed to load hosts' || prev === 'Failed to load scans') ? '' : prev)
  }, [hostsQuery.isError, scansQuery.isError])

  // Persist optimistic action state.
  useEffect(() => {
    if (typeof window === 'undefined') return
    localStorage.setItem(HOST_ACTION_STATE_STORAGE_KEY, JSON.stringify(hostActionState))
  }, [hostActionState])

  // Drop action state for hosts that no longer exist.
  useEffect(() => {
    if (!token || hosts.length === 0) return
    const validHostIds = new Set(hosts.map(h => h.id))
    setHostActionState(prev => {
      let changed = false
      const next = {}
      Object.entries(prev).forEach(([hostId, state]) => {
        if (validHostIds.has(hostId)) next[hostId] = state
        else changed = true
      })
      return changed ? next : prev
    })
  }, [hosts, token])

  // Optimistically upsert a single host's connectivity into its per-host cache entry.
  const setHostConnectivityState = useCallback((hostId, connected, errMsg = '') => {
    queryClient.setQueryData(['connectivity', hostId], {
      host_id: hostId,
      connected,
      checked_at: new Date().toISOString(),
      error: connected ? '' : (errMsg || 'Host action reported a connectivity failure.'),
    })
  }, [queryClient])

  // Refetch connectivity for one host (['connectivity', id]) or, with no id, all hosts
  // (the ['connectivity'] prefix matches every per-host query).
  const refreshConnectivity = useCallback(async (hostId = '') => {
    if (!enabled) return
    const busyKey = hostId ? `${hostId}:connectivity` : 'connectivity:all'
    setActionBusy(prev => ({ ...prev, [busyKey]: true }))
    try {
      await queryClient.refetchQueries({ queryKey: hostId ? ['connectivity', hostId] : ['connectivity'] })
    } catch (err) {
      console.warn('Connectivity refresh failed', err)
    } finally {
      setActionBusy(prev => ({ ...prev, [busyKey]: false }))
    }
  }, [enabled, queryClient])

  // loadData = refetch the server-state queries. Returns the latest hosts rows.
  const loadData = useCallback(async (opts = {}) => {
    if (!enabled) return []
    const tasks = [
      queryClient.refetchQueries({ queryKey: ['hosts'] }),
      queryClient.refetchQueries({ queryKey: ['scans'] }),
    ]
    if (!opts.skipConnectivity) {
      tasks.push(queryClient.refetchQueries({ queryKey: ['connectivity'] }))
    }
    await Promise.all(tasks)
    return queryClient.getQueryData(['hosts']) ?? []
  }, [enabled, queryClient])

  const hostAction = useCallback(async (hostId, mode, { skipReload = false } = {}) => {
    if (!authedFetch) return
    const key = `${hostId}:${mode}`
    setActionBusy(prev => ({ ...prev, [key]: true }))
    setError('')
    setHostActionError(prev => ({ ...prev, [hostId]: '' }))
    try {
      const resp = await authedFetch(`/hosts/${hostId}/${mode}`, { method: 'POST' })
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok) {
        throw new Error(apiErrorMessage(data, `${mode} failed`))
      }
      let summary = mode === 'scan' ? 'Scan completed successfully.' : 'Apply completed successfully.'
      if (mode === 'scan') {
        const count = Array.isArray(data.packages) ? data.packages.length : null
        summary = `Scan completed${count === null ? '' : ` • ${count} upgradable package(s)`}`
      } else if (mode === 'apply') {
        const changed = Number.isFinite(data.changed_packages) ? data.changed_packages : null
        summary = `Apply completed${changed === null ? '' : ` • ${changed} package(s) changed`}`
      }
      const actionAt = new Date().toISOString()
      setHostActionState(prev => {
        const existing = prev[hostId] || {}
        const nextAction = { mode, ok: true, summary, at: actionAt }
        return { ...prev, [hostId]: { ...existing, [mode]: nextAction, latest: nextAction } }
      })
      setHostConnectivityState(hostId, true)
      if (!skipReload) await loadData({ skipConnectivity: true })
    } catch (e) {
      const msg = e.message || `${mode} failed`
      setError(msg)
      setHostActionError(prev => ({ ...prev, [hostId]: msg }))
      setHostConnectivityState(hostId, false, msg)
      const actionAt = new Date().toISOString()
      setHostActionState(prev => {
        const existing = prev[hostId] || {}
        const nextAction = { mode, ok: false, summary: msg, at: actionAt }
        return { ...prev, [hostId]: { ...existing, [mode]: nextAction, latest: nextAction } }
      })
    } finally {
      await refreshConnectivity(hostId)
      setActionBusy(prev => ({ ...prev, [key]: false }))
    }
  }, [authedFetch, loadData, refreshConnectivity, setHostConnectivityState])

  const deleteHost = useCallback(async (host) => {
    if (!authedFetch) return { ok: false }
    const key = `host:delete:${host.id}`
    setActionBusy(prev => ({ ...prev, [key]: true }))
    setError('')
    try {
      const resp = await authedFetch(`/hosts/${host.id}`, { method: 'DELETE' })
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok) throw new Error(data.error || 'Failed to delete host')
      await loadData()
      return { ok: true }
    } catch (e) {
      setError(e.message || 'Failed to delete host')
      return { ok: false, error: e.message }
    } finally {
      setActionBusy(prev => ({ ...prev, [key]: false }))
    }
  }, [authedFetch, loadData])

  const createHost = useCallback(async (hostForm, editingHostId) => {
    if (!authedFetch) return
    const normalizedPort = Number(hostForm.port) || 22
    if (normalizedPort < 1 || normalizedPort > 65535) {
      throw new Error('Port must be between 1 and 65535')
    }
    const hostKeyTrustMode = (hostForm.host_key_trust_mode || 'tofu').trim()
    const hostKeyPinnedFingerprint = (hostForm.host_key_pinned_fingerprint || '').trim()
    if (hostKeyTrustMode === 'pinned' && !hostKeyPinnedFingerprint) {
      throw new Error('Pinned trust mode requires a host key fingerprint')
    }
    const payload = {
      ...hostForm,
      name: hostForm.name.trim(),
      address: hostForm.address.trim(),
      ssh_user: hostForm.ssh_user.trim(),
      auth_type: hostForm.auth_type.trim(),
      host_key_trust_mode: hostKeyTrustMode,
      host_key_pinned_fingerprint: hostKeyPinnedFingerprint,
      port: normalizedPort,
    }
    const editing = !!editingHostId
    const resp = await authedFetch(editing ? `/hosts/${editingHostId}` : '/hosts', {
      method: editing ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    })
    const data = await resp.json().catch(() => ({}))
    if (!resp.ok) {
      throw new Error(data.error || (editing ? 'Failed to update host' : 'Failed to add host'))
    }
    const createdHostId = typeof data.id === 'string' ? data.id.trim() : ''
    const updatedHostId = editing ? editingHostId : createdHostId
    await loadData()
    if (updatedHostId) await refreshConnectivity(updatedHostId)
  }, [authedFetch, loadData, refreshConnectivity])

  const updateHostOps = useCallback(async (host, patch) => {
    if (!authedFetch) return
    const next = { checks_enabled: patch.checks_enabled ?? host.checks_enabled }
    setError('')
    try {
      const resp = await authedFetch(`/hosts/${host.id}/operations`, {
        method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(next),
      })
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok) throw new Error(data.error || 'Failed to update host operational controls')
      await loadData()
      await refreshConnectivity(host.id)
    } catch (e) {
      setError(e.message || 'Failed to update host operational controls')
    }
  }, [authedFetch, loadData, refreshConnectivity])

  const updateHostKeyPolicy = useCallback(async (host, patch) => {
    if (!authedFetch) return
    const next = {
      host_key_required: true,
      host_key_trust_mode: patch.host_key_trust_mode ?? host.host_key_trust_mode ?? 'tofu',
      host_key_pinned_fingerprint: patch.host_key_pinned_fingerprint ?? host.host_key_pinned_fingerprint ?? '',
    }
    setError('')
    try {
      const resp = await authedFetch(`/hosts/${host.id}/host-key-policy`, {
        method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(next),
      })
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok) throw new Error(apiErrorMessage(data, 'Failed to update host key policy'))
      await loadData()
      await refreshConnectivity(host.id)
    } catch (e) {
      setError(e.message || 'Failed to update host key policy')
    }
  }, [authedFetch, loadData, refreshConnectivity])

  const resolveHostKeyMismatch = useCallback(async (host, action, note = '') => {
    if (!authedFetch) return
    setError('')
    try {
      const resp = await authedFetch(`/hosts/${host.id}/host-key/${action}`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ note: (note || '').trim().slice(0, 240) }),
      })
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok) throw new Error(apiErrorMessage(data, `Failed to ${action} host key fingerprint`))
      await loadData()
      await refreshConnectivity(host.id)
      await loadHostKeyAudit(host.id, true)
      return { ok: true }
    } catch (e) {
      setError(e.message || `Failed to ${action} host key fingerprint`)
      return { ok: false, error: e.message }
    }
  }, [authedFetch, loadData, refreshConnectivity])

  const restartServices = useCallback(async (hostId, services) => {
    if (!authedFetch || !Array.isArray(services) || services.length === 0) return
    const key = `${hostId}:restart-services`
    setActionBusy(prev => ({ ...prev, [key]: true }))
    setError('')
    setHostActionError(prev => ({ ...prev, [hostId]: '' }))
    try {
      const resp = await authedFetch(`/hosts/${hostId}/restart-services`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ services }),
      })
      const data = await resp.json().catch(() => ({}))
      if (resp.status === 429) throw new Error(data.error || 'Rate limited — please wait before retrying.')
      if (!resp.ok) throw new Error(apiErrorMessage(data, 'Service restart failed'))
      setHostConnectivityState(hostId, true)
      await loadData({ skipConnectivity: true })
      if (tokenRef.current) hostActionStream(hostId, 'scan')
    } catch (e) {
      const msg = e.message || 'Service restart failed'
      setError(msg)
      setHostActionError(prev => ({ ...prev, [hostId]: msg }))
    } finally {
      await refreshConnectivity(hostId)
      setActionBusy(prev => ({ ...prev, [key]: false }))
    }
  }, [authedFetch, loadData, refreshConnectivity, setHostConnectivityState])

  const rebootHost = useCallback(async (hostId) => {
    if (!authedFetch) return
    const key = `${hostId}:reboot`
    setActionBusy(prev => ({ ...prev, [key]: true }))
    setError('')
    setHostActionError(prev => ({ ...prev, [hostId]: '' }))
    try {
      const resp = await authedFetch(`/hosts/${hostId}/power`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ action: 'reboot' }),
      })
      const data = await resp.json().catch(() => ({}))
      if (resp.status === 429) throw new Error(data.error || 'Rate limited — please wait before retrying.')
      if (!resp.ok) throw new Error(apiErrorMessage(data, 'Reboot failed'))
      recoveryMonitor.startMonitor(hostId, tokenRef.current, 180)
      const actionAt = new Date().toISOString()
      setHostActionState(prev => {
        const existing = prev[hostId] || {}
        const nextAction = { mode: 'reboot', ok: true, summary: 'Reboot initiated — waiting for recovery…', at: actionAt }
        return { ...prev, [hostId]: { ...existing, reboot: nextAction, latest: nextAction } }
      })
      await loadData({ skipConnectivity: true })
    } catch (e) {
      const msg = e.message || 'Reboot failed'
      setError(msg)
      setHostActionError(prev => ({ ...prev, [hostId]: msg }))
    } finally {
      setActionBusy(prev => ({ ...prev, [key]: false }))
    }
  }, [authedFetch, loadData, recoveryMonitor])

  const shutdownHost = useCallback(async (hostId) => {
    if (!authedFetch) return
    const key = `${hostId}:shutdown`
    setActionBusy(prev => ({ ...prev, [key]: true }))
    setError('')
    setHostActionError(prev => ({ ...prev, [hostId]: '' }))
    try {
      const resp = await authedFetch(`/hosts/${hostId}/power`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ action: 'shutdown' }),
      })
      const data = await resp.json().catch(() => ({}))
      if (resp.status === 429) throw new Error(data.error || 'Rate limited — please wait before retrying.')
      if (!resp.ok) throw new Error(apiErrorMessage(data, 'Shutdown failed'))
      await loadData({ skipConnectivity: true })
    } catch (e) {
      const msg = e.message || 'Shutdown failed'
      setError(msg)
      setHostActionError(prev => ({ ...prev, [hostId]: msg }))
    } finally {
      await refreshConnectivity(hostId)
      setActionBusy(prev => ({ ...prev, [key]: false }))
    }
  }, [authedFetch, loadData, refreshConnectivity])

  const updateHostNotificationPrefs = useCallback(async (host, patch) => {
    if (!authedFetch) return
    const next = { ...(host.notification_prefs || {}), ...patch }
    setError('')
    try {
      const resp = await authedFetch(`/hosts/${host.id}/notifications`, {
        method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(next),
      })
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok) throw new Error(data.error || 'Failed to update host notification preferences')
      await loadData()
    } catch (e) {
      setError(e.message || 'Failed to update host notification preferences')
    }
  }, [authedFetch, loadData])

  const loadHostKeyAudit = useCallback(async (hostId, force = false) => {
    if (!hostId || !authedFetch) return
    if (!force && hostKeyAuditByHost[hostId]) return
    try {
      const resp = await authedFetch(`/hosts/${hostId}/host-key-audit?limit=12`)
      const data = await resp.json().catch(() => [])
      if (!resp.ok) throw new Error(data.error || 'Failed to load host key audit trail')
      setHostKeyAuditByHost(prev => ({ ...prev, [hostId]: Array.isArray(data) ? data : [] }))
    } catch (e) {
      setError(e.message || 'Failed to load host key audit trail')
    }
  }, [authedFetch, hostKeyAuditByHost])

  // Start a streaming action (scan or apply) for a host. Auth is via cookie now,
  // so no token is passed to the stream.
  const hostActionStream = useCallback((hostId, mode) => {
    setStreamHostId(hostId)
    setStreamMode(mode)
    streamCompletionHandled.current = false
    const key = `${hostId}:${mode}`
    setActionBusy(prev => ({ ...prev, [key]: true }))
    setHostActionError(prev => ({ ...prev, [hostId]: '' }))
    setError('')
    stream.startStream(hostId, mode)
  }, [stream])

  // Handle stream completion side effects — runs once when streaming stops.
  useEffect(() => {
    if (stream.isStreaming || !streamHostId || !streamMode) return
    if (streamCompletionHandled.current) return
    streamCompletionHandled.current = true

    const hostId = streamHostId
    const mode = streamMode
    const key = `${hostId}:${mode}`
    const actionAt = new Date().toISOString()

    if (stream.result && !stream.error) {
      let summary = mode === 'scan' ? 'Scan completed successfully.' : 'Apply completed successfully.'
      if (mode === 'scan') {
        const count = Array.isArray(stream.result.packages) ? stream.result.packages.length : null
        summary = `Scan completed${count === null ? '' : ` • ${count} upgradable package(s)`}`
      } else if (mode === 'apply') {
        const changed = Number.isFinite(stream.result.changed_packages) ? stream.result.changed_packages : null
        summary = `Apply completed${changed === null ? '' : ` • ${changed} package(s) changed`}`
      }
      setHostActionState(prev => {
        const existing = prev[hostId] || {}
        const nextAction = { mode, ok: true, summary, at: actionAt }
        return { ...prev, [hostId]: { ...existing, [mode]: nextAction, latest: nextAction } }
      })
      setHostConnectivityState(hostId, true)
      loadData({ skipConnectivity: true })

      if (mode === 'apply' && tokenRef.current) {
        const res = stream.result
        if (res.needs_reboot) {
          setPostApplyPrompt({ hostId, type: 'reboot' })
        } else if (Array.isArray(res.needs_restart) && res.needs_restart.length > 0) {
          setPostApplyPrompt({ hostId, type: 'restart', services: res.needs_restart })
        }
        scheduleFollowup(() => {
          if (tokenRef.current) hostActionStream(hostId, 'scan')
        }, 2000)
      }
    } else if (stream.interrupted) {
      // Apply's SSH connection dropped mid-run (a service restart or a reboot) — NOT a
      // failure. Record it as "verifying" and let the recovery monitor confirm the host
      // comes back, then re-scan (handled by the recovery-outcome effect below).
      const changed = Number.isFinite(stream.interrupted.changed_so_far) ? stream.interrupted.changed_so_far : null
      const summary = `Apply interrupted — host restarting${changed === null ? '' : ` after ${changed} package(s)`}; verifying…`
      setHostActionError(prev => ({ ...prev, [hostId]: '' }))
      setHostActionState(prev => {
        const existing = prev[hostId] || {}
        const nextAction = { mode, ok: true, summary, at: actionAt }
        return { ...prev, [hostId]: { ...existing, [mode]: nextAction, latest: nextAction } }
      })
      if (tokenRef.current) recoveryMonitor.startMonitor(hostId, tokenRef.current, 180)
    } else if (stream.error) {
      const msg = stream.error
      setHostActionError(prev => ({ ...prev, [hostId]: msg }))
      setHostActionState(prev => {
        const existing = prev[hostId] || {}
        const nextAction = { mode, ok: false, summary: msg, at: actionAt }
        return { ...prev, [hostId]: { ...existing, [mode]: nextAction, latest: nextAction } }
      })
    }

    setActionBusy(prev => ({ ...prev, [key]: false }))
    refreshConnectivity(hostId)
  }, [stream.isStreaming, stream.result, stream.error, stream.interrupted, streamHostId, streamMode, loadData, refreshConnectivity, setHostConnectivityState, hostActionStream, scheduleFollowup, recoveryMonitor])

  const closeStream = useCallback(() => {
    stream.resetStream()
    setStreamHostId(null)
    setStreamMode(null)
  }, [stream])

  // React to recovery-monitor outcome (exactly once per reboot event).
  const recoveryActedRef = useRef(false)
  useEffect(() => {
    if (recoveryMonitor.status === 'idle') {
      recoveryActedRef.current = false
      return
    }
    if (recoveryActedRef.current) return
    if (recoveryMonitor.status === 'recovered' && recoveryMonitor.hostId) {
      recoveryActedRef.current = true
      const hostId = recoveryMonitor.hostId
      const elapsed = recoveryMonitor.elapsed
      const actionAt = new Date().toISOString()
      setHostActionState(prev => {
        const existing = prev[hostId] || {}
        const nextAction = { mode: 'reboot', ok: true, summary: `Host recovered after ${elapsed}s`, at: actionAt }
        return { ...prev, [hostId]: { ...existing, reboot: nextAction, latest: nextAction } }
      })
      setHostActionError(prev => ({ ...prev, [hostId]: '' }))
      refreshConnectivity(hostId)
      scheduleFollowup(() => { hostAction(hostId, 'scan') }, 2000)
    } else if (recoveryMonitor.status === 'timeout' && recoveryMonitor.hostId) {
      recoveryActedRef.current = true
      const hostId = recoveryMonitor.hostId
      const actionAt = new Date().toISOString()
      setHostActionState(prev => {
        const existing = prev[hostId] || {}
        const nextAction = { mode: 'reboot', ok: false, summary: 'Host hasn\'t responded after 3 minutes — may need a manual check', at: actionAt }
        return { ...prev, [hostId]: { ...existing, reboot: nextAction, latest: nextAction } }
      })
      refreshConnectivity(hostId)
    } else if (recoveryMonitor.status === 'error' && recoveryMonitor.hostId) {
      recoveryActedRef.current = true
      const hostId = recoveryMonitor.hostId
      const actionAt = new Date().toISOString()
      setHostActionState(prev => {
        const existing = prev[hostId] || {}
        const nextAction = { mode: 'reboot', ok: false, summary: 'Recovery monitoring failed', at: actionAt }
        return { ...prev, [hostId]: { ...existing, reboot: nextAction, latest: nextAction } }
      })
    }
  }, [recoveryMonitor.status, recoveryMonitor.hostId, refreshConnectivity, hostAction, scheduleFollowup])

  const dismissPostApplyPrompt = useCallback(() => setPostApplyPrompt(null), [])

  const resetState = useCallback(() => {
    queryClient.removeQueries({ queryKey: ['hosts'] })
    queryClient.removeQueries({ queryKey: ['scans'] })
    queryClient.removeQueries({ queryKey: ['connectivity'] })
    followupTimers.current.forEach(clearTimeout)
    followupTimers.current = []
    setHostKeyAuditByHost({})
    setHostActionState({})
    setHostActionError({})
    setPostApplyPrompt(null)
    setError('')
    recoveryMonitor.reset()
    stream.resetStream()
    setStreamHostId(null)
    setStreamMode(null)
  }, [stream, recoveryMonitor, queryClient])

  // Cancel any pending follow-up scans on unmount.
  useEffect(() => () => followupTimers.current.forEach(clearTimeout), [])

  return {
    hosts, scans, scanByHost, loading, error, setError,
    actionBusy, setActionBusy, hostActionError, hostActionState,
    connectivityByHost,
    connectivityChecking,
    hostKeyAuditByHost,
    loadData, hostAction, deleteHost, createHost,
    refreshConnectivity, updateHostOps, updateHostKeyPolicy,
    resolveHostKeyMismatch, restartServices, rebootHost, shutdownHost,
    updateHostNotificationPrefs,
    loadHostKeyAudit, resetState,
    // Streaming
    hostActionStream, closeStream,
    streamHostId, streamMode,
    streamOutput: stream.output,
    streamPhase: stream.phase,
    streamProgress: stream.progress,
    streamIsStreaming: stream.isStreaming,
    streamError: stream.error,
    streamResult: stream.result,
    streamInterrupted: stream.interrupted,
    streamMeta: stream.streamMeta,
    // Recovery monitor
    recoveryMonitor,
    // Post-apply prompt
    postApplyPrompt,
    dismissPostApplyPrompt,
  }
}
