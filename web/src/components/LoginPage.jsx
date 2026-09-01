import React, { useEffect, useRef, useState } from 'react'

function ssoErrorMessage(code) {
  switch (code) {
    case 'sso_disabled': return 'Single sign-on is not enabled.'
    case 'sso_unavailable': return 'Could not reach the SSO provider. Try again shortly.'
    case 'sso_denied': return 'Sign-in was cancelled or denied.'
    case 'sso_expired': return 'The sign-in request expired. Please try again.'
    case 'sso_forbidden': return 'Your account is not allowed to access Patchdeck.'
    case 'sso_no_admin': return 'No administrator account exists yet. Complete first-run setup.'
    default: return 'Single sign-on failed. Please try again.'
  }
}

export default function LoginPage({
  setupLoading, setupStatus,
  login, setLogin, loginBusy, doLogin,
  totpRequired, cancelTotp,
  bootstrapForm, setBootstrapForm, bootstrapBusy, doBootstrap,
  bootstrapDone,
  error
}) {
  const otpRef = useRef(null)
  const [sso, setSso] = useState({ enabled: false, label: 'Sign in with SSO' })
  const [ssoError, setSsoError] = useState('')

  // Focus the TOTP field when it becomes visible
  useEffect(() => {
    if (totpRequired && otpRef.current) {
      // Small delay to let 1Password detect the field before we focus
      const t = setTimeout(() => otpRef.current?.focus(), 100)
      return () => clearTimeout(t)
    }
  }, [totpRequired])

  // Discover whether SSO is enabled (to show the button) and surface any sso_error the
  // OIDC callback bounced back with, then scrub it from the URL.
  useEffect(() => {
    let alive = true
    fetch('/api/auth/oidc/status', { credentials: 'same-origin' })
      .then(r => (r.ok ? r.json() : null))
      .then(d => { if (alive && d) setSso({ enabled: !!d.enabled, label: d.label || 'Sign in with SSO' }) })
      .catch(() => {})
    try {
      const params = new URLSearchParams(window.location.search)
      const code = params.get('sso_error')
      if (code) {
        setSsoError(ssoErrorMessage(code))
        params.delete('sso_error')
        const qs = params.toString()
        window.history.replaceState({}, '', window.location.pathname + (qs ? '?' + qs : ''))
      }
    } catch { /* ignore */ }
    return () => { alive = false }
  }, [])
  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50 dark:bg-zinc-950 text-gray-800 dark:text-zinc-100 p-6">
      <div className="w-full max-w-sm">
        <div className="flex items-center justify-between mb-8">
          <div className="flex items-center gap-3">
            <img src="/logo-32.png" alt="Patchdeck" className="w-8 h-8" />
            <h1 className="text-2xl font-semibold tracking-tight text-gray-900 dark:text-white">Patchdeck</h1>
          </div>
        </div>

        <div className="rounded-xl border border-gray-200 dark:border-zinc-800 bg-white dark:bg-zinc-900/50 p-6">
          {setupLoading ? (
            <p className="text-sm text-gray-500 dark:text-zinc-500">Checking setup…</p>
          ) : null}

          {/* Bootstrap / First-run setup */}
          {!setupLoading && setupStatus.bootstrap_required ? (
            <>
              <h2 className="font-medium text-lg mb-1">First-run setup</h2>
              <p className="text-sm text-gray-500 dark:text-zinc-500 mb-5">Create the initial administrator account.</p>
              <form className="space-y-4" onSubmit={doBootstrap}>
                <input
                  className="w-full rounded-lg border border-gray-300 dark:border-zinc-700 bg-gray-100 dark:bg-zinc-800/50 px-3 py-2.5 text-sm placeholder-gray-400 dark:placeholder-zinc-500 focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
                  placeholder="Admin username"
                  value={bootstrapForm.username}
                  onChange={e => setBootstrapForm(s => ({ ...s, username: e.target.value }))}
                  required
                />
                <input
                  type="password"
                  className="w-full rounded-lg border border-gray-300 dark:border-zinc-700 bg-gray-100 dark:bg-zinc-800/50 px-3 py-2.5 text-sm placeholder-gray-400 dark:placeholder-zinc-500 focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
                  placeholder="Password (12+ characters)"
                  value={bootstrapForm.password}
                  onChange={e => setBootstrapForm(s => ({ ...s, password: e.target.value }))}
                  required
                />
                <input
                  type="password"
                  className="w-full rounded-lg border border-gray-300 dark:border-zinc-700 bg-gray-100 dark:bg-zinc-800/50 px-3 py-2.5 text-sm placeholder-gray-400 dark:placeholder-zinc-500 focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
                  placeholder="Confirm password"
                  value={bootstrapForm.confirm_password}
                  onChange={e => setBootstrapForm(s => ({ ...s, confirm_password: e.target.value }))}
                  required
                />
                <button
                  type="submit"
                  disabled={bootstrapBusy}
                  className="w-full rounded-lg px-4 py-2.5 bg-gray-900 dark:bg-white text-white dark:text-zinc-900 text-sm font-medium hover:bg-gray-800 dark:hover:bg-zinc-200 disabled:opacity-50 transition-colors"
                >
                  {bootstrapBusy ? 'Creating admin…' : 'Create admin and continue'}
                </button>
              </form>
            </>
          ) : null}

          {/* Login form */}
          {!setupLoading && !setupStatus.bootstrap_required ? (
            <>
              {/* Step 1: Username + password */}
              <div className={totpRequired ? 'hidden' : ''}>
                <h2 className="font-medium text-lg mb-5">Sign in</h2>
                {bootstrapDone ? (
                  <p className="text-sm text-emerald-600 dark:text-emerald-400 mb-4">Account created! You can now sign in.</p>
                ) : null}
                {ssoError ? (
                  <p className="text-sm text-red-500 dark:text-red-400 mb-4">{ssoError}</p>
                ) : null}
                <form className="space-y-4" onSubmit={doLogin}>
                  <input
                    id="username"
                    name="username"
                    className="w-full rounded-lg border border-gray-300 dark:border-zinc-700 bg-gray-100 dark:bg-zinc-800/50 px-3 py-2.5 text-sm placeholder-gray-400 dark:placeholder-zinc-500 focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
                    placeholder="Username"
                    value={login.username}
                    onChange={e => setLogin(s => ({ ...s, username: e.target.value }))}
                    autoComplete="username"
                    required={!totpRequired}
                  />
                  <input
                    id="password"
                    name="password"
                    type="password"
                    className="w-full rounded-lg border border-gray-300 dark:border-zinc-700 bg-gray-100 dark:bg-zinc-800/50 px-3 py-2.5 text-sm placeholder-gray-400 dark:placeholder-zinc-500 focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
                    placeholder="Password"
                    value={login.password}
                    onChange={e => setLogin(s => ({ ...s, password: e.target.value }))}
                    autoComplete="current-password"
                    required={!totpRequired}
                  />
                  <button
                    type="submit"
                    disabled={loginBusy}
                    className="w-full rounded-lg px-4 py-2.5 bg-gray-900 dark:bg-white text-white dark:text-zinc-900 text-sm font-medium hover:bg-gray-800 dark:hover:bg-zinc-200 disabled:opacity-50 transition-colors"
                  >
                    {loginBusy ? 'Signing in…' : 'Sign in'}
                  </button>
                </form>
                {sso.enabled ? (
                  <>
                    <div className="flex items-center gap-3 my-4">
                      <div className="flex-1 h-px bg-gray-200 dark:bg-zinc-800" />
                      <span className="text-xs text-gray-400 dark:text-zinc-600">or</span>
                      <div className="flex-1 h-px bg-gray-200 dark:bg-zinc-800" />
                    </div>
                    <button
                      type="button"
                      onClick={() => { window.location.href = '/auth/oidc/login' }}
                      className="w-full rounded-lg px-4 py-2.5 border border-gray-300 dark:border-zinc-700 text-sm font-medium text-gray-700 dark:text-zinc-200 hover:border-gray-400 dark:hover:border-zinc-500 hover:bg-gray-50 dark:hover:bg-zinc-800/50 transition-colors"
                    >
                      {sso.label}
                    </button>
                  </>
                ) : null}
              </div>

              {/* Step 2: TOTP code entry — always in DOM for password manager detection */}
              <div className={totpRequired ? '' : 'hidden'}>
                <h2 className="font-medium text-lg mb-1">Two-factor authentication</h2>
                <p className="text-sm text-gray-500 dark:text-zinc-500 mb-5">
                  Enter the 6-digit code from your authenticator app, or a recovery code.
                </p>
                <form className="space-y-4" onSubmit={doLogin} action="/api/login" method="POST">
                  <input type="hidden" name="username" autoComplete="username" value={login.username} readOnly />
                  <input type="hidden" name="password" autoComplete="current-password" value={login.password} readOnly />
                  <label htmlFor="one-time-code" className="sr-only">Verification code</label>
                  <input
                    ref={otpRef}
                    id="one-time-code"
                    name="one-time-code"
                    type="text"
                    aria-label="verification-code-input-0"
                    className="w-full rounded-lg border border-gray-300 dark:border-zinc-700 bg-gray-100 dark:bg-zinc-800/50 px-3 py-2.5 text-sm text-center font-mono tracking-widest placeholder-gray-400 dark:placeholder-zinc-500 focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
                    placeholder="Enter code"
                    value={login.code}
                    onChange={e => setLogin(s => ({ ...s, code: e.target.value }))}
                    autoComplete="one-time-code"
                    inputMode="numeric"
                    maxLength={19}
                  />
                  <button
                    type="submit"
                    disabled={loginBusy || !login.code.trim()}
                    className="w-full rounded-lg px-4 py-2.5 bg-gray-900 dark:bg-white text-white dark:text-zinc-900 text-sm font-medium hover:bg-gray-800 dark:hover:bg-zinc-200 disabled:opacity-50 transition-colors"
                  >
                    {loginBusy ? 'Verifying…' : 'Verify'}
                  </button>
                  <button
                    type="button"
                    onClick={cancelTotp}
                    className="w-full text-center text-xs text-gray-500 dark:text-zinc-500 hover:text-gray-700 dark:hover:text-zinc-300 transition-colors"
                  >
                    ← Back to sign in
                  </button>
                </form>
              </div>
            </>
          ) : null}

          {error ? <p className="text-sm text-red-500 dark:text-red-400 mt-4">{error}</p> : null}
        </div>
      </div>
    </div>
  )
}
