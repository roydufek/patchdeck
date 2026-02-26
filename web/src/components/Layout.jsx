import React, { useState } from 'react'
import { useTheme } from '../hooks/useTheme.js'

const NAV_ITEMS = [
  { key: 'hosts', label: 'Hosts', icon: '◉' },
  { key: 'jobs', label: 'Schedules', icon: '⏱' },
  { key: 'activity', label: 'Activity', icon: '📋' },
  { key: 'settings', label: 'Settings', icon: '⚙' },
]

function ThemeIcon({ theme, isDark }) {
  if (theme === 'system') {
    return (
      <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
      </svg>
    )
  }
  if (theme === 'light') {
    return (
      <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364 6.364l-.707-.707M6.343 6.343l-.707-.707m12.728 0l-.707.707M6.343 17.657l-.707.707M16 12a4 4 0 11-8 0 4 4 0 018 0z" />
      </svg>
    )
  }
  return (
    <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
      <path strokeLinecap="round" strokeLinejoin="round" d="M20.354 15.354A9 9 0 018.646 3.646 9.003 9.003 0 0012 21a9.003 9.003 0 008.354-5.646z" />
    </svg>
  )
}

const THEME_LABELS = { system: 'System', light: 'Light', dark: 'Dark' }

export default function Layout({ children, currentPage, onNavigate, onLogout, onRefresh, loading }) {
  const [collapsed, setCollapsed] = useState(false)
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const { theme, isDark, cycleTheme } = useTheme()

  function handleNav(key) {
    onNavigate(key)
    setSidebarOpen(false)
  }

  return (
    <div className="flex h-screen bg-gray-50 dark:bg-zinc-950 text-gray-800 dark:text-zinc-100">
      {/* Mobile top bar */}
      <div className="md:hidden fixed top-0 left-0 right-0 h-14 bg-white dark:bg-zinc-900 border-b border-gray-200 dark:border-zinc-800 flex items-center px-4 z-30">
        <button
          onClick={() => setSidebarOpen(v => !v)}
          className="text-gray-600 dark:text-zinc-400 hover:text-gray-900 dark:hover:text-white text-xl p-1"
        >
          ☰
        </button>
        <img src="/logo-32.png" alt="Patchdeck" className="ml-3 w-6 h-6" />
        <h1 className="ml-1.5 font-semibold text-gray-900 dark:text-white">Patchdeck</h1>
      </div>

      {/* Mobile backdrop */}
      {sidebarOpen && (
        <div
          className="md:hidden fixed inset-0 bg-black/50 z-30"
          onClick={() => setSidebarOpen(false)}
        />
      )}

      {/* Sidebar */}
      <aside className={`fixed top-0 left-0 h-full z-40 flex flex-col border-r border-gray-200 dark:border-zinc-800/80 bg-white dark:bg-zinc-950 transition-all
        transform md:translate-x-0 ${sidebarOpen ? 'translate-x-0' : '-translate-x-full'}
        ${collapsed ? 'md:w-16 w-56' : 'w-56 md:w-52'}`}
      >
        <div className="flex items-center gap-2 px-4 py-5 border-b border-gray-200 dark:border-zinc-800/60">
          {!collapsed && (
            <div className="flex items-center gap-2 cursor-pointer hover:opacity-80 transition-opacity select-none" onClick={() => window.location.reload()}>
              <img src="/logo-32.png" alt="Patchdeck" className="w-7 h-7" />
              <h1 className="text-lg font-semibold tracking-tight text-gray-900 dark:text-white">Patchdeck</h1>
            </div>
          )}
          {collapsed && (
            <div className="flex justify-center w-full cursor-pointer hover:opacity-80 transition-opacity" onClick={() => window.location.reload()}>
              <img src="/logo-32.png" alt="Patchdeck" className="w-7 h-7" />
            </div>
          )}
          {/* Close button on mobile */}
          <button
            onClick={() => setSidebarOpen(false)}
            className="md:hidden ml-auto text-gray-400 dark:text-zinc-500 hover:text-gray-600 dark:hover:text-zinc-300 text-sm p-1"
            title="Close"
          >
            ✕
          </button>
          {/* Collapse button on desktop */}
          <button
            onClick={() => setCollapsed(v => !v)}
            className="hidden md:block ml-auto text-gray-400 dark:text-zinc-500 hover:text-gray-600 dark:hover:text-zinc-300 text-sm p-1"
            title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          >
            {collapsed ? '»' : '«'}
          </button>
        </div>

        <nav className="flex-1 py-3 space-y-0.5 px-2">
          {NAV_ITEMS.map(item => {
            const active = currentPage === item.key
            return (
              <button
                key={item.key}
                onClick={() => handleNav(item.key)}
                className={`w-full flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm transition-colors
                  ${active
                    ? 'bg-gray-100 dark:bg-zinc-800/80 text-gray-900 dark:text-white font-medium'
                    : 'text-gray-500 dark:text-zinc-400 hover:text-gray-700 dark:hover:text-zinc-200 hover:bg-gray-100 dark:hover:bg-zinc-800/40'
                  }`}
                title={collapsed ? item.label : undefined}
              >
                <span className="text-base flex-shrink-0">{item.icon}</span>
                {!collapsed && <span>{item.label}</span>}
              </button>
            )
          })}
        </nav>

        <div className="border-t border-gray-200 dark:border-zinc-800/60 p-2 space-y-1">
          <button
            onClick={cycleTheme}
            className="w-full flex items-center gap-3 rounded-lg px-3 py-2 text-sm text-gray-500 dark:text-zinc-400 hover:text-gray-700 dark:hover:text-zinc-200 hover:bg-gray-100 dark:hover:bg-zinc-800/40 transition-colors"
            title={`Theme: ${THEME_LABELS[theme]} — click to cycle`}
          >
            <span className="text-base flex-shrink-0"><ThemeIcon theme={theme} isDark={isDark} /></span>
            {!collapsed && <span>{THEME_LABELS[theme]}</span>}
          </button>
          <button
            onClick={onRefresh}
            disabled={loading}
            className="w-full flex items-center gap-3 rounded-lg px-3 py-2 text-sm text-gray-500 dark:text-zinc-400 hover:text-gray-700 dark:hover:text-zinc-200 hover:bg-gray-100 dark:hover:bg-zinc-800/40 disabled:opacity-40 transition-colors"
            title="Refresh all data"
          >
            <span className="text-base flex-shrink-0">↻</span>
            {!collapsed && <span>{loading ? 'Refreshing…' : 'Refresh'}</span>}
          </button>
          <button
            onClick={onLogout}
            className="w-full flex items-center gap-3 rounded-lg px-3 py-2 text-sm text-gray-500 dark:text-zinc-400 hover:text-gray-700 dark:hover:text-zinc-200 hover:bg-gray-100 dark:hover:bg-zinc-800/40 transition-colors"
            title="Log out"
          >
            <span className="text-base flex-shrink-0">
              <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><polyline points="16 17 21 12 16 7"/><line x1="21" y1="12" x2="9" y2="12"/></svg>
            </span>
            {!collapsed && <span>Log out</span>}
          </button>
        </div>
      </aside>

      {/* Main content */}
      <main className={`flex-1 overflow-y-auto pt-14 md:pt-0 transition-all ${collapsed ? 'md:ml-16' : 'md:ml-52'}`}>
        <div className="max-w-6xl mx-auto px-4 md:px-6 py-4 md:py-6">
          {children}
        </div>
      </main>
    </div>
  )
}
