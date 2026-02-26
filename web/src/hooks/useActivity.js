import { useState, useCallback } from 'react'

export default function useActivity(token) {
  const [entries, setEntries] = useState([])
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)

  const loadActivity = useCallback(async (limit = 50, offset = 0, hostId = '') => {
    setLoading(true)
    setError(null)
    try {
      const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
      if (hostId) params.set('host_id', hostId)
      let res
      try {
        res = await fetch(`/api/activity?${params}`, {
          headers: { Authorization: `Bearer ${token}` }
        })
      } catch (err) {
        throw new Error('Unable to reach Patchdeck server')
      }
      if (!res.ok) throw new Error(`Failed to load activity`)
      const data = await res.json()
      const items = Array.isArray(data) ? data : []
      if (offset === 0) {
        setEntries(items)
      } else {
        setEntries(prev => [...prev, ...items])
      }
      setHasMore(items.length >= limit)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }, [token])

  return { entries, hasMore, loading, error, loadActivity }
}
