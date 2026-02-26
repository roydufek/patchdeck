import React, { createContext, useContext } from 'react'
import { useToast } from '../hooks/useToast.js'

const ToastContext = createContext(null)

export function useToastContext() {
  return useContext(ToastContext)
}

export function ToastProvider({ children }) {
  const toast = useToast()

  return (
    <ToastContext.Provider value={toast}>
      {children}
      <ToastContainer toasts={toast.toasts} onDismiss={toast.removeToast} />
    </ToastContext.Provider>
  )
}

const borderColors = {
  success: 'border-l-green-600',
  error: 'border-l-red-600',
  info: 'border-l-blue-600'
}

const icons = {
  success: '✓',
  error: '✗',
  info: 'ℹ'
}

const iconColors = {
  success: 'text-green-400',
  error: 'text-red-400',
  info: 'text-blue-400'
}

function ToastContainer({ toasts, onDismiss }) {
  if (toasts.length === 0) return null

  return (
    <div className="fixed top-4 right-4 z-[60] flex flex-col gap-2 max-w-sm w-full pointer-events-none">
      {toasts.map(t => (
        <div
          key={t.id}
          className={`pointer-events-auto rounded-lg border border-gray-200 dark:border-zinc-700 bg-white dark:bg-zinc-900 shadow-xl px-4 py-3 flex items-start gap-3 border-l-4 ${borderColors[t.type] || borderColors.info} animate-slide-in`}
        >
          <span className={`flex-shrink-0 text-sm mt-0.5 ${iconColors[t.type] || iconColors.info}`}>
            {icons[t.type] || icons.info}
          </span>
          <p className="text-sm text-gray-700 dark:text-zinc-200 flex-1">{t.message}</p>
          <button
            onClick={() => onDismiss(t.id)}
            className="flex-shrink-0 text-gray-400 dark:text-zinc-500 hover:text-gray-600 dark:hover:text-zinc-300 text-xs ml-2"
          >
            ✕
          </button>
        </div>
      ))}
    </div>
  )
}

export default ToastContainer
