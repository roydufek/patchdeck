import React, { useState, useEffect, useCallback } from 'react'
import { useAuth } from './hooks/useAuth.js'
import { useHosts } from './hooks/useHosts.js'
import { useJobs } from './hooks/useJobs.js'
import { useSettings } from './hooks/useSettings.js'
import { useTOTP } from './hooks/useTOTP.js'
import useActivity from './hooks/useActivity.js'
import { API } from './api.js'
import Layout from './components/Layout.jsx'
import LoginPage from './components/LoginPage.jsx'
import Dashboard from './components/Dashboard.jsx'
import HostDrawer from './components/HostDrawer.jsx'
import JobsPage from './components/JobsPage.jsx'
import SettingsPage from './components/SettingsPage.jsx'
import ActivityPage from './components/ActivityPage.jsx'
import { ToastProvider } from './components/Toast.jsx'

function AppInner() {
  const [page, setPage] = useState('hosts')

  // Auth
  const auth = useAuth()

  // Hosts
  const hostsHook = useHosts(auth.token, auth.clearToken)

  // Jobs
  const jobsHook = useJobs(auth.token, auth.clearToken, hostsHook.hosts)

  // Settings
  const settingsHook = useSettings(auth.token, auth.clearToken)

  // TOTP
  const totpHook = useTOTP(auth.token)

  // Activity
  const activityHook = useActivity(auth.token)

  // Tags
  const [tags, setTags] = useState([])

  // Theme is managed by useTheme hook in Layout

  // Load data when authenticated
  useEffect(() => {
    if (!auth.token) return
    Promise.all([
      hostsHook.loadData(),
      jobsHook.loadJobs(),
      settingsHook.loadSettings(),
      settingsHook.loadTokens(),
      fetch(`${API}/tags`, { headers: { Authorization: `Bearer ${auth.token}` } })
        .then(r => r.ok ? r.json() : []).catch(() => [])
    ]).then(([hostRows, , , , tagsData]) => {
      setTags(Array.isArray(tagsData) ? tagsData : [])
    })
  }, [auth.token])

  // Load TOTP status when viewing settings
  useEffect(() => {
    if (page === 'settings' && auth.token) {
      totpHook.fetchStatus()
    }
  }, [page, auth.token])

  // Sync job mode with host eligibility
  // (no longer needed — removed auto_update_policy gating)

  // Host drawer state
  const [hostFormOpen, setHostFormOpen] = useState(false)
  const [editingHostId, setEditingHostId] = useState('')
  const [hostForm, setHostForm] = useState({
    name: '', address: '', port: 22, ssh_user: '',
    auth_type: 'key', password: '', private_key_pem: '',
    passphrase: '', sudo_password: '',
    host_key_trust_mode: 'tofu', host_key_pinned_fingerprint: '',
    tags_input: ''
  })
  const [hostBusy, setHostBusy] = useState(false)
  const [hostFormError, setHostFormError] = useState('')

  const [hostDetailsOpen, setHostDetailsOpen] = useState({})

  function resetHostForm() {
    setHostForm({
      name: '', address: '', port: 22, ssh_user: '',
      auth_type: 'key', password: '', private_key_pem: '',
      passphrase: '', sudo_password: '',
      host_key_trust_mode: 'tofu', host_key_pinned_fingerprint: '',
      tags_input: ''
    })
    setEditingHostId('')
    setHostFormOpen(false)
    setHostFormError('')
  }

  function startAddHost() {
    setEditingHostId('')
    setHostForm({
      name: '', address: '', port: 22, ssh_user: '',
      auth_type: 'key', password: '', private_key_pem: '',
      passphrase: '', sudo_password: '',
      host_key_trust_mode: 'tofu', host_key_pinned_fingerprint: '',
      tags_input: ''
    })
    setHostFormError('')
    setHostFormOpen(true)
  }

  function beginEditHost(host) {
    setEditingHostId(host.id)
    setHostForm({
      name: host.name || '',
      address: host.address || '',
      port: host.port || 22,
      ssh_user: host.ssh_user || '',
      auth_type: host.auth_type || 'key',
      password: '', private_key_pem: '', passphrase: '',
      sudo_password: '',
      host_key_trust_mode: host.host_key_trust_mode || 'tofu',
      host_key_pinned_fingerprint: host.host_key_pinned_fingerprint || '',
      tags_input: Array.isArray(host.tags) ? host.tags.join(', ') : ''
    })
    setHostFormError('')
    setHostFormOpen(true)
  }

  async function handleCreateHost(e) {
    e.preventDefault()
    setHostBusy(true)
    setHostFormError('')
    try {
      const formToSend = { ...hostForm }
      // Convert tags_input to tags array
      formToSend.tags = (formToSend.tags_input || '').split(',').map(t => t.trim()).filter(Boolean)
      delete formToSend.tags_input
      await hostsHook.createHost(formToSend, editingHostId)
      resetHostForm()
    } catch (e) {
      setHostFormError(e.message || (editingHostId ? 'Failed to update host' : 'Failed to add host'))
    } finally {
      setHostBusy(false)
    }
  }

  function handleRefreshAll() {
    hostsHook.loadData()
    jobsHook.loadJobs()
    settingsHook.loadSettings()
  }

  function handleLogout() {
    auth.logout()
    hostsHook.resetState()
    jobsHook.resetState()
    setHostDetailsOpen({})
  }

  // Merge errors
  const globalError = hostsHook.error || jobsHook.error || settingsHook.error || auth.error || ''

  // Not authenticated
  if (!auth.token) {
    return (
      <LoginPage
        setupLoading={auth.setupLoading}
        setupStatus={auth.setupStatus}
        login={auth.login}
        setLogin={auth.setLogin}
        loginBusy={auth.loginBusy}
        doLogin={auth.doLogin}
        totpRequired={auth.totpRequired}
        cancelTotp={auth.cancelTotp}
        bootstrapForm={auth.bootstrapForm}
        setBootstrapForm={auth.setBootstrapForm}
        bootstrapBusy={auth.bootstrapBusy}
        doBootstrap={auth.doBootstrap}
        bootstrapDone={auth.bootstrapDone}
        error={auth.error}
      />
    )
  }

  return (
    <>
      <Layout
        currentPage={page}
        onNavigate={setPage}
        onLogout={handleLogout}
        onRefresh={handleRefreshAll}
        loading={hostsHook.loading}
      >
        {globalError && (
          <div className="mb-4 rounded-lg border border-red-300 dark:border-red-800/50 bg-red-50 dark:bg-red-950/30 px-4 py-2.5 text-sm text-red-600 dark:text-red-400">
            {globalError}
          </div>
        )}

        {page === 'hosts' && (
          <Dashboard
            hosts={hostsHook.hosts}
            scanByHost={hostsHook.scanByHost}
            connectivityByHost={hostsHook.connectivityByHost}
            hostActionState={hostsHook.hostActionState}
            hostActionError={hostsHook.hostActionError}
            actionBusy={hostsHook.actionBusy}
            loading={hostsHook.loading}
            onScan={(hostId) => hostsHook.hostAction(hostId, 'scan')}
            onApply={(hostId) => hostsHook.hostAction(hostId, 'apply')}
            onRefreshConnectivity={(hostId) => hostsHook.refreshConnectivity(hostId)}
            onDeleteHost={(host) => hostsHook.deleteHost(host)}
            onEditHost={beginEditHost}
            onAddHost={startAddHost}
            hostDetailsOpen={hostDetailsOpen}
            onToggleDetails={(hostId) => setHostDetailsOpen(prev => ({ ...prev, [hostId]: !prev[hostId] }))}
            onUpdateHostOps={hostsHook.updateHostOps}
            onUpdateHostKeyPolicy={hostsHook.updateHostKeyPolicy}
            onResolveHostKeyMismatch={hostsHook.resolveHostKeyMismatch}
            onUpdateHostNotificationPrefs={hostsHook.updateHostNotificationPrefs}
            onLoadHostKeyAudit={hostsHook.loadHostKeyAudit}
            hostKeyAuditByHost={hostsHook.hostKeyAuditByHost}
            onRestartServices={(hostId, services) => hostsHook.restartServices(hostId, services)}
            onReboot={(hostId) => hostsHook.rebootHost(hostId)}
            onShutdown={(hostId) => hostsHook.shutdownHost(hostId)}
            error=""
            onScanStream={(hostId) => hostsHook.hostActionStream(hostId, 'scan', auth.token)}
            onApplyStream={(hostId) => hostsHook.hostActionStream(hostId, 'apply', auth.token)}
            onCloseStream={hostsHook.closeStream}
            streamHostId={hostsHook.streamHostId}
            streamMode={hostsHook.streamMode}
            streamOutput={hostsHook.streamOutput}
            streamPhase={hostsHook.streamPhase}
            streamProgress={hostsHook.streamProgress}
            streamIsStreaming={hostsHook.streamIsStreaming}
            streamError={hostsHook.streamError}
            streamResult={hostsHook.streamResult}
            recoveryMonitor={hostsHook.recoveryMonitor}
            postApplyPrompt={hostsHook.postApplyPrompt}
            onDismissPostApplyPrompt={hostsHook.dismissPostApplyPrompt}
            onRefreshAll={handleRefreshAll}
            token={auth.token}
          />
        )}

        {page === 'jobs' && (
          <JobsPage
            jobs={jobsHook.jobs}
            hosts={hostsHook.hosts}
            tags={tags}
            actionBusy={jobsHook.actionBusy}
            jobForm={jobsHook.jobForm}
            setJobForm={jobsHook.setJobForm}
            jobBusy={jobsHook.jobBusy}
            jobComposerOpen={jobsHook.jobComposerOpen}
            setJobComposerOpen={jobsHook.setJobComposerOpen}
            onCreateJob={jobsHook.createJob}
            onToggleJob={jobsHook.toggleJob}
            onDeleteJob={jobsHook.deleteJob}
            error=""
          />
        )}

        {page === 'activity' && (
          <ActivityPage
            activity={activityHook}
            hosts={hostsHook.hosts}
            onLoadActivity={activityHook.loadActivity}
          />
        )}

        {page === 'settings' && (
          <SettingsPage
            notificationSettings={settingsHook.notificationSettings}
            setNotificationSettings={settingsHook.setNotificationSettings}
            notificationRuntime={settingsHook.notificationRuntime}
            settingsBusy={settingsHook.settingsBusy}
            onSave={settingsHook.saveNotificationSettings}
            onTest={settingsHook.sendNotificationTest}
            tokens={settingsHook.tokens}
            tokensBusy={settingsHook.tokensBusy}
            newToken={settingsHook.newToken}
            onClearNewToken={settingsHook.clearNewToken}
            onCreateToken={settingsHook.createToken}
            onRevokeToken={settingsHook.revokeToken}
            auditRetentionDays={settingsHook.auditRetentionDays}
            setAuditRetentionDays={settingsHook.setAuditRetentionDays}
            auditBusy={settingsHook.auditBusy}
            onSaveAuditRetention={settingsHook.saveAuditRetention}
            onExportActivity={settingsHook.exportActivityCSV}
            totpStatus={totpHook.totpStatus}
            setupData={totpHook.setupData}
            recoveryCodes={totpHook.recoveryCodes}
            totpBusy={totpHook.totpBusy}
            totpError={totpHook.totpError}
            onTotpStartSetup={totpHook.startSetup}
            onTotpConfirm={totpHook.confirmSetup}
            onTotpDisable={totpHook.disableTOTP}
            onTotpCancelSetup={totpHook.cancelSetup}
            onTotpDismissRecoveryCodes={totpHook.dismissRecoveryCodes}
            error=""
          />
        )}
      </Layout>

      <HostDrawer
        open={hostFormOpen}
        editingHostId={editingHostId}
        hostForm={hostForm}
        setHostForm={setHostForm}
        onSubmit={handleCreateHost}
        onClose={resetHostForm}
        hostBusy={hostBusy}
        error={hostFormError}
      />
    </>
  )
}

export default function App() {
  return (
    <ToastProvider>
      <AppInner />
    </ToastProvider>
  )
}
