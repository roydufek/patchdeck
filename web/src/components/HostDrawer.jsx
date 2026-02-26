import React, { useEffect, useRef, useState } from 'react'

export default function HostDrawer({
  open, editingHostId, hostForm, setHostForm,
  onSubmit, onClose, hostBusy, error
}) {
  const nameInputRef = useRef(null)
  const drawerRef = useRef(null)
  const [validationErrors, setValidationErrors] = useState({})

  const hostFormPinnedMode = (hostForm.host_key_trust_mode || 'tofu') === 'pinned'
  const hostFormPinnedFingerprint = (hostForm.host_key_pinned_fingerprint || '').trim()
  const hostFormPinnedFingerprintMissing = hostFormPinnedMode && !hostFormPinnedFingerprint

  // Clear validation errors when form changes (only if errors exist)
  useEffect(() => {
    setValidationErrors(prev => Object.keys(prev).length > 0 ? {} : prev)
  }, [hostForm.name, hostForm.address, hostForm.ssh_user, hostForm.port])

  function handleSubmit(e) {
    e.preventDefault()
    const errors = {}
    if (!hostForm.name.trim()) errors.name = 'Name is required'
    if (!hostForm.address.trim()) errors.address = 'Address is required'
    if (!hostForm.ssh_user.trim()) errors.ssh_user = 'SSH user is required'
    const port = Number(hostForm.port)
    if (!port || port < 1 || port > 65535) errors.port = 'Port must be between 1 and 65535'
    if (hostForm.auth_type === 'password' && !editingHostId && !hostForm.password.trim()) {
      errors.password = 'Password is required'
    }
    if (hostForm.auth_type === 'key' && !editingHostId && !hostForm.private_key_pem.trim()) {
      errors.private_key_pem = 'Private key is required'
    }
    if (Object.keys(errors).length > 0) {
      setValidationErrors(errors)
      return
    }
    setValidationErrors({})
    onSubmit(e)
  }

  const onCloseRef = useRef(onClose)
  useEffect(() => { onCloseRef.current = onClose }, [onClose])

  useEffect(() => {
    if (!open) return

    const originalOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'

    const onKeyDown = (event) => {
      if (event.key === 'Escape') {
        onCloseRef.current()
        return
      }
      if (event.key !== 'Tab') return
      const container = drawerRef.current
      if (!container) return
      const focusable = Array.from(container.querySelectorAll('button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'))
        .filter(el => !el.hasAttribute('disabled') && el.getAttribute('aria-hidden') !== 'true')
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first.focus()
      }
    }

    window.addEventListener('keydown', onKeyDown)

    const focusTimer = window.setTimeout(() => {
      nameInputRef.current?.focus()
    }, 0)

    return () => {
      window.clearTimeout(focusTimer)
      window.removeEventListener('keydown', onKeyDown)
      document.body.style.overflow = originalOverflow
    }
  }, [open])

  if (!open) return null

  return (
    <div
      className="fixed inset-0 z-40 bg-black/60 backdrop-blur-sm"
      onClick={e => { if (e.target === e.currentTarget) onClose() }}
      role="presentation"
    >
      <div
        ref={drawerRef}
        className="ml-auto h-full w-full max-w-xl bg-gray-50 dark:bg-zinc-950 border-l border-gray-200 dark:border-zinc-800 shadow-2xl flex flex-col"
        role="dialog"
        aria-modal="true"
        aria-label={editingHostId ? 'Edit host' : 'Add host'}
      >
        {/* Header */}
        <div className="flex items-center justify-between border-b border-gray-200 dark:border-zinc-800 px-6 py-4">
          <div>
            <h3 className="font-semibold text-base">{editingHostId ? 'Edit host' : 'Add host'}</h3>
            <p className="text-sm text-gray-500 dark:text-zinc-500 mt-0.5">
              {editingHostId ? 'Update connection details.' : 'Register a new host for scanning.'}
            </p>
          </div>
          <button onClick={onClose} className="text-gray-400 dark:text-zinc-500 hover:text-gray-600 dark:hover:text-zinc-300 text-sm transition-colors">✕</button>
        </div>

        {/* Form */}
        <form className="flex-1 overflow-y-auto px-6 py-5 space-y-4" onSubmit={handleSubmit}>
          <div className="grid sm:grid-cols-2 gap-3">
            <div>
              <input
                ref={nameInputRef}
                className={`w-full rounded-lg border ${validationErrors.name ? 'border-red-500' : 'border-gray-300 dark:border-zinc-700'} bg-white dark:bg-zinc-900 px-3 py-2.5 text-sm text-gray-800 dark:text-zinc-100 placeholder-gray-400 dark:placeholder-zinc-500 font-sans focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors`}
                placeholder="Name"
                value={hostForm.name}
                onChange={e => setHostForm(s => ({ ...s, name: e.target.value }))}
              />
              {validationErrors.name && <p className="text-xs text-red-400 mt-1">{validationErrors.name}</p>}
            </div>
            <div>
              <input
                className={`w-full rounded-lg border ${validationErrors.address ? 'border-red-500' : 'border-gray-300 dark:border-zinc-700'} bg-white dark:bg-zinc-900 px-3 py-2.5 text-sm text-gray-800 dark:text-zinc-100 placeholder-gray-400 dark:placeholder-zinc-500 font-sans focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors`}
                placeholder="Address"
                value={hostForm.address}
                onChange={e => setHostForm(s => ({ ...s, address: e.target.value }))}
              />
              {validationErrors.address && <p className="text-xs text-red-400 mt-1">{validationErrors.address}</p>}
            </div>
            <div>
              <input
                type="number"
                className={`w-full rounded-lg border ${validationErrors.port ? 'border-red-500' : 'border-gray-300 dark:border-zinc-700'} bg-white dark:bg-zinc-900 px-3 py-2.5 text-sm text-gray-800 dark:text-zinc-100 placeholder-gray-400 dark:placeholder-zinc-500 font-sans focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors`}
                placeholder="Port"
                value={hostForm.port}
                onChange={e => setHostForm(s => ({ ...s, port: e.target.value }))}
                min="1" max="65535"
              />
              {validationErrors.port && <p className="text-xs text-red-400 mt-1">{validationErrors.port}</p>}
            </div>
            <div>
              <input
                className={`w-full rounded-lg border ${validationErrors.ssh_user ? 'border-red-500' : 'border-gray-300 dark:border-zinc-700'} bg-white dark:bg-zinc-900 px-3 py-2.5 text-sm text-gray-800 dark:text-zinc-100 placeholder-gray-400 dark:placeholder-zinc-500 font-sans focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors`}
                placeholder="SSH user"
                value={hostForm.ssh_user}
                onChange={e => setHostForm(s => ({ ...s, ssh_user: e.target.value }))}
              />
              {validationErrors.ssh_user && <p className="text-xs text-red-400 mt-1">{validationErrors.ssh_user}</p>}
            </div>
          </div>

          <div className="grid sm:grid-cols-2 gap-3">
            <div>
              <label className="block text-xs text-gray-500 dark:text-zinc-500 mb-1">Auth type</label>
              <select
                className="w-full rounded-lg border border-gray-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-3 py-2.5 text-sm text-gray-800 dark:text-zinc-100 font-sans focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
                value={hostForm.auth_type}
                onChange={e => setHostForm(s => ({ ...s, auth_type: e.target.value }))}
              >
                <option value="key">SSH key</option>
                <option value="password">Password</option>
              </select>
            </div>
            <input
              type="password"
              className="rounded-lg border border-gray-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-3 py-2.5 text-sm text-gray-800 dark:text-zinc-100 placeholder-gray-400 dark:placeholder-zinc-500 font-sans focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors self-end"
              placeholder="Sudo password (optional)"
              value={hostForm.sudo_password}
              onChange={e => setHostForm(s => ({ ...s, sudo_password: e.target.value }))}
            />
          </div>

          {hostForm.auth_type === 'password' ? (
            <input
              type="password"
              className="w-full rounded-lg border border-gray-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-3 py-2.5 text-sm text-gray-800 dark:text-zinc-100 placeholder-gray-400 dark:placeholder-zinc-500 font-sans focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
              placeholder={editingHostId ? "SSH password (blank = keep current)" : "SSH password"}
              value={hostForm.password}
              onChange={e => setHostForm(s => ({ ...s, password: e.target.value }))}
              required={!editingHostId}
            />
          ) : (
            <>
              <textarea
                className="w-full rounded-lg border border-gray-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-3 py-2.5 text-sm text-gray-800 dark:text-zinc-100 placeholder-gray-400 dark:placeholder-zinc-500 font-sans focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors font-mono"
                placeholder={editingHostId ? "Private key PEM (blank = keep current)" : "Private key PEM"}
                value={hostForm.private_key_pem}
                onChange={e => setHostForm(s => ({ ...s, private_key_pem: e.target.value }))}
                rows={5}
                required={!editingHostId}
              />
              <input
                type="password"
                className="w-full rounded-lg border border-gray-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-3 py-2.5 text-sm text-gray-800 dark:text-zinc-100 placeholder-gray-400 dark:placeholder-zinc-500 font-sans focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
                placeholder="Key passphrase (optional)"
                value={hostForm.passphrase}
                onChange={e => setHostForm(s => ({ ...s, passphrase: e.target.value }))}
              />
            </>
          )}

          <div className="border-t border-gray-200 dark:border-zinc-800 pt-4 space-y-3">
            <p className="text-xs text-gray-500 dark:text-zinc-500">SSH host key verification is mandatory during alpha.</p>
            <div className="grid sm:grid-cols-2 gap-3">
              <div>
                <label className="block text-xs text-gray-500 dark:text-zinc-500 mb-1">Host key trust mode</label>
                <select
                  className="w-full rounded-lg border border-gray-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-3 py-2.5 text-sm text-gray-800 dark:text-zinc-100 font-sans focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
                  value={hostForm.host_key_trust_mode || 'tofu'}
                  onChange={e => setHostForm(s => ({ ...s, host_key_trust_mode: e.target.value }))}
                >
                  <option value="tofu">TOFU (trust on first use)</option>
                  <option value="pinned">Pinned (manual fingerprint)</option>
                </select>
              </div>
              <div>
                <label className="block text-xs text-gray-500 dark:text-zinc-500 mb-1">Pinned fingerprint</label>
                <input
                  className={`w-full rounded-lg border bg-white dark:bg-zinc-900 px-3 py-2.5 text-sm text-gray-800 dark:text-zinc-100 placeholder-gray-400 dark:placeholder-zinc-500 font-sans focus:outline-none transition-colors ${hostFormPinnedFingerprintMissing ? 'border-amber-500' : 'border-gray-300 dark:border-zinc-700 focus:border-gray-400 dark:focus:border-zinc-500'}`}
                  placeholder="Required for pinned mode"
                  value={hostForm.host_key_pinned_fingerprint || ''}
                  onChange={e => setHostForm(s => ({ ...s, host_key_pinned_fingerprint: e.target.value }))}
                />
                {hostFormPinnedMode && (
                  <p className={`text-[11px] mt-1 ${hostFormPinnedFingerprintMissing ? 'text-amber-500' : 'text-gray-400 dark:text-zinc-600'}`}>
                    {hostFormPinnedFingerprintMissing
                      ? 'A fingerprint is required when using Pinned mode.'
                      : 'The fingerprint will be saved exactly as entered.'}
                  </p>
                )}
                {!hostFormPinnedMode && (
                  <p className="text-[11px] mt-1 text-gray-400 dark:text-zinc-600">TOFU automatically records the host key on first connection.</p>
                )}
              </div>
            </div>
          </div>

          {/* Tags */}
          <div>
            <label className="block text-xs text-gray-500 dark:text-zinc-500 mb-1">Tags</label>
            <input
              className="w-full rounded-lg border border-gray-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-3 py-2.5 text-sm text-gray-800 dark:text-zinc-100 placeholder-gray-400 dark:placeholder-zinc-500 font-sans focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
              placeholder="e.g. prod, web, us-east (comma-separated)"
              value={hostForm.tags_input || ''}
              onChange={e => setHostForm(s => ({ ...s, tags_input: e.target.value }))}
            />
            {(() => {
              const tags = (hostForm.tags_input || '').split(',').map(t => t.trim()).filter(Boolean)
              if (tags.length === 0) return null
              return (
                <div className="flex flex-wrap gap-1.5 mt-2">
                  {tags.map((tag, i) => (
                    <span
                      key={i}
                      className="inline-flex items-center rounded-full bg-gray-100 dark:bg-zinc-800 border border-gray-200 dark:border-zinc-700 px-2 py-0.5 text-[11px] text-gray-700 dark:text-zinc-300"
                    >
                      {tag}
                      <button
                        type="button"
                        onClick={() => {
                          const next = tags.filter((_, j) => j !== i).join(', ')
                          setHostForm(s => ({ ...s, tags_input: next }))
                        }}
                        className="ml-1 text-gray-400 dark:text-zinc-500 hover:text-gray-600 dark:hover:text-zinc-300 transition-colors"
                      >
                        ×
                      </button>
                    </span>
                  ))}
                </div>
              )
            })()}
          </div>

          {/* Submit bar */}
          <div className="sticky bottom-0 -mx-6 mt-4 border-t border-gray-200 dark:border-zinc-800 bg-gray-50/95 dark:bg-zinc-950/95 backdrop-blur px-6 py-4 flex items-center gap-3">
            <button
              type="submit"
              disabled={hostBusy || hostFormPinnedFingerprintMissing}
              className="rounded-lg px-4 py-2.5 bg-emerald-600 text-white text-sm font-medium hover:bg-emerald-500 disabled:opacity-50 transition-colors"
            >
              {hostBusy
                ? (editingHostId ? 'Saving…' : 'Adding…')
                : (editingHostId ? 'Save host' : 'Add host')}
            </button>
            <button
              type="button"
              onClick={onClose}
              disabled={hostBusy}
              className="rounded-lg px-4 py-2.5 bg-gray-100 dark:bg-zinc-800 border border-gray-200 dark:border-zinc-700 text-sm text-gray-700 dark:text-zinc-300 hover:text-gray-900 dark:hover:text-zinc-200 hover:bg-gray-200 dark:hover:bg-zinc-700 disabled:opacity-50 transition-colors"
            >
              Cancel
            </button>
          </div>
        </form>

        {error && <p className="px-6 pb-4 text-sm text-red-500 dark:text-red-400">{error}</p>}
      </div>
    </div>
  )
}
