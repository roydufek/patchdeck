import { useState, useRef, useCallback, useEffect } from 'react'
import { API } from '../api.js'

// Multi-host recovery monitor. Tracks several hosts coming back concurrently (e.g. a bulk
// reboot) — each host gets its own /await-recovery SSE reconnect loop, its own status, and its
// own progress. `monitors` is a Map<hostId, {status, attempts, elapsed, timeoutSec}> where
// status is 'monitoring' | 'recovered' | 'timeout' | 'error'. startMonitor(hostId, …) adds a
// monitor without disturbing the others (it only replaces that host's own prior monitor).
export function useRecoveryMonitor() {
  const [monitors, setMonitors] = useState(() => new Map())
  const sourcesRef = useRef(new Map()) // hostId -> { es, retryTimer }

  const stopOne = useCallback((hostId) => {
    const s = sourcesRef.current.get(hostId)
    if (s) {
      if (s.retryTimer) clearTimeout(s.retryTimer)
      if (s.es) s.es.close()
      sourcesRef.current.delete(hostId)
    }
  }, [])

  const cleanupAll = useCallback(() => {
    sourcesRef.current.forEach(s => {
      if (s.retryTimer) clearTimeout(s.retryTimer)
      if (s.es) s.es.close()
    })
    sourcesRef.current.clear()
  }, [])

  useEffect(() => cleanupAll, [cleanupAll])

  const patchHost = useCallback((hostId, patch) => {
    setMonitors(prev => {
      const next = new Map(prev)
      next.set(hostId, { ...(next.get(hostId) || {}), ...patch })
      return next
    })
  }, [])

  const startMonitor = useCallback((hostId, _token, timeoutSec = 180) => {
    stopOne(hostId) // replace only THIS host's monitor; leave the others running
    patchHost(hostId, { status: 'monitoring', attempts: 0, elapsed: 0, timeoutSec })

    // Auth rides on the httpOnly session cookie (withCredentials); token kept for signature compat.
    const params = new URLSearchParams()
    params.set('timeout', String(timeoutSec))
    const url = `${API}/hosts/${hostId}/await-recovery?${params}`

    let gotStart = false
    let gotResult = false

    const connect = () => {
      const es = new EventSource(url, { withCredentials: true })
      sourcesRef.current.set(hostId, { es, retryTimer: null })

      es.addEventListener('start', () => { gotStart = true })

      es.addEventListener('ping', (e) => {
        try {
          const d = JSON.parse(e.data)
          patchHost(hostId, { attempts: d.attempt || 0, elapsed: d.elapsed_seconds || 0 })
        } catch {}
      })

      es.addEventListener('result', (e) => {
        try {
          const d = JSON.parse(e.data)
          gotResult = true
          patchHost(hostId, { elapsed: d.elapsed_seconds || 0, status: d.recovered ? 'recovered' : 'timeout' })
        } catch {}
      })

      es.addEventListener('done', () => { stopOne(hostId) })

      es.onerror = () => {
        const cur = sourcesRef.current.get(hostId)
        if (!cur || cur.es !== es) return // superseded by a newer monitor for this host
        es.close()
        if (gotResult) { sourcesRef.current.delete(hostId); return } // already handled via result
        if (!gotStart) {
          // Dropped before 'start' — host is mid-shutdown; retry shortly (server's initial wait
          // may not have fired yet). Tracked so stopOne/reset/unmount can cancel it.
          const t = setTimeout(() => {
            const c = sourcesRef.current.get(hostId)
            if (c && c.retryTimer) { c.retryTimer = null; connect() }
          }, 3000)
          sourcesRef.current.set(hostId, { es: null, retryTimer: t })
          return
        }
        // Got start but lost the connection mid-monitoring — treat as error.
        sourcesRef.current.delete(hostId)
        patchHost(hostId, { status: 'error' })
      }
    }

    connect()
  }, [stopOne, patchHost])

  // reset() with no arg clears every monitor; reset(hostId) clears just one.
  const reset = useCallback((hostId) => {
    if (hostId == null) {
      cleanupAll()
      setMonitors(new Map())
      return
    }
    stopOne(hostId)
    setMonitors(prev => {
      if (!prev.has(hostId)) return prev
      const next = new Map(prev)
      next.delete(hostId)
      return next
    })
  }, [cleanupAll, stopOne])

  return { monitors, startMonitor, reset }
}
