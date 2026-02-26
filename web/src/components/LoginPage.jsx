import React from 'react'

export default function LoginPage({
  setupLoading, setupStatus,
  login, setLogin, loginBusy, doLogin,
  totpRequired, cancelTotp,
  bootstrapForm, setBootstrapForm, bootstrapBusy, doBootstrap,
  bootstrapDone,
  error
}) {
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
            setupStatus.registration_enabled === false ? (
              <>
                <h2 className="font-medium text-lg mb-1">Registration disabled</h2>
                <p className="text-sm text-gray-600 dark:text-zinc-400 mt-2">Registration is disabled. Contact your administrator.</p>
              </>
            ) : (
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
            )
          ) : null}

          {/* Login form */}
          {!setupLoading && !setupStatus.bootstrap_required ? (
            <>
              {/* Step 2: TOTP code entry */}
              {totpRequired ? (
                <>
                  <h2 className="font-medium text-lg mb-1">Two-factor authentication</h2>
                  <p className="text-sm text-gray-500 dark:text-zinc-500 mb-5">
                    Enter the 6-digit code from your authenticator app, or a recovery code.
                  </p>
                  <form className="space-y-4" onSubmit={doLogin} action="/api/login" method="POST">
                    {/* Hidden fields so 1Password/password managers associate TOTP with the login item */}
                    <input type="hidden" name="username" autoComplete="username" value={login.username} readOnly />
                    <input type="hidden" name="password" autoComplete="current-password" value={login.password} readOnly />
                    <label htmlFor="one-time-code" className="sr-only">Verification code</label>
                    <input
                      id="one-time-code"
                      name="one-time-code"
                      type="tel"
                      aria-label="verification-code-input-0"
                      className="w-full rounded-lg border border-gray-300 dark:border-zinc-700 bg-gray-100 dark:bg-zinc-800/50 px-3 py-2.5 text-sm text-center font-mono tracking-widest placeholder-gray-400 dark:placeholder-zinc-500 focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
                      placeholder="000000"
                      value={login.code}
                      onChange={e => setLogin(s => ({ ...s, code: e.target.value }))}
                      autoFocus
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
                </>
              ) : (
                /* Step 1: Username + password */
                <>
                  <h2 className="font-medium text-lg mb-5">Sign in</h2>
                  {bootstrapDone ? (
                    <p className="text-sm text-emerald-600 dark:text-emerald-400 mb-4">Account created! You can now sign in.</p>
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
                      required
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
                      required
                    />
                    <button
                      type="submit"
                      disabled={loginBusy}
                      className="w-full rounded-lg px-4 py-2.5 bg-gray-900 dark:bg-white text-white dark:text-zinc-900 text-sm font-medium hover:bg-gray-800 dark:hover:bg-zinc-200 disabled:opacity-50 transition-colors"
                    >
                      {loginBusy ? 'Signing in…' : 'Sign in'}
                    </button>
                  </form>
                </>
              )}
            </>
          ) : null}

          {error ? <p className="text-sm text-red-500 dark:text-red-400 mt-4">{error}</p> : null}
        </div>
      </div>
    </div>
  )
}
