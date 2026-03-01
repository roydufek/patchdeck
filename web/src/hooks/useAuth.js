import { useState, useEffect, useCallback } from 'react'
import { API } from '../api.js'

export function useAuth() {
  const [token, setToken] = useState(localStorage.getItem('token') || '')
  const [setupStatus, setSetupStatus] = useState({
    bootstrap_required: false,
    supported_roles: ['admin'],
    registration_enabled: true
  })
  const [setupLoading, setSetupLoading] = useState(true)
  const [error, setError] = useState('')

  const [login, setLogin] = useState({ username: '', password: '', code: '' })
  const [loginBusy, setLoginBusy] = useState(false)
  const [totpRequired, setTotpRequired] = useState(false)

  const [bootstrapForm, setBootstrapForm] = useState({
    username: '', password: '', confirm_password: ''
  })
  const [bootstrapBusy, setBootstrapBusy] = useState(false)
  const [bootstrapDone, setBootstrapDone] = useState(false)

  useEffect(() => {
    if (token) {
      setSetupLoading(false)
      return
    }
    let active = true
    setSetupLoading(true)
    fetch(`${API}/setup`)
      .then(resp => resp.json().catch(() => ({})))
      .then(data => {
        if (!active) return
        setSetupStatus({
          bootstrap_required: !!data.bootstrap_required,
          supported_roles: Array.isArray(data.supported_roles) && data.supported_roles.length ? data.supported_roles : ['admin'],
          registration_enabled: data.registration_enabled !== false
        })
      })
      .catch(() => {
        if (!active) return
        setSetupStatus({ bootstrap_required: false, supported_roles: ['admin'], registration_enabled: true })
      })
      .finally(() => {
        if (!active) return
        setSetupLoading(false)
      })
    return () => { active = false }
  }, [token])

  const doLogin = useCallback(async (e) => {
    e.preventDefault()
    setLoginBusy(true)
    setError('')
    try {
      let resp
      try {
        resp = await fetch(`${API}/login`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            username: login.username,
            password: login.password,
            code: login.code || ''
          })
        })
      } catch (err) {
        throw new Error('Unable to reach Patchdeck server')
      }
      const data = await resp.json().catch(() => ({}))

      // Server says TOTP is required — show the code step
      if (resp.status === 403 && data.totp_required) {
        setTotpRequired(true)
        return
      }

      if (!resp.ok || !data.token) {
        throw new Error(data.error || 'Login failed')
      }
      setToken(data.token)
      localStorage.setItem('token', data.token)
      setLogin(s => ({ ...s, password: '', code: '' }))
      setTotpRequired(false)
      setBootstrapDone(false)
    } catch (e) {
      setError(e.message || 'Login failed')
    } finally {
      setLoginBusy(false)
    }
  }, [login])

  const cancelTotp = useCallback(() => {
    setTotpRequired(false)
    setLogin(s => ({ ...s, code: '' }))
    setError('')
  }, [])

  const doBootstrap = useCallback(async (e) => {
    e.preventDefault()
    setBootstrapBusy(true)
    setError('')
    try {
      if (bootstrapForm.password.length < 12) {
        throw new Error('Password must be at least 12 characters')
      }
      if (bootstrapForm.password !== bootstrapForm.confirm_password) {
        throw new Error('Passwords do not match')
      }
      let resp
      try {
        resp = await fetch(`${API}/bootstrap`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            username: bootstrapForm.username.trim(),
            password: bootstrapForm.password
          })
        })
      } catch (err) {
        throw new Error('Unable to reach Patchdeck server')
      }
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok) {
        throw new Error(data.error || 'Bootstrap failed')
      }
      setBootstrapDone(true)
      setSetupStatus(s => ({ ...s, bootstrap_required: false }))
      setLogin(s => ({ ...s, username: bootstrapForm.username.trim(), code: '' }))
      setBootstrapForm({ username: '', password: '', confirm_password: '' })
    } catch (err) {
      setError(err.message || 'Bootstrap failed')
    } finally {
      setBootstrapBusy(false)
    }
  }, [bootstrapForm])

  const logout = useCallback(() => {
    setToken('')
    localStorage.removeItem('token')
    localStorage.removeItem('patchdeck.hostActionState.v3')
    localStorage.removeItem('patchdeck.hostActionState.v2')
  }, [])

  const clearToken = useCallback(() => {
    setToken('')
    setError('Your session expired. Please log in again.')
    localStorage.removeItem('token')
  }, [])

  return {
    token, setToken,
    setupStatus, setupLoading,
    error, setError,
    login, setLogin, loginBusy, doLogin,
    totpRequired, cancelTotp,
    bootstrapForm, setBootstrapForm, bootstrapBusy, doBootstrap,
    bootstrapDone,
    logout, clearToken
  }
}
