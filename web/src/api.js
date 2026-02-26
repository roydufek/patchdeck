export const API = import.meta.env.VITE_PATCHDECK_API || '/api'

export function createAuthedFetch(token, onUnauthorized) {
  return async function authedFetch(path, opts = {}) {
    const headers = { ...(opts.headers || {}), Authorization: `Bearer ${token}` }
    let resp
    try {
      resp = await fetch(`${API}${path}`, { ...opts, headers })
    } catch (err) {
      throw new Error('Unable to reach Patchdeck server')
    }
    if (resp.status === 401) {
      if (onUnauthorized) onUnauthorized()
      // Don't surface "session expired" if there was never a real token
      if (!token) throw new Error('Authentication required')
      throw new Error('Session expired. Please log in again.')
    }
    return resp
  }
}

export function apiErrorMessage(data, fallback) {
  if (!data || typeof data !== 'object') return fallback

  if (data.code === 'host_key_mismatch' || data.operator_action_required) {
    const expected = (data.expected_fingerprint || 'unknown').trim() || 'unknown'
    const presented = (data.presented_fingerprint || 'unknown').trim() || 'unknown'
    return `SSH host key mismatch detected (possible MITM). Operations are blocked until you Accept or Deny the new fingerprint. Expected: ${expected} | Presented: ${presented}`
  }

  if (typeof data.error === 'string' && data.error.trim()) return data.error.trim()
  return fallback
}

/**
 * Extract the best user-facing error message from a caught error.
 * Handles network errors, JSON API errors, and generic errors.
 */
export function extractErrorMessage(err, fallback = 'An unexpected error occurred') {
  if (!err) return fallback
  const msg = err.message || ''
  // Network / fetch errors
  if (msg.includes('Failed to fetch') || msg.includes('NetworkError') || msg.includes('Load failed')) {
    return 'Unable to reach Patchdeck server'
  }
  if (msg.includes('AbortError') || msg.includes('aborted')) {
    return 'Request was cancelled'
  }
  return msg || fallback
}
