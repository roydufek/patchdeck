import React, { useState, useEffect, useMemo, useRef } from 'react'
import ConfirmDialog from './ConfirmDialog.jsx'
import { useToastContext } from './Toast.jsx'
import { validateCronExpression } from '../utils/format.js'

function jobModeLabel(mode) {
  if (mode === 'scan') return 'Scan'
  if (mode === 'apply') return 'Apply'
  if (mode === 'scan_apply') return 'Scan + Apply'
  return mode || 'Unknown'
}

function jobTargetLabel(job, hosts) {
  if (job.tag_filter) return `Tag: ${job.tag_filter}`
  if (Array.isArray(job.host_ids) && job.host_ids.length > 1) {
    return `${job.host_ids.length} hosts`
  }
  return job.host_name || job.host_id || 'Unknown'
}

const CRON_PRESETS = [
  { label: 'Every hour', value: '0 * * * *' },
  { label: 'Every 6 hours', value: '0 */6 * * *' },
  { label: 'Daily at midnight', value: '0 0 * * *' },
  { label: 'Weekly (Mon 2am)', value: '0 2 * * 1' },
]

function cronSummary(cronExpr) {
  const expr = (cronExpr || '').trim().toLowerCase()
  switch (expr) {
    case '0 * * * *': case '@hourly': return 'every hour'
    case '0 */6 * * *': return 'every 6 hours'
    case '0 0 * * *': case '@daily': case '@midnight': return 'daily at midnight'
    case '0 2 * * 1': return 'weekly (Mon 2am)'
    case '@weekly': return 'weekly'
    case '@monthly': return 'monthly'
    default: return cronExpr || '—'
  }
}

export default function JobsPage({
  jobs, hosts, tags, actionBusy,
  jobForm, setJobForm, jobBusy, jobComposerOpen, setJobComposerOpen,
  onCreateJob, onToggleJob, onDeleteJob, error
}) {
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [showCronHelp, setShowCronHelp] = useState(false)
  const cronHelpRef = useRef(null)
  const toast = useToastContext()

  // Close popover on outside click
  useEffect(() => {
    if (!showCronHelp) return
    function handleClick(e) {
      if (cronHelpRef.current && !cronHelpRef.current.contains(e.target)) {
        setShowCronHelp(false)
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [showCronHelp])

  // Build a human-readable summary preview
  const previewSummary = useMemo(() => {
    const modeText = jobModeLabel(jobForm.mode)
    let targetText = ''
    if (jobForm.target_mode === 'hosts') {
      const count = jobForm.host_ids.length
      if (count === 0) targetText = 'no hosts selected'
      else if (count === 1) {
        const h = hosts.find(h => h.id === jobForm.host_ids[0])
        targetText = h ? h.name : '1 host'
      } else {
        targetText = `${count} hosts`
      }
    } else if (jobForm.target_mode === 'tag') {
      targetText = jobForm.tag_filter ? `hosts with tag "${jobForm.tag_filter}"` : 'no tag selected'
    } else if (jobForm.target_mode === 'all') {
      targetText = `all ${hosts.length} host${hosts.length !== 1 ? 's' : ''}`
    }
    const schedText = cronSummary(jobForm.cron_expr)
    return `${modeText} on ${targetText} ${schedText}`
  }, [jobForm, hosts])

  return (
    <div>
      <div className="flex items-center justify-between mb-6 gap-3 flex-wrap">
        <div>
          <h2 className="text-lg font-semibold">Scheduled Jobs</h2>
          <p className="text-sm text-gray-500 dark:text-zinc-500 mt-0.5">Create recurring scan or apply jobs.</p>
        </div>
        <button
          onClick={() => setJobComposerOpen(v => !v)}
          disabled={!hosts.length}
          className="rounded-lg px-3 py-1.5 bg-gray-900 dark:bg-white text-white dark:text-zinc-900 text-sm font-medium hover:bg-gray-800 dark:hover:bg-zinc-200 disabled:opacity-50 transition-colors"
        >
          {jobComposerOpen ? '− Hide form' : '+ Add schedule'}
        </button>
      </div>

      <div className="flex items-center gap-3 mb-4 text-xs text-gray-500 dark:text-zinc-500">
        <span>{jobs.length} job{jobs.length !== 1 ? 's' : ''}</span>
        <span>·</span>
        <span>{jobs.filter(j => j.enabled).length} enabled</span>
        <span className="hidden sm:inline">· Supports 5-field cron or @hourly/@daily/@weekly macros</span>
      </div>

      {/* Composer */}
      {jobComposerOpen && (
        <form
          className="rounded-xl border border-gray-200 dark:border-zinc-800 bg-white dark:bg-zinc-900/40 p-4 mb-6 space-y-4"
          onSubmit={onCreateJob}
        >
          {/* Row 1: Action type + Name */}
          <div className="grid sm:grid-cols-2 gap-3">
            <div>
              <label className="block text-xs text-gray-500 dark:text-zinc-500 mb-1">Action type</label>
              <select
                className="w-full rounded-lg border border-gray-300 dark:border-zinc-700 bg-gray-100 dark:bg-zinc-800/50 px-3 py-2.5 text-sm focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
                value={jobForm.mode}
                onChange={e => setJobForm(s => ({ ...s, mode: e.target.value }))}
              >
                <option value="scan">Scan</option>
                <option value="apply">Apply</option>
                <option value="scan_apply">Scan + Apply</option>
              </select>
            </div>
            <div>
              <label className="block text-xs text-gray-500 dark:text-zinc-500 mb-1">Name (optional)</label>
              <input
                className="w-full rounded-lg border border-gray-300 dark:border-zinc-700 bg-gray-100 dark:bg-zinc-800/50 px-3 py-2.5 text-sm placeholder-gray-400 dark:placeholder-zinc-500 focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
                placeholder="e.g. Nightly prod scan"
                value={jobForm.name}
                onChange={e => setJobForm(s => ({ ...s, name: e.target.value }))}
              />
            </div>
          </div>

          {/* Row 2: Target selection */}
          <div>
            <label className="block text-xs text-gray-500 dark:text-zinc-500 mb-2">Target</label>
            <div className="flex gap-4 mb-3">
              {[
                { value: 'hosts', label: 'Specific hosts' },
                { value: 'tag', label: 'Hosts with tag' },
                { value: 'all', label: 'All hosts' },
              ].map(opt => (
                <label key={opt.value} className="flex items-center gap-1.5 text-sm text-gray-700 dark:text-zinc-300 cursor-pointer select-none">
                  <input
                    type="radio"
                    name="target_mode"
                    value={opt.value}
                    checked={jobForm.target_mode === opt.value}
                    onChange={() => setJobForm(s => ({ ...s, target_mode: opt.value }))}
                    className="accent-emerald-500"
                  />
                  {opt.label}
                </label>
              ))}
            </div>

            {jobForm.target_mode === 'hosts' && (
              <div className="rounded-lg border border-gray-200 dark:border-zinc-800 bg-gray-50 dark:bg-zinc-900/60 px-3 py-2 max-h-48 overflow-y-auto space-y-1">
                {hosts.length === 0 && <p className="text-xs text-gray-400 dark:text-zinc-600">No hosts available.</p>}
                {hosts.map(h => {
                  const checked = jobForm.host_ids.includes(h.id)
                  return (
                    <label key={h.id} className="flex items-center gap-2 py-1 hover:bg-gray-100 dark:hover:bg-zinc-800/40 rounded px-1.5 cursor-pointer select-none transition-colors">
                      <input
                        type="checkbox"
                        checked={checked}
                        onChange={e => {
                          setJobForm(s => ({
                            ...s,
                            host_ids: e.target.checked
                              ? [...s.host_ids, h.id]
                              : s.host_ids.filter(id => id !== h.id)
                          }))
                        }}
                        className="rounded border-gray-300 dark:border-zinc-600 bg-gray-100 dark:bg-zinc-800 text-emerald-500 focus:ring-0 focus:ring-offset-0 h-3.5 w-3.5"
                      />
                      <span className="text-sm text-gray-700 dark:text-zinc-300">{h.name}</span>
                      <span className="text-xs text-gray-400 dark:text-zinc-600 font-mono">{h.address}</span>
                      {Array.isArray(h.tags) && h.tags.length > 0 && (
                        <span className="flex gap-1">
                          {h.tags.slice(0, 3).map((tag, i) => (
                            <span key={i} className="text-[10px] bg-gray-100 dark:bg-zinc-800 border border-gray-200 dark:border-zinc-700/50 rounded-full px-1.5 text-gray-500 dark:text-zinc-500">{tag}</span>
                          ))}
                        </span>
                      )}
                    </label>
                  )
                })}
              </div>
            )}

            {jobForm.target_mode === 'tag' && (
              <select
                className="rounded-lg border border-gray-300 dark:border-zinc-700 bg-gray-100 dark:bg-zinc-800/50 px-3 py-2.5 text-sm focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors"
                value={jobForm.tag_filter}
                onChange={e => setJobForm(s => ({ ...s, tag_filter: e.target.value }))}
              >
                <option value="" disabled>Select tag</option>
                {(tags || []).map(tag => (
                  <option key={tag} value={tag}>{tag}</option>
                ))}
              </select>
            )}

            {jobForm.target_mode === 'all' && (
              <p className="text-xs text-gray-500 dark:text-zinc-500">Job will run on all {hosts.length} registered host{hosts.length !== 1 ? 's' : ''}.</p>
            )}
          </div>

          {/* Row 3: Schedule */}
          <div>
            <div className="flex items-center gap-2 mb-2">
              <label className="block text-xs text-gray-500 dark:text-zinc-500">Schedule</label>
              <div className="relative" ref={cronHelpRef}>
                <button
                  type="button"
                  onClick={() => setShowCronHelp(v => !v)}
                  className="text-gray-400 dark:text-zinc-500 hover:text-gray-600 dark:hover:text-zinc-300 transition-colors text-xs"
                  title="Cron syntax cheatsheet"
                >
                  ?
                </button>
                {showCronHelp && (
                  <div className="absolute left-0 top-6 z-50 w-80 sm:w-96 rounded-xl border border-gray-200 dark:border-zinc-700 bg-white dark:bg-zinc-900 shadow-xl p-4 text-xs">
                    <div className="flex items-center justify-between mb-3">
                      <span className="font-semibold text-sm text-gray-800 dark:text-zinc-100">Cron Syntax Cheatsheet</span>
                      <button onClick={() => setShowCronHelp(false)} className="text-gray-400 dark:text-zinc-500 hover:text-gray-600 dark:hover:text-zinc-300">✕</button>
                    </div>

                    {/* Field reference */}
                    <div className="grid grid-cols-5 gap-1 mb-3 text-center">
                      {[
                        { label: 'Min', range: '0–59' },
                        { label: 'Hour', range: '0–23' },
                        { label: 'Day', range: '1–31' },
                        { label: 'Month', range: '1–12' },
                        { label: 'Weekday', range: '0–6' },
                      ].map((f, i) => (
                        <div key={i} className="rounded-md bg-gray-100 dark:bg-zinc-800 border border-gray-200 dark:border-zinc-700/50 px-1 py-1.5">
                          <div className="font-medium text-gray-700 dark:text-zinc-200 text-[11px]">{f.label}</div>
                          <div className="text-gray-400 dark:text-zinc-500 text-[10px]">{f.range}</div>
                        </div>
                      ))}
                    </div>
                    <p className="text-[10px] text-gray-400 dark:text-zinc-600 mb-3 text-center">Weekday: 0 = Sun, 1 = Mon … 6 = Sat</p>

                    {/* Operators */}
                    <div className="grid grid-cols-2 gap-x-4 gap-y-1 mb-3">
                      {[
                        { sym: '*', desc: 'any value' },
                        { sym: ',', desc: 'list (1,15)' },
                        { sym: '-', desc: 'range (1-5)' },
                        { sym: '/', desc: 'step (*/6 = every 6th)' },
                      ].map((op, i) => (
                        <div key={i} className="flex items-center gap-2">
                          <span className="font-mono text-emerald-500 dark:text-emerald-400 w-4 text-center font-bold">{op.sym}</span>
                          <span className="text-gray-600 dark:text-zinc-400">{op.desc}</span>
                        </div>
                      ))}
                    </div>

                    {/* Examples */}
                    <p className="text-gray-500 dark:text-zinc-500 font-medium mb-1.5">Common examples</p>
                    <div className="space-y-1 text-gray-600 dark:text-zinc-400">
                      {[
                        ['0 * * * *', 'Every hour'],
                        ['0 */6 * * *', 'Every 6 hours'],
                        ['0 0 * * *', 'Daily at midnight'],
                        ['0 2 * * 1', 'Weekly Mon 2am'],
                        ['30 4 1 * *', '1st of month 4:30am'],
                        ['0 9-17 * * 1-5', 'Hourly, work hours M–F'],
                        ['*/15 * * * *', 'Every 15 minutes'],
                      ].map(([expr, desc], i) => (
                        <div key={i} className="flex gap-3 items-baseline">
                          <code
                            className="font-mono text-emerald-500 dark:text-emerald-400 bg-gray-100 dark:bg-zinc-800 rounded px-1.5 py-0.5 text-[11px] whitespace-nowrap cursor-pointer hover:bg-gray-200 dark:hover:bg-zinc-700 transition-colors"
                            title="Click to use"
                            onClick={() => { setJobForm(s => ({ ...s, cron_expr: expr })); setShowCronHelp(false) }}
                          >{expr}</code>
                          <span>{desc}</span>
                        </div>
                      ))}
                    </div>
                    <p className="text-gray-400 dark:text-zinc-600 mt-3">Shortcuts: <code className="font-mono text-[11px]">@hourly @daily @weekly @monthly</code></p>
                  </div>
                )}
              </div>
            </div>
            <div className="flex gap-2 mb-2 flex-wrap">
              {CRON_PRESETS.map(preset => (
                <button
                  key={preset.value}
                  type="button"
                  onClick={() => setJobForm(s => ({ ...s, cron_expr: preset.value }))}
                  className={`rounded-lg px-2.5 py-1 text-xs border transition-colors ${
                    jobForm.cron_expr === preset.value
                      ? 'border-emerald-600 text-emerald-400 bg-emerald-950/30'
                      : 'border-gray-300 dark:border-zinc-700 text-gray-500 dark:text-zinc-400 hover:border-gray-400 dark:hover:border-zinc-500 hover:text-gray-700 dark:hover:text-zinc-200'
                  }`}
                >
                  {preset.label}
                </button>
              ))}
            </div>
            <input
              className="rounded-lg border border-gray-300 dark:border-zinc-700 bg-gray-100 dark:bg-zinc-800/50 px-3 py-2.5 text-sm font-mono placeholder-gray-400 dark:placeholder-zinc-500 focus:outline-none focus:border-gray-400 dark:focus:border-zinc-500 transition-colors w-full sm:w-auto"
              placeholder="Cron expression"
              value={jobForm.cron_expr}
              onChange={e => setJobForm(s => ({ ...s, cron_expr: e.target.value }))}
              required
            />
            {(() => {
              const cronErr = validateCronExpression(jobForm.cron_expr.trim())
              if (!cronErr || !jobForm.cron_expr.trim()) return null
              return <p className="text-xs text-amber-400 mt-1">⚠ {cronErr}</p>
            })()}
          </div>

          {/* Preview */}
          <div className="rounded-lg border border-gray-200 dark:border-zinc-800 bg-gray-50 dark:bg-zinc-900/60 px-3 py-2">
            <p className="text-xs text-gray-500 dark:text-zinc-500">
              Preview: <span className="text-gray-700 dark:text-zinc-300">{previewSummary}</span>
            </p>
          </div>

          <div className="flex items-center gap-2">
            <button
              type="submit"
              disabled={jobBusy || !hosts.length}
              className="rounded-lg px-4 py-2 bg-gray-900 dark:bg-white text-white dark:text-zinc-900 text-sm font-medium hover:bg-gray-800 dark:hover:bg-zinc-200 disabled:opacity-50 transition-colors"
            >
              {jobBusy ? 'Creating…' : 'Create schedule'}
            </button>
            <button
              type="button"
              onClick={() => setJobComposerOpen(false)}
              className="rounded-lg px-4 py-2 border border-gray-300 dark:border-zinc-700 text-sm text-gray-500 dark:text-zinc-400 hover:text-gray-700 dark:hover:text-zinc-200 transition-colors"
            >
              Cancel
            </button>
          </div>
        </form>
      )}

      {/* Job list */}
      {jobs.length === 0 ? (
        <div className="rounded-xl border border-dashed border-gray-300 dark:border-zinc-700 p-8 text-center">
          <p className="text-3xl mb-3">📋</p>
          <p className="text-gray-700 dark:text-zinc-300 mb-1">No scheduled jobs</p>
          <p className="text-sm text-gray-400 dark:text-zinc-600 mb-4">Create a job to automate scans and updates</p>
          {hosts.length > 0 && (
            <button
              onClick={() => setJobComposerOpen(true)}
              className="rounded-lg px-4 py-2 bg-gray-900 dark:bg-white text-white dark:text-zinc-900 text-sm font-medium hover:bg-gray-800 dark:hover:bg-zinc-200 transition-colors"
            >
              + Add schedule
            </button>
          )}
        </div>
      ) : (
        <div className="space-y-2">
          {jobs.map(j => {
            const busy = !!actionBusy[`job:${j.id}`]
            const deleteBusy = !!actionBusy[`job:delete:${j.id}`]
            const targetLabel = jobTargetLabel(j, hosts)
            return (
              <div
                key={j.id}
                className="rounded-xl border border-gray-200 dark:border-zinc-800 bg-white dark:bg-zinc-900/40 px-5 py-3 flex items-center justify-between gap-4 flex-wrap"
              >
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium truncate">{j.name || `${jobModeLabel(j.mode)} on ${targetLabel}`}</p>
                  <p className="text-xs text-gray-500 dark:text-zinc-500 mt-0.5">
                    {targetLabel} · <span className="font-mono">{j.cron_expr}</span> · {jobModeLabel(j.mode)}
                  </p>
                </div>
                <div className="flex items-center gap-2 flex-shrink-0">
                  <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-[11px] ${j.enabled ? 'text-emerald-400' : 'text-gray-500 dark:text-zinc-500'}`}>
                    {j.enabled ? '● Enabled' : '○ Disabled'}
                  </span>
                  <button
                    onClick={() => onToggleJob(j, !j.enabled)}
                    disabled={busy || deleteBusy}
                    className="rounded-lg px-3 py-1.5 text-xs border border-gray-300 dark:border-zinc-700 text-gray-700 dark:text-zinc-300 hover:text-gray-900 dark:hover:text-white hover:border-gray-400 dark:hover:border-zinc-500 disabled:opacity-30 transition-colors"
                  >
                    {busy ? 'Updating…' : j.enabled ? 'Disable' : 'Enable'}
                  </button>
                  <button
                    onClick={() => {
                      const jobLabel = j.name || `${jobModeLabel(j.mode)} on ${targetLabel}`
                      setConfirmDialog({
                        title: 'Delete scheduled job',
                        message: `Delete job "${jobLabel}"? This cannot be undone.`,
                        color: 'red',
                        payload: j
                      })
                    }}
                    disabled={busy || deleteBusy}
                    className="rounded-lg px-3 py-1.5 text-xs border border-red-800/60 text-red-400 hover:text-red-300 hover:border-red-700 disabled:opacity-30 transition-colors"
                  >
                    {deleteBusy ? 'Deleting…' : 'Delete'}
                  </button>
                </div>
              </div>
            )
          })}
        </div>
      )}

      {error && <p className="text-sm text-red-500 dark:text-red-400 mt-4">{error}</p>}

      <ConfirmDialog
        open={!!confirmDialog}
        title={confirmDialog?.title || ''}
        message={confirmDialog?.message || ''}
        confirmLabel="Delete"
        confirmColor="red"
        onConfirm={async () => {
          const job = confirmDialog?.payload
          setConfirmDialog(null)
          if (job) {
            const result = await onDeleteJob(job)
            if (result?.ok) {
              toast.addToast({ type: 'success', message: `Job deleted.` })
            } else if (result?.error) {
              toast.addToast({ type: 'error', message: result.error, duration: 6000 })
            }
          }
        }}
        onCancel={() => setConfirmDialog(null)}
      />
    </div>
  )
}
