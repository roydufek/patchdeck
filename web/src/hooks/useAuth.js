import { useState, useEffect, useCallback } from 'react'
import { API } from '../api.js'

export function useAuth() {
  const [token, setToken] = useState(localStorage.getItem('token') || '')
  const [setupStatus, setSetupStatus] = useState({
    bootstrap_required: false,
    supported_roles: ['admin'],
    bootstrap_roles: ['admin'],
    totp_optional: true,
    registration_enabled: true
  })
  const [setupLoading, setSetupLoading] = useState(true)
  const [error, setError] = useState('')

  const [login, setLogin] = useState({ username: '', password: '', code: '' })
  const [loginBusy, setLoginBusy] = useState(false)

  const [bootstrapForm, setBootstrapForm] = useState({
    username: '', password: '', confirm_password: '',
    role: 'admin', enable_totp: true
  })
  const [bootstrapBusy, setBootstrapBusy] = useState(false)
  const [bootstrapResult, setBootstrapResult] = useState({ otpauth: '', totp_enabled: false })

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
          bootstrap_roles: Array.isArray(data.bootstrap_roles) && data.bootstrap_roles.length ? data.bootstrap_roles : ['admin'],
          totp_optional: data.totp_optional !== false,
          registration_enabled: data.registration_enabled !== false
        })
      })
      .catch(() => {
        if (!active) return
        setSetupStatus({ bootstrap_required: false, supported_roles: ['admin'], bootstrap_roles: ['admin'], totp_optional: true, registration_enabled: true })
      })
      .finally(() => {
        if (!active) return
        setSetupLoading(false)
      })
    return () => { active = false }
  }, [token])

  useEffect(() => {
    const roles = Array.isArray(setupStatus.bootstrap_roles) && setupStatus.bootstrap_roles.length ? setupStatus.bootstrap_roles : ['admin']
    setBootstrapForm(s => ({ ...s, role: roles.includes(s.role) ? s.role : roles[0] }))
  }, [setupStatus.bootstrap_roles])

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
          body: JSON.stringify(login)
        })
      } catch (err) {
        throw new Error('Unable to reach Patchdeck server')
      }
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok || !data.token) {
        if (resp.status === 401) {
          throw new Error('Invalid username or password')
        }
        throw new Error(data.error || 'Login failed')
      }
      setToken(data.token)
      localStorage.setItem('token', data.token)
      setLogin(s => ({ ...s, password: '', code: '' }))
      setBootstrapResult({ otpauth: '', totp_enabled: false })
    } catch (e) {
      setError(e.message || 'Login failed')
    } finally {
      setLoginBusy(false)
    }
  }, [login])

  const doBootstrap = useCallback(async (e) => {
    e.preventDefault()
    setBootstrapBusy(true)
    setError('')
    setBootstrapResult({ otpauth: '', totp_enabled: false })
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
            password: bootstrapForm.password,
            role: (bootstrapForm.role || 'admin').trim().toLowerCase(),
            enable_totp: !!bootstrapForm.enable_totp
          })
        })
      } catch (err) {
        throw new Error('Unable to reach Patchdeck server')
      }
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok) {
        throw new Error(data.error || 'Bootstrap failed')
      }
      setBootstrapResult({ otpauth: data.otpauth || '', totp_enabled: !!data.totp_enabled })
      setSetupStatus(s => ({ ...s, bootstrap_required: false }))
      setLogin(s => ({ ...s, username: bootstrapForm.username.trim(), code: '' }))
      setBootstrapForm(s => ({ ...s, username: '', password: '', confirm_password: '', enable_totp: true }))
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
    localStorage.removeItem('token')
  }, [])

  return {
    token, setToken,
    setupStatus, setupLoading,
    error, setError,
    login, setLogin, loginBusy, doLogin,
    bootstrapForm, setBootstrapForm, bootstrapBusy, doBootstrap,
    bootstrapResult,
    logout, clearToken
  }
}
