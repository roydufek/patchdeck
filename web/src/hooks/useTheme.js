import { useState, useEffect, useCallback, useMemo } from 'react'

function getSystemPrefersDark() {
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

function applyTheme(theme) {
  const isDark = theme === 'dark' || (theme === 'system' && getSystemPrefersDark())
  document.documentElement.classList.toggle('dark', isDark)
  return isDark
}

export function useTheme() {
  const [theme, setThemeState] = useState(() => {
    try {
      return localStorage.getItem('patchdeck-theme') || 'system'
    } catch {
      return 'system'
    }
  })

  const [isDark, setIsDark] = useState(() => applyTheme(theme))

  const setTheme = useCallback((newTheme) => {
    setThemeState(newTheme)
    try {
      localStorage.setItem('patchdeck-theme', newTheme)
    } catch {}
    setIsDark(applyTheme(newTheme))
  }, [])

  // Apply on mount
  useEffect(() => {
    setIsDark(applyTheme(theme))
  }, [theme])

  // Listen for system preference changes when in 'system' mode
  useEffect(() => {
    const mql = window.matchMedia('(prefers-color-scheme: dark)')
    function handler() {
      if (theme === 'system') {
        setIsDark(applyTheme('system'))
      }
    }
    mql.addEventListener('change', handler)
    return () => mql.removeEventListener('change', handler)
  }, [theme])

  const cycleTheme = useCallback(() => {
    setThemeState(prev => {
      const next = prev === 'system' ? 'light' : prev === 'light' ? 'dark' : 'system'
      try {
        localStorage.setItem('patchdeck-theme', next)
      } catch {}
      applyTheme(next)
      return next
    })
  }, [])

  return useMemo(() => ({ theme, setTheme, isDark, cycleTheme }), [theme, setTheme, isDark, cycleTheme])
}
