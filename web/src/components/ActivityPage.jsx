import React, { useEffect, useState } from 'react'
import { timeAgo, fullDate } from '../utils/timeago.js'
import Spinner from './Spinner.jsx'

const EVENT_META = {
  scan_ok:            { icon: '🔍', label: 'Scan completed',           tone: 'text-emerald-400' },
  scan_fail:          { icon: '❌', label: 'Scan failed',              tone: 'text-red-400' },
  apply_ok:           { icon: '📦', label: 'Updates applied',          tone: 'text-emerald-400' },
  apply_fail:         { icon: '⚠️', label: 'Apply failed',             tone: 'text-red-400' },
  reboot:             { icon: '🔄', label: 'Reboot initiated',         tone: 'text-amber-400' },
  reboot_ok:          { icon: '🔄', label: 'Reboot initiated',         tone: 'text-amber-400' },
  reboot_fail:        { icon: '❌', label: 'Reboot failed',            tone: 'text-red-400' },
  shutdown:           { icon: '⏻',  label: 'Shutdown initiated',       tone: 'text-red-400' },
  shutdown_ok:        { icon: '⏻',  label: 'Shutdown initiated',       tone: 'text-red-400' },
  shutdown_fail:      { icon: '❌', label: 'Shutdown failed',          tone: 'text-red-400' },
  restart_services:   { icon: '🔧', label: 'Services restarted',       tone: 'text-blue-400' },
  restart_ok:         { icon: '🔧', label: 'Services restarted',       tone: 'text-blue-400' },
  restart_fail:       { icon: '❌', label: 'Service restart failed',   tone: 'text-red-400' },
  host_added:         { icon: '➕', label: 'Host added',               tone: 'text-emerald-400' },
  host_deleted:       { icon: '🗑️', label: 'Host deleted',             tone: 'text-gray-500 dark:text-zinc-400' },
  host_key_accepted:  { icon: '✅', label: 'Host key accepted',        tone: 'text-emerald-400' },
  host_key_denied:    { icon: '🚫', label: 'Host key denied',          tone: 'text-red-400' },
}

function eventMeta(type) {
  return EVENT_META[type] || { icon: '📋', label: type, tone: 'text-gray-500 dark:text-zinc-400' }
}

export default function ActivityPage({ activity, hosts, onLoadActivity }) {
  const [hostFilter, setHostFilter] = useState('')
  const [offset, setOffset] = useState(0)
  const LIMIT = 50

  useEffect(() => {
    setOffset(0)
    onLoadActivity(LIMIT, 0, hostFilter)
  }, [hostFilter])

  function handleLoadMore() {
    const next = offset + LIMIT
    setOffset(next)
    onLoadActivity(LIMIT, next, hostFilter)
  }

  const uniqueHosts = Array.isArray(hosts) ? hosts : []

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold text-gray-800 dark:text-zinc-100">Activity</h2>
        <select
          value={hostFilter}
          onChange={e => setHostFilter(e.target.value)}
          className="bg-white dark:bg-zinc-900 border border-gray-300 dark:border-zinc-700 text-gray-700 dark:text-zinc-300 rounded-md px-3 py-1.5 text-sm"
        >
          <option value="">All hosts</option>
          {uniqueHosts.map(h => (
            <option key={h.id} value={h.id}>{h.name}</option>
          ))}
        </select>
      </div>

      {activity.error && (
        <p className="text-sm text-red-500 dark:text-red-400">{activity.error}</p>
      )}

      {activity.entries.length === 0 && !activity.loading && (
        <div className="text-center py-12 text-gray-400 dark:text-zinc-600">
          <p className="text-3xl mb-2">📋</p>
          <p className="text-sm">No activity recorded yet.</p>
        </div>
      )}

      <div className="space-y-1">
        {activity.entries.map((entry, i) => {
          const meta = eventMeta(entry.event_type)
          return (
            <div key={entry.id || i} className="flex items-start gap-3 rounded-lg bg-gray-50 dark:bg-zinc-900/60 border border-gray-200 dark:border-zinc-800/50 px-4 py-2.5 text-sm">
              <span className="text-base mt-0.5 flex-shrink-0">{meta.icon}</span>
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2 flex-wrap">
                  <span className={`font-medium ${meta.tone}`}>{meta.label}</span>
                  {entry.host_name && (
                    <span className="text-gray-500 dark:text-zinc-500 text-xs">
                      on <span className="text-gray-600 dark:text-zinc-400">{entry.host_name}</span>
                    </span>
                  )}
                  <span className="text-gray-400 dark:text-zinc-600 text-xs" title={fullDate(entry.created_at)}>
                    {timeAgo(entry.created_at)}
                  </span>
                </div>
                {entry.summary && (
                  <p className="text-xs text-gray-500 dark:text-zinc-500 mt-0.5 truncate">{entry.summary}</p>
                )}
              </div>
            </div>
          )
        })}
      </div>

      {activity.hasMore && (
        <div className="text-center pt-2">
          <button
            onClick={handleLoadMore}
            disabled={activity.loading}
            className="text-sm text-gray-500 dark:text-zinc-400 hover:text-gray-700 dark:hover:text-zinc-200 transition-colors disabled:opacity-50"
          >
            {activity.loading ? 'Loading…' : 'Load more'}
          </button>
        </div>
      )}

      {activity.loading && activity.entries.length === 0 && (
        <div className="flex flex-col items-center justify-center py-16 gap-3">
          <Spinner size="lg" />
          <p className="text-sm text-gray-500 dark:text-zinc-500">Loading activity…</p>
        </div>
      )}
    </div>
  )
}
