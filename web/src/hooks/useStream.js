import { useState, useRef, useCallback, useEffect } from 'react'
import { API } from '../api.js'

export function useActionStream() {
  const [output, setOutput] = useState([])
  const [phase, setPhase] = useState(null)
  const [progress, setProgress] = useState(null)
  const [isStreaming, setIsStreaming] = useState(false)
  const [error, setError] = useState(null)
  const [result, setResult] = useState(null)
  const [streamMeta, setStreamMeta] = useState(null)
  const eventSourceRef = useRef(null)
  const doneReceivedRef = useRef(false)
  const resultReceivedRef = useRef(false)

  const cleanup = useCallback(() => {
    if (eventSourceRef.current) {
      eventSourceRef.current.close()
      eventSourceRef.current = null
    }
  }, [])

  // Cleanup on unmount
  useEffect(() => cleanup, [cleanup])

  const resetStream = useCallback(() => {
    cleanup()
    doneReceivedRef.current = false
    resultReceivedRef.current = false
    setOutput([])
    setPhase(null)
    setProgress(null)
    setIsStreaming(false)
    setError(null)
    setResult(null)
    setStreamMeta(null)
  }, [cleanup])

  const startStream = useCallback((hostId, mode, token) => {
    // Reset state for new stream
    cleanup()
    doneReceivedRef.current = false
    resultReceivedRef.current = false
    setOutput([])
    setPhase(null)
    setProgress(null)
    setIsStreaming(true)
    setError(null)
    setResult(null)
    setStreamMeta(null)

    const params = new URLSearchParams()
    if (token) params.set('token', token)
    params.set('force', 'true')

    const url = `${API}/hosts/${hostId}/${mode}/stream?${params.toString()}`
    const es = new EventSource(url)
    eventSourceRef.current = es

    es.addEventListener('start', (e) => {
      try {
        const data = JSON.parse(e.data)
        setStreamMeta(data)
      } catch {}
    })

    es.addEventListener('line', (e) => {
      try {
        const data = JSON.parse(e.data)
        setOutput(prev => [...prev, { text: data.text, seq: data.seq }])
      } catch {}
    })

    es.addEventListener('progress', (e) => {
      try {
        const data = JSON.parse(e.data)
        setPhase(data.phase || null)
        setProgress(data)
      } catch {}
    })

    es.addEventListener('result', (e) => {
      try {
        const data = JSON.parse(e.data)
        resultReceivedRef.current = true
        setResult(data)
      } catch {}
    })

    es.addEventListener('error', (e) => {
      // SSE spec "error" event from server (has e.data)
      if (e.data) {
        try {
          const data = JSON.parse(e.data)
          setError(data.error || 'Stream error')
        } catch {
          setError('Stream error')
        }
      }
    })

    es.addEventListener('done', () => {
      doneReceivedRef.current = true
      setIsStreaming(false)
      cleanup()
    })

    // EventSource built-in onerror (connection lost, server closed, etc.)
    // This fires when the connection drops — including after a normal server-side close.
    // We only treat it as an error if we never received a 'done' event.
    es.onerror = () => {
      if (eventSourceRef.current !== es) return
      // Always close — prevent EventSource auto-reconnect
      cleanup()
      // If we already got 'done', this is just the connection closing — ignore
      if (doneReceivedRef.current) return
      // If we got a result but no done, treat as success (server closed before done)
      if (resultReceivedRef.current) {
        setIsStreaming(false)
        return
      }
      // Genuine connection failure
      setIsStreaming(false)
      setError(prevErr => prevErr || 'Connection to server lost')
    }
  }, [cleanup])

  return { startStream, resetStream, output, phase, progress, isStreaming, error, result, streamMeta }
}
