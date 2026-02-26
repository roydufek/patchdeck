import { useState, useCallback, useRef } from 'react'

let toastIdCounter = 0

/**
 * useToast hook — manages a stack of toast notifications.
 * Returns { toasts, addToast, removeToast }.
 *
 * addToast({ type: 'success'|'error'|'info', message: string, duration?: number })
 */
export function useToast() {
  const [toasts, setToasts] = useState([])
  const timersRef = useRef({})

  const removeToast = useCallback((id) => {
    if (timersRef.current[id]) {
      clearTimeout(timersRef.current[id])
      delete timersRef.current[id]
    }
    setToasts(prev => prev.filter(t => t.id !== id))
  }, [])

  const addToast = useCallback(({ type = 'info', message, duration = 4000 }) => {
    const id = ++toastIdCounter
    setToasts(prev => [...prev, { id, type, message }])
    if (duration > 0) {
      timersRef.current[id] = setTimeout(() => {
        removeToast(id)
      }, duration)
    }
    return id
  }, [removeToast])

  return { toasts, addToast, removeToast }
}
