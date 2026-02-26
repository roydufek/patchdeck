import React from 'react'

export default function Spinner({ size = 'md', className = '' }) {
  const sizes = { sm: 'h-4 w-4', md: 'h-6 w-6', lg: 'h-8 w-8' }
  return (
    <div
      className={`${sizes[size] || sizes.md} animate-spin rounded-full border-2 border-gray-300 dark:border-zinc-700 border-t-emerald-500 ${className}`}
      role="status"
      aria-label="Loading"
    />
  )
}
