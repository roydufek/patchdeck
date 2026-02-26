import { useState, useRef, useCallback, useEffect } from 'react'
import { API } from '../api.js'

export function useRecoveryMonitor() {
  // States: idle | monitoring | recovered | timeout | error
  const [status, setStatus] = useState('idle')
  const [hostId, setHostId] = useState(null)
  const [attempts, setAttempts] = useState(0)
  const [elapsed, setElapsed] = useState(0)
  const eventSourceRef = useRef(null)

  const cleanup = useCallback(() => {
    if (eventSourceRef.current) {
      eventSourceRef.current.close()
      eventSourceRef.current = null
    }
  }, [])

  useEffect(() => cleanup, [cleanup])

  const startMonitor = useCallback((hId, token, timeoutSec = 180) => {
    cleanup()
    setHostId(hId)
    setStatus('monitoring')
    setAttempts(0)
    setElapsed(0)

    const params = new URLSearchParams()
    if (token) params.set('token', token)
    params.set('timeout', String(timeoutSec))
    const url = `${API}/hosts/${hId}/await-recovery?${params}`
    const es = new EventSource(url)
    eventSourceRef.current = es

    es.addEventListener('ping', (e) => {
      try {
        const d = JSON.parse(e.data)
        setAttempts(d.attempt || 0)
        setElapsed(d.elapsed_seconds || 0)
      } catch {}
    })

    es.addEventListener('result', (e) => {
      try {
        const d = JSON.parse(e.data)
        setElapsed(d.elapsed_seconds || 0)
        setStatus(d.recovered ? 'recovered' : 'timeout')
      } catch {}
    })

    es.addEventListener('done', () => {
      cleanup()
    })

    es.onerror = () => {
      if (eventSourceRef.current === es) {
        cleanup()
        setStatus(prev => prev === 'monitoring' ? 'error' : prev)
      }
    }
  }, [cleanup])

  const reset = useCallback(() => {
    cleanup()
    setStatus('idle')
    setHostId(null)
    setAttempts(0)
    setElapsed(0)
  }, [cleanup])

  return { status, hostId, attempts, elapsed, startMonitor, reset }
}
