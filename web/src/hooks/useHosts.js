import { useState, useEffect, useMemo, useCallback, useRef } from 'react'
import { createAuthedFetch, apiErrorMessage } from '../api.js'
import { isConnectionFailureMessage, connectionIndicator, loadPersistedHostActionState, HOST_ACTION_STATE_STORAGE_KEY } from '../utils/status.js'
import { useActionStream } from './useStream.js'
import { useRecoveryMonitor } from './useRecoveryMonitor.js'

export function useHosts(token, clearToken) {
  const [hosts, setHosts] = useState([])
  const [scans, setScans] = useState([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [actionBusy, setActionBusy] = useState({})
  const [hostActionError, setHostActionError] = useState({})
  const [hostActionState, setHostActionState] = useState(() => loadPersistedHostActionState())
  const [connectivityByHost, setConnectivityByHost] = useState({})
  const [hostKeyAuditByHost, setHostKeyAuditByHost] = useState({})

  // Streaming support
  const stream = useActionStream()
  const [streamHostId, setStreamHostId] = useState(null)
  const [streamMode, setStreamMode] = useState(null)
  const streamCompletionHandled = useRef(false)

  // Recovery monitor
  const recoveryMonitor = useRecoveryMonitor()

  // Post-apply prompt
  const [postApplyPrompt, setPostApplyPrompt] = useState(null) // { hostId, type: 'reboot'|'restart', services?: [] }

  // Token ref for EventSource-based features
  const tokenRef = useRef(token)
  useEffect(() => { tokenRef.current = token }, [token])

  const authedFetch = useMemo(() => {
    if (!token) return null
    return createAuthedFetch(token, clearToken)
  }, [token, clearToken])

  const scanByHost = useMemo(() => {
    const m = new Map()
    scans.forEach(s => m.set(s.host_id, s))
    return m
  }, [scans])

  // Persist action state
  useEffect(() => {
    if (typeof window === 'undefined') return
    localStorage.setItem(HOST_ACTION_STATE_STORAGE_KEY, JSON.stringify(hostActionState))
  }, [hostActionState])

  // Clean up stale action state
  useEffect(() => {
    if (!token || hosts.length === 0) return
    const validHostIds = new Set(hosts.map(h => h.id))
    setHostActionState(prev => {
      let changed = false
      const next = {}
      Object.entries(prev).forEach(([hostId, state]) => {
        if (validHostIds.has(hostId)) {
          next[hostId] = state
        } else {
          changed = true
        }
      })
      return changed ? next : prev
    })
  }, [hosts, token])

  function buildConnectivityMap(hostRows, connectivityRows, fallbackError = '') {
    const byHost = (Array.isArray(connectivityRows) ? connectivityRows : []).reduce((acc, row) => {
      if (row && row.host_id) acc[row.host_id] = row
      return acc
    }, {})

    return (Array.isArray(hostRows) ? hostRows : []).reduce((acc, host) => {
      if (!host || !host.id) return acc
      const known = byHost[host.id]
      if (known) {
        acc[host.id] = known
      } else {
        acc[host.id] = {
          host_id: host.id,
          connected: null,
          checked_at: '',
          error: fallbackError || ''
        }
      }
      return acc
    }, {})
  }

  function setHostConnectivityState(hostId, connected, error = '') {
    const checkedAt = new Date().toISOString()
    setConnectivityByHost(prev => ({
      ...prev,
      [hostId]: {
        host_id: hostId,
        connected,
        checked_at: checkedAt,
        error: connected ? '' : (error || 'Host action reported a connectivity failure.')
      }
    }))
  }

  const refreshConnectivity = useCallback(async (hostId = '') => {
    if (!authedFetch) return
    const busyKey = hostId ? `${hostId}:connectivity` : 'connectivity:all'
    setActionBusy(prev => ({ ...prev, [busyKey]: true }))
    try {
      const resp = await authedFetch('/hosts/connectivity')
      if (!resp.ok) throw new Error('Failed to run host connectivity checks')
      const rows = await resp.json().catch(() => [])

      if (hostId) {
        setConnectivityByHost(prev => {
          const next = { ...prev }
          if (Array.isArray(rows)) {
            const row = rows.find(item => item && item.host_id === hostId)
            if (row) {
              next[hostId] = row
              return next
            }
          }
          next[hostId] = {
            host_id: hostId,
            connected: false,
            checked_at: new Date().toISOString(),
            error: 'Quick SSH check did not return data for this host.'
          }
          return next
        })
        return
      }

      setConnectivityByHost(prev => {
        const currentHosts = hosts
        return buildConnectivityMap(currentHosts, rows, '')
      })
    } catch (err) {
      console.warn('Connectivity refresh failed', err)
      if (hostId) {
        setConnectivityByHost(prev => ({
          ...prev,
          [hostId]: {
            host_id: hostId,
            connected: false,
            checked_at: new Date().toISOString(),
            error: 'Connectivity refresh failed.'
          }
        }))
      }
    } finally {
      setActionBusy(prev => ({ ...prev, [busyKey]: false }))
    }
  }, [authedFetch, hosts])

  const loadData = useCallback(async (opts = {}) => {
    if (!authedFetch) return
    const skipConnectivity = opts.skipConnectivity || false
    setLoading(true)
    setError('')
    try {
      const [hostsResp, scansResp] = await Promise.all([
        authedFetch('/hosts'),
        authedFetch('/scans'),
      ])

      if (!hostsResp.ok) throw new Error('Failed to load hosts')
      if (!scansResp.ok) throw new Error('Failed to load scans')

      const hostRows = await hostsResp.json()
      setHosts(hostRows)
      setScans(await scansResp.json())

      if (!skipConnectivity) {
        let connectivityRows = []
        let connectivityFallbackError = ''
        try {
          const connectivityResp = await authedFetch('/hosts/connectivity')
          if (!connectivityResp.ok) {
            throw new Error('Failed to run host connectivity checks')
          }
          connectivityRows = await connectivityResp.json().catch(() => [])
        } catch (connectivityErr) {
          connectivityFallbackError = connectivityErr?.message || 'Connectivity checks unavailable during this refresh.'
        }
        setConnectivityByHost(buildConnectivityMap(hostRows, connectivityRows, connectivityFallbackError))
      }

      return hostRows
    } catch (e) {
      setError(e.message || 'Failed to load data')
      return []
    } finally {
      setLoading(false)
    }
  }, [authedFetch])

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
        return {
          ...prev,
          [hostId]: { ...existing, [mode]: nextAction, latest: nextAction }
        }
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
        return {
          ...prev,
          [hostId]: { ...existing, [mode]: nextAction, latest: nextAction }
        }
      })
    } finally {
      await refreshConnectivity(hostId)
      setActionBusy(prev => ({ ...prev, [key]: false }))
    }
  }, [authedFetch, loadData, refreshConnectivity])

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
      port: normalizedPort
    }
    const editing = !!editingHostId
    const resp = await authedFetch(editing ? `/hosts/${editingHostId}` : '/hosts', {
      method: editing ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    })
    const data = await resp.json().catch(() => ({}))
    if (!resp.ok) {
      throw new Error(data.error || (editing ? 'Failed to update host' : 'Failed to add host'))
    }
    const createdHostId = typeof data.id === 'string' ? data.id.trim() : ''
    const updatedHostId = editing ? editingHostId : createdHostId
    await loadData()
    if (updatedHostId) {
      await refreshConnectivity(updatedHostId)
    }
  }, [authedFetch, loadData, refreshConnectivity])

  const updateHostOps = useCallback(async (host, patch) => {
    if (!authedFetch) return
    const next = {
      checks_enabled: patch.checks_enabled ?? host.checks_enabled,
    }
    setError('')
    try {
      const resp = await authedFetch(`/hosts/${host.id}/operations`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(next)
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
      host_key_pinned_fingerprint: patch.host_key_pinned_fingerprint ?? host.host_key_pinned_fingerprint ?? ''
    }
    setError('')
    try {
      const resp = await authedFetch(`/hosts/${host.id}/host-key-policy`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(next)
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
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ note: (note || '').trim().slice(0, 240) })
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
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ services })
      })
      const data = await resp.json().catch(() => ({}))
      if (resp.status === 429) {
        throw new Error(data.error || 'Rate limited — please wait before retrying.')
      }
      if (!resp.ok) {
        throw new Error(apiErrorMessage(data, 'Service restart failed'))
      }
      setHostConnectivityState(hostId, true)
      await loadData({ skipConnectivity: true })
      // Auto-trigger a scan to refresh needs_restart list
      if (tokenRef.current) {
        hostActionStream(hostId, 'scan', tokenRef.current)
      }
    } catch (e) {
      const msg = e.message || 'Service restart failed'
      setError(msg)
      setHostActionError(prev => ({ ...prev, [hostId]: msg }))
    } finally {
      await refreshConnectivity(hostId)
      setActionBusy(prev => ({ ...prev, [key]: false }))
    }
  }, [authedFetch, loadData, refreshConnectivity])

  const rebootHost = useCallback(async (hostId) => {
    if (!authedFetch) return
    const key = `${hostId}:reboot`
    setActionBusy(prev => ({ ...prev, [key]: true }))
    setError('')
    setHostActionError(prev => ({ ...prev, [hostId]: '' }))
    try {
      const resp = await authedFetch(`/hosts/${hostId}/power`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action: 'reboot' })
      })
      const data = await resp.json().catch(() => ({}))
      if (resp.status === 429) {
        throw new Error(data.error || 'Rate limited — please wait before retrying.')
      }
      if (!resp.ok) {
        throw new Error(apiErrorMessage(data, 'Reboot failed'))
      }
      // Start recovery monitor
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
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action: 'shutdown' })
      })
      const data = await resp.json().catch(() => ({}))
      if (resp.status === 429) {
        throw new Error(data.error || 'Rate limited — please wait before retrying.')
      }
      if (!resp.ok) {
        throw new Error(apiErrorMessage(data, 'Shutdown failed'))
      }
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
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(next)
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

  // Start a streaming action (scan or apply) for a host
  const hostActionStream = useCallback((hostId, mode, tokenValue) => {
    setStreamHostId(hostId)
    setStreamMode(mode)
    streamCompletionHandled.current = false
    const key = `${hostId}:${mode}`
    setActionBusy(prev => ({ ...prev, [key]: true }))
    setHostActionError(prev => ({ ...prev, [hostId]: '' }))
    setError('')
    stream.startStream(hostId, mode, tokenValue)
  }, [stream])

  // Handle stream completion side effects — runs once when streaming stops
  useEffect(() => {
    // Only act when streaming just finished and we haven't handled it yet
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
      setConnectivityByHost(prev => ({
        ...prev,
        [hostId]: { host_id: hostId, connected: true, checked_at: actionAt, error: '' }
      }))
      // Reload data once (not in a loop) — skip connectivity since we call refreshConnectivity below
      loadData({ skipConnectivity: true })

      // Post-apply: auto-trigger scan to refresh package counts and reboot status
      if (mode === 'apply' && tokenRef.current) {
        // Check if the apply result indicates needs_reboot or needs_restart
        const res = stream.result
        if (res.needs_reboot) {
          setPostApplyPrompt({ hostId, type: 'reboot' })
        } else if (Array.isArray(res.needs_restart) && res.needs_restart.length > 0) {
          setPostApplyPrompt({ hostId, type: 'restart', services: res.needs_restart })
        }
        // Schedule auto-scan after a brief delay to let data settle
        setTimeout(() => {
          if (tokenRef.current) {
            hostActionStream(hostId, 'scan', tokenRef.current)
          }
        }, 2000)
      }
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
  }, [stream.isStreaming, stream.result, stream.error, streamHostId, streamMode, loadData, refreshConnectivity])

  const closeStream = useCallback(() => {
    stream.resetStream()
    setStreamHostId(null)
    setStreamMode(null)
  }, [stream])

  // Watch recovery monitor status
  // NOTE: intentionally excludes recoveryMonitor.elapsed from deps — elapsed ticks on every
  // ping and would re-fire this effect repeatedly, causing multiple scans and UI flicker.
  // recoveryActedRef ensures post-recovery actions fire exactly once per reboot event.
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
      // Use hostAction (not hostActionStream) so the scan works regardless of
      // whether the stream panel is open or the card is collapsed
      refreshConnectivity(hostId)
      setTimeout(() => {
        hostAction(hostId, 'scan')
      }, 2000)
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
  }, [recoveryMonitor.status, recoveryMonitor.hostId, loadData, refreshConnectivity])

  // Dismiss post-apply prompt
  const dismissPostApplyPrompt = useCallback(() => {
    setPostApplyPrompt(null)
  }, [])

  const resetState = useCallback(() => {
    setHosts([])
    setScans([])
    setHostKeyAuditByHost({})
    setHostActionState({})
    setConnectivityByHost({})
    setHostActionError({})
    setPostApplyPrompt(null)
    setError('')
    recoveryMonitor.reset()
    stream.resetStream()
    setStreamHostId(null)
    setStreamMode(null)
  }, [stream, recoveryMonitor])

  return {
    hosts, scans, scanByHost, loading, error, setError,
    actionBusy, setActionBusy, hostActionError, hostActionState,
    connectivityByHost,
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
    streamMeta: stream.streamMeta,
    // Recovery monitor
    recoveryMonitor,
    // Post-apply prompt
    postApplyPrompt,
    dismissPostApplyPrompt
  }
}
