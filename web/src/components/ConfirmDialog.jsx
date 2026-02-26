import React from 'react'

/**
 * Reusable confirmation dialog with dark theme.
 * Props:
 *   open       - boolean, whether to show the dialog
 *   title      - string, dialog title
 *   message    - string, dialog body text
 *   confirmLabel - string, label for the confirm button (default: "Confirm")
 *   cancelLabel  - string, label for the cancel button (default: "Cancel")
 *   confirmColor - 'red' | 'blue' (default: 'red')
 *   onConfirm  - function, called when user confirms
 *   onCancel   - function, called when user cancels or clicks backdrop
 *   busy       - boolean, disable buttons while busy (optional)
 */
export default function ConfirmDialog({
  open, title, message, confirmLabel = 'Confirm', cancelLabel = 'Cancel',
  confirmColor = 'red', onConfirm, onCancel, busy = false
}) {
  if (!open) return null

  const confirmColorClass = confirmColor === 'blue'
    ? 'bg-blue-600 hover:bg-blue-500 text-white'
    : 'bg-red-600 hover:bg-red-500 text-white'

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm"
      onClick={e => { if (e.target === e.currentTarget && !busy) onCancel() }}
    >
      <div className="w-full max-w-md rounded-xl border border-gray-200 dark:border-zinc-800 bg-white dark:bg-zinc-900 shadow-2xl p-6">
        {title && <h3 className="text-base font-semibold text-gray-900 dark:text-zinc-100 mb-2">{title}</h3>}
        {message && <p className="text-sm text-gray-600 dark:text-zinc-400 mb-6 leading-relaxed">{message}</p>}
        <div className="flex items-center justify-end gap-3">
          <button
            onClick={onCancel}
            disabled={busy}
            className="rounded-lg px-4 py-2 text-sm bg-gray-200 dark:bg-zinc-700 text-gray-700 dark:text-zinc-200 hover:bg-gray-300 dark:hover:bg-zinc-600 disabled:opacity-40 transition-colors"
          >
            {cancelLabel}
          </button>
          <button
            onClick={onConfirm}
            disabled={busy}
            className={`rounded-lg px-4 py-2 text-sm font-medium disabled:opacity-40 transition-colors ${confirmColorClass}`}
          >
            {busy ? 'Processing…' : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
