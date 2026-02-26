import { useState, useCallback, useMemo } from 'react'
import { createAuthedFetch } from '../api.js'
import { validateCronExpression } from '../utils/format.js'

export function useJobs(token, clearToken, hosts) {
  const [jobs, setJobs] = useState([])
  const [error, setError] = useState('')
  const [actionBusy, setActionBusy] = useState({})

  const [jobForm, setJobForm] = useState({
    host_ids: [],
    tag_filter: '',
    target_mode: 'hosts', // 'hosts' | 'tag' | 'all'
    name: '',
    cron_expr: '0 3 * * *',
    mode: 'scan'
  })
  const [jobBusy, setJobBusy] = useState(false)
  const [jobComposerOpen, setJobComposerOpen] = useState(false)

  const authedFetch = useMemo(() => {
    if (!token) return null
    return createAuthedFetch(token, clearToken)
  }, [token, clearToken])

  const loadJobs = useCallback(async () => {
    if (!authedFetch) return
    try {
      const resp = await authedFetch('/jobs')
      if (!resp.ok) throw new Error('Failed to load jobs')
      const data = await resp.json()
      setJobs(data)
    } catch (e) {
      setError(e.message || 'Failed to load jobs')
    }
  }, [authedFetch])

  const createJob = useCallback(async (e) => {
    e.preventDefault()
    setError('')
    if (!authedFetch) return

    const cronErr = validateCronExpression(jobForm.cron_expr.trim())
    if (cronErr) {
      setError(cronErr)
      return
    }

    // Build POST body based on target_mode
    const body = {
      name: jobForm.name.trim(),
      cron_expr: jobForm.cron_expr.trim(),
      mode: jobForm.mode.trim()
    }

    if (jobForm.target_mode === 'hosts') {
      if (jobForm.host_ids.length === 0) {
        setError('Please select at least one host.')
        return
      }
      body.host_ids = jobForm.host_ids
      // Backward compat: if single host, also send host_id
      if (jobForm.host_ids.length === 1) {
        body.host_id = jobForm.host_ids[0]
      }
    } else if (jobForm.target_mode === 'tag') {
      if (!jobForm.tag_filter.trim()) {
        setError('Please select a tag.')
        return
      }
      body.tag_filter = jobForm.tag_filter.trim()
    } else if (jobForm.target_mode === 'all') {
      // All hosts — send all host IDs
      const allIds = hosts.map(h => h.id)
      if (allIds.length === 0) {
        setError('No hosts available.')
        return
      }
      body.host_ids = allIds
      if (allIds.length === 1) {
        body.host_id = allIds[0]
      }
    }

    setJobBusy(true)
    try {
      const resp = await authedFetch('/jobs', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      })
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok) {
        throw new Error(data.error || 'Failed to create job')
      }
      setJobForm(s => ({ ...s, name: '', cron_expr: s.cron_expr || '0 3 * * *', host_ids: [], tag_filter: '' }))
      setJobComposerOpen(false)
      await loadJobs()
    } catch (e) {
      setError(e.message || 'Failed to create job')
    } finally {
      setJobBusy(false)
    }
  }, [authedFetch, jobForm, hosts, loadJobs])

  const toggleJob = useCallback(async (job, enabled) => {
    if (!authedFetch) return
    const key = `job:${job.id}`
    setActionBusy(prev => ({ ...prev, [key]: true }))
    setError('')
    try {
      const resp = await authedFetch(`/jobs/${job.id}/enabled`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ enabled })
      })
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok) throw new Error(data.error || 'Failed to update job')
      await loadJobs()
    } catch (e) {
      setError(e.message || 'Failed to update job')
    } finally {
      setActionBusy(prev => ({ ...prev, [key]: false }))
    }
  }, [authedFetch, loadJobs])

  const deleteJob = useCallback(async (job) => {
    if (!authedFetch) return { ok: false }

    const key = `job:delete:${job.id}`
    setActionBusy(prev => ({ ...prev, [key]: true }))
    setError('')
    try {
      const resp = await authedFetch(`/jobs/${job.id}`, { method: 'DELETE' })
      const data = await resp.json().catch(() => ({}))
      if (!resp.ok) throw new Error(data.error || 'Failed to delete job')
      await loadJobs()
      return { ok: true }
    } catch (e) {
      setError(e.message || 'Failed to delete job')
      return { ok: false, error: e.message }
    } finally {
      setActionBusy(prev => ({ ...prev, [key]: false }))
    }
  }, [authedFetch, loadJobs])

  const resetState = useCallback(() => {
    setJobs([])
  }, [])

  return {
    jobs, setJobs, error, setError, actionBusy,
    jobForm, setJobForm, jobBusy, jobComposerOpen, setJobComposerOpen,
    loadJobs, createJob, toggleJob, deleteJob, resetState
  }
}
