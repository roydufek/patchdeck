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

    let gotStart = false
    let gotResult = false
    let retryTimer = null

    const connect = () => {
      const es = new EventSource(url)
      eventSourceRef.current = es

      es.addEventListener('start', () => {
        gotStart = true
      })

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
          gotResult = true
          setElapsed(d.elapsed_seconds || 0)
          setStatus(d.recovered ? 'recovered' : 'timeout')
        } catch {}
      })

      es.addEventListener('done', () => {
        cleanup()
      })

      es.onerror = () => {
        if (eventSourceRef.current !== es) return
        cleanup()
        if (gotResult) return // already handled via result event
        if (!gotStart) {
          // Connection dropped before server sent 'start' — host is mid-shutdown,
          // retry after a short delay (the server's 10s initial wait may not have fired yet)
          retryTimer = setTimeout(() => {
            if (eventSourceRef.current === null) connect()
          }, 3000)
          return
        }
        // Got start but lost connection mid-monitoring — treat as error
        setStatus(prev => prev === 'monitoring' ? 'error' : prev)
      }
    }

    connect()

    // Store cleanup for retryTimer too
    const originalCleanup = cleanup
    eventSourceRef._retryCleanup = () => {
      if (retryTimer) clearTimeout(retryTimer)
      retryTimer = null
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
