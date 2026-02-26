import { useState, useCallback } from 'react'
import { API } from '../api.js'

export function useTOTP(token) {
  const [totpStatus, setTotpStatus] = useState(null)
  const [setupData, setSetupData] = useState(null)
  const [recoveryCodes, setRecoveryCodes] = useState(null)
  const [totpBusy, setTotpBusy] = useState(false)
  const [totpError, setTotpError] = useState('')

  const authHeaders = useCallback(() => ({
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${token}`
  }), [token])

  const fetchStatus = useCallback(async () => {
    if (!token) return
    try {
      const resp = await fetch(`${API}/settings/totp`, {
        headers: { 'Authorization': `Bearer ${token}` }
      })
      const data = await resp.json()
      setTotpStatus({ enabled: !!data.enabled })
    } catch {
      setTotpStatus({ enabled: false })
    }
  }, [token])

  const startSetup = useCallback(async (secret) => {
    setTotpBusy(true)
    setTotpError('')
    try {
      const body = secret ? JSON.stringify({ secret }) : '{}'
      const resp = await fetch(`${API}/settings/totp/setup`, {
        method: 'POST',
        headers: authHeaders(),
        body
      })
      const data = await resp.json()
      if (!resp.ok) throw new Error(data.error || 'Setup failed')
      setSetupData(data)
      return data
    } catch (e) {
      setTotpError(e.message)
      return null
    } finally {
      setTotpBusy(false)
    }
  }, [token, authHeaders])

  const confirmSetup = useCallback(async (secret, code) => {
    setTotpBusy(true)
    setTotpError('')
    try {
      const resp = await fetch(`${API}/settings/totp/confirm`, {
        method: 'POST',
        headers: authHeaders(),
        body: JSON.stringify({ secret, code })
      })
      const data = await resp.json()
      if (!resp.ok) throw new Error(data.error || 'Verification failed')
      setRecoveryCodes(data.recovery_codes || [])
      setSetupData(null)
      setTotpStatus({ enabled: true })
      return data
    } catch (e) {
      setTotpError(e.message)
      return null
    } finally {
      setTotpBusy(false)
    }
  }, [token, authHeaders])

  const disableTOTP = useCallback(async (password) => {
    setTotpBusy(true)
    setTotpError('')
    try {
      const resp = await fetch(`${API}/settings/totp/disable`, {
        method: 'POST',
        headers: authHeaders(),
        body: JSON.stringify({ password })
      })
      const data = await resp.json()
      if (!resp.ok) throw new Error(data.error || 'Failed to disable TOTP')
      setTotpStatus({ enabled: false })
      setRecoveryCodes(null)
      return data
    } catch (e) {
      setTotpError(e.message)
      return null
    } finally {
      setTotpBusy(false)
    }
  }, [token, authHeaders])

  const cancelSetup = useCallback(() => {
    setSetupData(null)
    setTotpError('')
  }, [])

  const dismissRecoveryCodes = useCallback(() => {
    setRecoveryCodes(null)
  }, [])

  return {
    totpStatus, setupData, recoveryCodes, totpBusy, totpError,
    fetchStatus, startSetup, confirmSetup, disableTOTP, cancelSetup, dismissRecoveryCodes
  }
}
