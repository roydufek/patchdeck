import React, { useState, useEffect } from 'react'
import Spinner from './Spinner.jsx'
import { useToastContext } from './Toast.jsx'

export default function SettingsPage({
  notificationSettings, setNotificationSettings,
  notificationRuntime,
  settingsBusy, onSave, onTest,
  tokens, tokensBusy, newToken, onClearNewToken, onCreateToken, onRevokeToken,
  auditRetentionDays, setAuditRetentionDays, auditBusy, onSaveAuditRetention, onExportActivity,
  error, loading
}) {
  const toast = useToastContext()
  const [tokenName, setTokenName] = useState('')
  const [showCreateForm, setShowCreateForm] = useState(false)
  const [revokeConfirm, setRevokeConfirm] = useState(null)
  const [copied, setCopied] = useState(false)
  const [retentionInput, setRetentionInput] = useState(String(auditRetentionDays ?? 30))
  const [retentionError, setRetentionError] = useState('')

  useEffect(() => {
    setRetentionInput(String(auditRetentionDays ?? 30))
  }, [auditRetentionDays])

  async function handleTest() {
    const result = await onTest()
    if (result?.ok) {
      toast.addToast({ type: 'success', message: 'Test notification sent successfully.' })
    } else if (result?.error) {
      toast.addToast({ type: 'error', message: result.error, duration: 6000 })
    }
  }

  async function handleSave(e) {
    await onSave(e)
    toast.addToast({ type: 'success', message: 'Notification settings saved.' })
  }

  async function handleCreateToken(e) {
    e.preventDefault()
    if (!tokenName.trim()) return
    const result = await onCreateToken(tokenName.trim())
    if (result) {
      setTokenName('')
      setShowCreateForm(false)
      setCopied(false)
      toast.addToast({ type: 'success', message: 'API token created.' })
    }
  }

  async function handleRevokeToken(id) {
    await onRevokeToken(id)
    setRevokeConfirm(null)
    toast.addToast({ type: 'success', message: 'API token revoked.' })
  }

  function handleCopyToken() {
    if (newToken?.token) {
      navigator.clipboard.writeText(newToken.token).then(() => {
        setCopied(true)
        setTimeout(() => setCopied(false), 2000)
      })
    }
  }

  function formatDate(dateStr) {
    if (!dateStr) return '—'
    try {
      return new Date(dateStr).toLocaleDateString('en-US', {
        year: 'numeric', month: 'short', day: 'numeric',
        hour: '2-digit', minute: '2-digit'
      })
    } catch {
      return dateStr
    }
  }

  return (
    <div>
      <div className="mb-6">
        <h2 className="text-lg font-semibold">Settings</h2>
        <p className="text-sm text-gray-500 dark:text-zinc-500 mt-0.5">Notification destination, event routing, and API tokens.</p>
      </div>

      <form className="space-y-6" onSubmit={handleSave}>
        {/* Apprise destination */}
        <div className="rounded-xl border border-gray-200 dark:border-zinc-800 bg-gray-50 dark:bg-zinc-900/40 p-5 space-y-4">
          <div>
            <label className="block text-xs text-gray-500 dark:text-zinc-500 font-medium uppercase tracking-wider mb-2">Notification destination</label>
            <div className="flex gap-3 flex-wrap">
              <input
                className="flex-1 min-w-0 rounded-lg border border-gray-300 dark:border-zinc-700 bg-white dark:bg-zinc-800/50 px-3 py-2.5 text-sm text-gray-800 dark:text-zinc-100 placeholder-gray-400 dark:placeholder-zinc-500 focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
                placeholder="Apprise URL (gotify://, discord://, mailto://, etc)"
                value={notificationSettings.apprise_url || ''}
                onChange={e => setNotificationSettings(s => ({ ...s, apprise_url: e.target.value }))}
              />
              <button
                type="submit"
                disabled={settingsBusy}
                className="rounded-lg px-4 py-2.5 bg-gray-900 dark:bg-white text-white dark:text-zinc-900 text-sm font-medium hover:bg-gray-800 dark:hover:bg-zinc-200 disabled:opacity-50 transition-colors"
              >
                {settingsBusy ? 'Saving…' : 'Save'}
              </button>
              <button
                type="button"
                onClick={handleTest}
                disabled={settingsBusy}
                className="rounded-lg px-4 py-2.5 border border-gray-300 dark:border-zinc-700 text-sm text-gray-600 dark:text-zinc-300 hover:text-gray-900 dark:hover:text-white hover:border-gray-400 dark:hover:border-zinc-500 disabled:opacity-30 transition-colors"
              >
                Send test
              </button>
            </div>
          </div>

          <div className="text-xs text-gray-500 dark:text-zinc-500">
            {notificationRuntime.available ? (
              <p>Apprise CLI ready at <span className="font-mono text-gray-600 dark:text-zinc-400">{notificationRuntime.bin_path}</span>{notificationRuntime.version ? ` · ${notificationRuntime.version}` : ''}</p>
            ) : (
              <p className="text-red-500 dark:text-red-400">Apprise CLI unavailable at <span className="font-mono">{notificationRuntime.bin_path}</span>{notificationRuntime.error ? ` · ${notificationRuntime.error}` : ''}</p>
            )}
            <p className="mt-1 text-gray-400 dark:text-zinc-600">One destination URL per instance during alpha. Use your notification backend for fan-out.</p>
          </div>
        </div>

        {/* Event toggles */}
        <div className="rounded-xl border border-gray-200 dark:border-zinc-800 bg-gray-50 dark:bg-zinc-900/40 p-5 space-y-4">
          <label className="block text-xs text-gray-500 dark:text-zinc-500 font-medium uppercase tracking-wider">Event routing</label>
          <div className="grid sm:grid-cols-2 gap-x-6 gap-y-3 text-sm text-gray-700 dark:text-zinc-300">
            <label className="flex items-center gap-2.5">
              <input
                type="checkbox"
                checked={notificationSettings.updates_available !== false}
                onChange={e => setNotificationSettings(s => ({ ...s, updates_available: e.target.checked }))}
                className="rounded border-gray-300 dark:border-zinc-600"
              />
              Updates available
            </label>
            <label className="flex items-center gap-2.5">
              <input
                type="checkbox"
                checked={notificationSettings.auto_apply_success !== false}
                onChange={e => setNotificationSettings(s => ({ ...s, auto_apply_success: e.target.checked }))}
                className="rounded border-gray-300 dark:border-zinc-600"
              />
              Auto-apply success
            </label>
            <label className="flex items-center gap-2.5">
              <input
                type="checkbox"
                checked={notificationSettings.auto_apply_failure !== false}
                onChange={e => setNotificationSettings(s => ({ ...s, auto_apply_failure: e.target.checked }))}
                className="rounded border-gray-300 dark:border-zinc-600"
              />
              Auto-apply failure
            </label>
            <label className="flex items-center gap-2.5">
              <input
                type="checkbox"
                checked={notificationSettings.scan_failure !== false}
                onChange={e => setNotificationSettings(s => ({ ...s, scan_failure: e.target.checked }))}
                className="rounded border-gray-300 dark:border-zinc-600"
              />
              Scan/connect failure
            </label>
          </div>
          <p className="text-[11px] text-gray-400 dark:text-zinc-600">
            Host key mismatch and first-contact fingerprint rejections are routed through the scan/connect failure channel.
          </p>
        </div>
      </form>

      {/* API Tokens */}
      <div className="mt-6 rounded-xl border border-gray-200 dark:border-zinc-800 bg-gray-50 dark:bg-zinc-900/40 p-5 space-y-4">
        <div className="flex items-center justify-between">
          <label className="block text-xs text-gray-500 dark:text-zinc-500 font-medium uppercase tracking-wider">API Tokens</label>
          {!showCreateForm && (
            <button
              type="button"
              onClick={() => { setShowCreateForm(true); onClearNewToken() }}
              disabled={tokensBusy}
              className="rounded-lg px-3 py-1.5 bg-gray-900 dark:bg-white text-white dark:text-zinc-900 text-xs font-medium hover:bg-gray-800 dark:hover:bg-zinc-200 disabled:opacity-50 transition-colors"
            >
              Create token
            </button>
          )}
        </div>

        {/* New token display (shown once after creation) */}
        {newToken && (
          <div className="rounded-lg border border-emerald-300 dark:border-emerald-700/50 bg-emerald-50 dark:bg-emerald-950/30 p-4 space-y-2">
            <p className="text-sm font-medium text-emerald-600 dark:text-emerald-400">Token created: {newToken.name}</p>
            <div className="flex items-center gap-2">
              <code className="flex-1 rounded bg-gray-100 dark:bg-zinc-800 px-3 py-2 text-sm font-mono text-gray-800 dark:text-zinc-200 select-all break-all">
                {newToken.token}
              </code>
              <button
                type="button"
                onClick={handleCopyToken}
                className="shrink-0 rounded-lg px-3 py-2 border border-gray-300 dark:border-zinc-700 text-xs text-gray-600 dark:text-zinc-300 hover:text-gray-900 dark:hover:text-white hover:border-gray-400 dark:hover:border-zinc-500 transition-colors"
              >
                {copied ? '✓ Copied' : 'Copy'}
              </button>
            </div>
            <p className="text-xs text-amber-600 dark:text-amber-400/80 flex items-center gap-1">
              <span>⚠</span> This token won't be shown again. Copy it now.
            </p>
            <button
              type="button"
              onClick={onClearNewToken}
              className="text-xs text-gray-500 dark:text-zinc-500 hover:text-gray-700 dark:hover:text-zinc-300 transition-colors"
            >
              Dismiss
            </button>
          </div>
        )}

        {/* Create form */}
        {showCreateForm && !newToken && (
          <form onSubmit={handleCreateToken} className="flex gap-3 items-end">
            <div className="flex-1">
              <label className="block text-xs text-gray-500 dark:text-zinc-500 mb-1">Token name</label>
              <input
                className="w-full rounded-lg border border-gray-300 dark:border-zinc-700 bg-white dark:bg-zinc-800/50 px-3 py-2 text-sm text-gray-800 dark:text-zinc-100 placeholder-gray-400 dark:placeholder-zinc-500 focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
                placeholder="e.g. CI pipeline, monitoring…"
                value={tokenName}
                onChange={e => setTokenName(e.target.value)}
                autoFocus
              />
            </div>
            <button
              type="submit"
              disabled={tokensBusy || !tokenName.trim()}
              className="rounded-lg px-4 py-2 bg-gray-900 dark:bg-white text-white dark:text-zinc-900 text-sm font-medium hover:bg-gray-800 dark:hover:bg-zinc-200 disabled:opacity-50 transition-colors"
            >
              {tokensBusy ? 'Creating…' : 'Create'}
            </button>
            <button
              type="button"
              onClick={() => { setShowCreateForm(false); setTokenName('') }}
              className="rounded-lg px-3 py-2 border border-gray-300 dark:border-zinc-700 text-sm text-gray-500 dark:text-zinc-400 hover:text-gray-900 dark:hover:text-white hover:border-gray-400 dark:hover:border-zinc-500 transition-colors"
            >
              Cancel
            </button>
          </form>
        )}

        {/* Token list */}
        {tokens.length > 0 ? (
          <div className="space-y-2">
            {tokens.map(t => (
              <div key={t.id} className="flex items-center justify-between rounded-lg border border-gray-200 dark:border-zinc-800 bg-white dark:bg-zinc-800/30 px-4 py-3">
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium text-gray-800 dark:text-zinc-200 truncate">{t.name}</span>
                    {t.revoked ? (
                      <span className="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium bg-red-50 dark:bg-red-950/50 text-red-500 dark:text-red-400 border border-red-200 dark:border-red-800/50">Revoked</span>
                    ) : (
                      <span className="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium bg-emerald-50 dark:bg-emerald-950/50 text-emerald-600 dark:text-emerald-400 border border-emerald-200 dark:border-emerald-800/50">Active</span>
                    )}
                  </div>
                  <div className="flex gap-4 mt-1 text-xs text-gray-400 dark:text-zinc-500">
                    <span>Created {formatDate(t.created_at)}</span>
                    <span>Last used {t.last_used_at ? formatDate(t.last_used_at) : 'never'}</span>
                  </div>
                </div>
                {!t.revoked && (
                  revokeConfirm === t.id ? (
                    <div className="flex items-center gap-2 shrink-0 ml-3">
                      <span className="text-xs text-gray-500 dark:text-zinc-400">Revoke?</span>
                      <button
                        type="button"
                        onClick={() => handleRevokeToken(t.id)}
                        disabled={tokensBusy}
                        className="rounded px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-500 disabled:opacity-50 transition-colors"
                      >
                        Yes
                      </button>
                      <button
                        type="button"
                        onClick={() => setRevokeConfirm(null)}
                        className="rounded px-2 py-1 text-xs border border-gray-300 dark:border-zinc-700 text-gray-500 dark:text-zinc-400 hover:text-gray-900 dark:hover:text-white transition-colors"
                      >
                        No
                      </button>
                    </div>
                  ) : (
                    <button
                      type="button"
                      onClick={() => setRevokeConfirm(t.id)}
                      disabled={tokensBusy}
                      className="shrink-0 ml-3 rounded-lg px-3 py-1.5 text-xs border border-gray-300 dark:border-zinc-700 text-gray-500 dark:text-zinc-400 hover:text-red-500 dark:hover:text-red-400 hover:border-red-300 dark:hover:border-red-700 disabled:opacity-30 transition-colors"
                    >
                      Revoke
                    </button>
                  )
                )}
              </div>
            ))}
          </div>
        ) : (
          <p className="text-sm text-gray-400 dark:text-zinc-600">No API tokens yet. Create one to use the API programmatically.</p>
        )}

        <p className="text-[11px] text-gray-400 dark:text-zinc-600">
          API tokens allow programmatic access. Use the <span className="font-mono">Authorization: Bearer pd_...</span> header. Tokens cannot be recovered after creation.
        </p>
      </div>

      {/* Audit Log Retention */}
      <div className="mt-6 rounded-xl border border-gray-200 dark:border-zinc-800 bg-gray-50 dark:bg-zinc-900/40 p-5 space-y-4">
        <label className="block text-xs text-gray-500 dark:text-zinc-500 font-medium uppercase tracking-wider">Audit Log</label>

        <div className="space-y-3">
          <div>
            <label className="block text-xs text-gray-500 dark:text-zinc-500 mb-1">Retention period (days)</label>
            <div className="flex gap-3 items-start">
              <div className="flex-1 max-w-[200px]">
                <input
                  type="number"
                  min="0"
                  className="w-full rounded-lg border border-gray-300 dark:border-zinc-700 bg-white dark:bg-zinc-800/50 px-3 py-2.5 text-sm text-gray-800 dark:text-zinc-100 placeholder-gray-400 dark:placeholder-zinc-500 focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
                  value={retentionInput}
                  onChange={e => {
                    setRetentionInput(e.target.value)
                    setRetentionError('')
                  }}
                />
                {retentionError && (
                  <p className="text-xs text-red-500 dark:text-red-400 mt-1">{retentionError}</p>
                )}
              </div>
              <button
                type="button"
                disabled={auditBusy}
                onClick={async () => {
                  const val = parseInt(retentionInput, 10)
                  if (isNaN(val) || val < 0) {
                    setRetentionError('Enter a valid number (0 or 30+)')
                    return
                  }
                  if (val !== 0 && val < 30) {
                    setRetentionError('Minimum is 30 days. Use 0 for unlimited.')
                    return
                  }
                  const result = await onSaveAuditRetention(val)
                  if (result?.ok) {
                    toast.addToast({ type: 'success', message: 'Audit retention updated.' })
                  } else if (result?.error) {
                    setRetentionError(result.error)
                  }
                }}
                className="rounded-lg px-4 py-2.5 bg-gray-900 dark:bg-white text-white dark:text-zinc-900 text-sm font-medium hover:bg-gray-800 dark:hover:bg-zinc-200 disabled:opacity-50 transition-colors"
              >
                {auditBusy ? 'Saving…' : 'Save'}
              </button>
            </div>
            <p className="text-[11px] text-gray-400 dark:text-zinc-600 mt-2">
              Minimum 30 days. Set to 0 for unlimited retention. Records older than this are automatically purged daily.
            </p>
          </div>

          <div className="pt-2 border-t border-gray-200 dark:border-zinc-800/60">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-gray-700 dark:text-zinc-300">Export activity log</p>
                <p className="text-[11px] text-gray-400 dark:text-zinc-600">Download all activity records as CSV.</p>
              </div>
              <button
                type="button"
                onClick={async () => {
                  const result = await onExportActivity()
                  if (result?.ok) {
                    toast.addToast({ type: 'success', message: 'Activity log exported.' })
                  } else if (result?.error) {
                    toast.addToast({ type: 'error', message: result.error, duration: 6000 })
                  }
                }}
                className="rounded-lg px-4 py-2 border border-gray-300 dark:border-zinc-700 text-sm text-gray-600 dark:text-zinc-300 hover:text-gray-900 dark:hover:text-white hover:border-gray-400 dark:hover:border-zinc-500 transition-colors"
              >
                ⬇ Export CSV
              </button>
            </div>
          </div>
        </div>
      </div>

      {error && <p className="text-sm text-red-500 dark:text-red-400 mt-4">{error}</p>}
    </div>
  )
}
