import { useState, useCallback, useMemo } from 'react'
import { createAuthedFetch, apiErrorMessage } from '../api.js'

export function useSettings(token, clearToken) {
  const [notificationSettings, setNotificationSettings] = useState({
    apprise_url: '',
    updates_available: true,
    auto_apply_success: true,
    auto_apply_failure: true,
    scan_failure: true
  })
  const [notificationRuntime, setNotificationRuntime] = useState({
    available: false,
    bin_path: 'apprise',
    version: '',
    error: ''
  })
  const [settingsBusy, setSettingsBusy] = useState(false)
  const [error, setError] = useState('')

  // API Tokens state
  const [tokens, setTokens] = useState([])
  const [tokensBusy, setTokensBusy] = useState(false)
  const [newToken, setNewToken] = useState(null) // { id, name, token, created_at } shown once

  // Audit retention state
  const [auditRetentionDays, setAuditRetentionDays] = useState(30)
  const [auditBusy, setAuditBusy] = useState(false)

  const authedFetch = useMemo(() => {
    if (!token) return null
    return createAuthedFetch(token, clearToken)
  }, [token, clearToken])

  const loadSettings = useCallback(async () => {
    if (!authedFetch) return
    try {
      const [notifResp, notifRuntimeResp, auditResp] = await Promise.all([
        authedFetch('/settings/notifications'),
        authedFetch('/settings/notifications/runtime'),
        authedFetch('/settings/audit')
      ])
      if (!notifResp.ok) throw new Error('Failed to load notification settings')
      if (!notifRuntimeResp.ok) throw new Error('Failed to load notification runtime status')

      const notif = await notifResp.json()
      const runtime = await notifRuntimeResp.json()
      setNotificationRuntime({
        available: !!runtime.available,
        bin_path: runtime.bin_path || 'apprise',
        version: runtime.version || '',
        error: runtime.error || ''
      })
      setNotificationSettings({
        apprise_url: notif.apprise_url || '',
        updates_available: notif.updates_available !== false,
        auto_apply_success: notif.auto_apply_success !== false,
        auto_apply_failure: notif.auto_apply_failure !== false,
        scan_failure: notif.scan_failure !== false
      })
      if (auditResp.ok) {
        const audit = await auditResp.json()
        setAuditRetentionDays(audit.retention_days ?? 30)
      }
    } catch (e) {
      setError(e.message || 'Failed to load settings')
    }
  }, [authedFetch])

  const saveNotificationSettings = useCallback(async (e) => {
    e.preventDefault()
    if (!authedFetch) return
    setSettingsBusy(true)
    setError('')
    try {
      const resp = await authedFetch('/settings/notifications', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          apprise_url: (notificationSettings.apprise_url || '').trim(),
          updates_available: notificationSettings.updates_available !== false,
          auto_apply_success: notificationSettings.auto_apply_success !== false,
          auto_apply_failure: notificationSettings.auto_apply_failure !== false,
          scan_failure: notificationSettings.scan_failure !== false
        })
      })
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok) throw new Error(apiErrorMessage(data, 'Failed to update notification settings'))
      await loadSettings()
    } catch (e) {
      setError(e.message || 'Failed to update notification settings')
    } finally {
      setSettingsBusy(false)
    }
  }, [authedFetch, notificationSettings, loadSettings])

  const sendNotificationTest = useCallback(async () => {
    if (!authedFetch) return
    setSettingsBusy(true)
    setError('')
    try {
      if (!notificationRuntime.available) {
        throw new Error(`Notifications runtime unavailable: ${notificationRuntime.error || 'Apprise is not ready on this host.'}`)
      }
      const resp = await authedFetch('/settings/notifications/test', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ apprise_url: (notificationSettings.apprise_url || '').trim() })
      })
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok) throw new Error(apiErrorMessage(data, 'Failed to send test notification'))
      return { ok: true }
    } catch (e) {
      setError(e.message || 'Failed to send test notification')
      return { ok: false, error: e.message || 'Failed to send test notification' }
    } finally {
      setSettingsBusy(false)
    }
  }, [authedFetch, notificationRuntime, notificationSettings])

  // --- API Token functions ---

  const loadTokens = useCallback(async () => {
    if (!authedFetch) return
    try {
      const resp = await authedFetch('/settings/tokens')
      if (!resp.ok) throw new Error('Failed to load API tokens')
      const data = await resp.json()
      setTokens(data || [])
    } catch (e) {
      setError(e.message || 'Failed to load API tokens')
    }
  }, [authedFetch])

  const createToken = useCallback(async (name) => {
    if (!authedFetch) return
    setTokensBusy(true)
    setError('')
    try {
      const resp = await authedFetch('/settings/tokens', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name })
      })
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok) throw new Error(apiErrorMessage(data, 'Failed to create API token'))
      setNewToken(data)
      await loadTokens()
      return data
    } catch (e) {
      setError(e.message || 'Failed to create API token')
      return null
    } finally {
      setTokensBusy(false)
    }
  }, [authedFetch, loadTokens])

  const revokeToken = useCallback(async (id) => {
    if (!authedFetch) return
    setTokensBusy(true)
    setError('')
    try {
      const resp = await authedFetch(`/settings/tokens/${id}`, { method: 'DELETE' })
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok) throw new Error(apiErrorMessage(data, 'Failed to revoke API token'))
      await loadTokens()
    } catch (e) {
      setError(e.message || 'Failed to revoke API token')
    } finally {
      setTokensBusy(false)
    }
  }, [authedFetch, loadTokens])

  const clearNewToken = useCallback(() => setNewToken(null), [])

  // --- Audit retention functions ---

  const saveAuditRetention = useCallback(async (days) => {
    if (!authedFetch) return
    setAuditBusy(true)
    setError('')
    try {
      const resp = await authedFetch('/settings/audit', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ retention_days: days })
      })
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok) throw new Error(apiErrorMessage(data, 'Failed to save audit retention'))
      setAuditRetentionDays(days)
      return { ok: true }
    } catch (e) {
      setError(e.message || 'Failed to save audit retention')
      return { ok: false, error: e.message }
    } finally {
      setAuditBusy(false)
    }
  }, [authedFetch])

  const exportActivityCSV = useCallback(async () => {
    if (!authedFetch) return
    try {
      const resp = await authedFetch('/activity/export')
      if (!resp.ok) throw new Error('Failed to export activity')
      const blob = await resp.blob()
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `patchdeck-activity-${new Date().toISOString().slice(0, 10)}.csv`
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
      URL.revokeObjectURL(url)
      return { ok: true }
    } catch (e) {
      setError(e.message || 'Failed to export activity')
      return { ok: false, error: e.message }
    }
  }, [authedFetch])

  return {
    notificationSettings, setNotificationSettings,
    notificationRuntime,
    settingsBusy, error, setError,
    loadSettings, saveNotificationSettings, sendNotificationTest,
    // API Tokens
    tokens, tokensBusy, newToken, clearNewToken,
    loadTokens, createToken, revokeToken,
    // Audit retention
    auditRetentionDays, setAuditRetentionDays, auditBusy,
    saveAuditRetention, exportActivityCSV
  }
}
