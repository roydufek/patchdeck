package models

import "time"

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Role         string    `json:"role"`
	PasswordHash string    `json:"-"`
	TOTPSecret   string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

type Host struct {
	ID                    string                `json:"id"`
	Name                  string                `json:"name"`
	Address               string                `json:"address"`
	Port                  int                   `json:"port"`
	SSHUser               string                `json:"ssh_user"`
	AuthType              string                `json:"auth_type"`
	SecretCipher          string                `json:"-"`
	CreatedAt             time.Time             `json:"created_at"`
	NotificationPrefs     HostNotificationPrefs `json:"notification_prefs"`
	ChecksEnabled         bool                  `json:"checks_enabled"`
	AutoUpdatePolicy      string                `json:"auto_update_policy"`
	HostKeyRequired       bool                  `json:"host_key_required"`
	HostKeyTrustMode      string                `json:"host_key_trust_mode"`
	HostKeyPinned         string                `json:"host_key_pinned_fingerprint,omitempty"`
	HostKeyTrusted        string                `json:"host_key_trusted_fingerprint,omitempty"`
	HostKeyPending        string                `json:"host_key_pending_fingerprint,omitempty"`
	HostKeyLastVerifiedAt *time.Time            `json:"host_key_last_verified_at,omitempty"`
	Tags                  []string              `json:"tags"`
}

// ScanHistoryEntry represents a historical scan snapshot for a host.
// PackageInfo represents a single upgradable package with version details.
type PackageInfo struct {
	Name           string `json:"name"`
	CurrentVersion string `json:"current_version,omitempty"`
	NewVersion     string `json:"new_version,omitempty"`
	Arch           string `json:"arch,omitempty"`
	Source         string `json:"source,omitempty"`
	// DeferReason is set only on deferred packages: "phased" (Ubuntu phased rollout) or
	// "held" (kept back). Empty for normal/actionable packages.
	DeferReason string `json:"defer_reason,omitempty"`
}

type ScanHistoryEntry struct {
	ID               string        `json:"id"`
	HostID           string        `json:"host_id"`
	Packages         []PackageInfo `json:"packages"`
	NeedsReboot      bool          `json:"needs_reboot"`
	RebootReason     string        `json:"reboot_reason,omitempty"`
	NeedsRestart     []string      `json:"needs_restart"`
	NeedrestartFound bool          `json:"needrestart_found"`
	RawOutput        string        `json:"raw_output"`
	CreatedAt        time.Time     `json:"created_at"`
}

type ScanResult struct {
	HostID           string        `json:"host_id"`
	Packages         []PackageInfo `json:"packages"`
	// DeferredPackages are upgradable but won't be installed by Apply (apt-get
	// dist-upgrade) right now — e.g. Ubuntu phased rollout. Kept out of Packages so
	// they don't inflate the "needs action" count; surfaced separately in the UI.
	DeferredPackages []PackageInfo `json:"deferred_packages,omitempty"`
	NeedsReboot      bool          `json:"needs_reboot"`
	RebootReason     string        `json:"reboot_reason,omitempty"`
	NeedsRestart     []string      `json:"needs_restart"`
	NeedrestartFound bool          `json:"needrestart_found"`
	AptUpdateFailed  bool          `json:"apt_update_failed,omitempty"`
	RawOutput        string        `json:"raw_output"`
	OsName           string        `json:"os_name,omitempty"`
	OsVersion        string        `json:"os_version,omitempty"`
	Uptime           string        `json:"uptime,omitempty"`
	Kernel           string        `json:"kernel,omitempty"`
}

type ScanSnapshot struct {
	HostID           string        `json:"host_id"`
	HostName         string        `json:"host_name"`
	Packages         []PackageInfo `json:"packages"`
	DeferredPackages []PackageInfo `json:"deferred_packages,omitempty"`
	NeedsReboot      bool          `json:"needs_reboot"`
	RebootReason     string        `json:"reboot_reason,omitempty"`
	NeedsRestart     []string      `json:"needs_restart"`
	NeedrestartFound bool          `json:"needrestart_found"`
	OsName           string        `json:"os_name,omitempty"`
	OsVersion        string        `json:"os_version,omitempty"`
	Uptime           string        `json:"uptime,omitempty"`
	Kernel           string        `json:"kernel,omitempty"`
	UpdatedAt        time.Time     `json:"updated_at"`
}

type ApplyResult struct {
	ChangedPackages int    `json:"changed_packages"`
	RawOutput       string `json:"raw_output"`
	NeedsReboot     bool   `json:"needs_reboot"`
}

type RestartResult struct {
	Services []string `json:"services"`
	Success  bool     `json:"success"`
	Output   string   `json:"output"`
	// RebootRequired lists units that were NOT restarted because a live restart would be
	// destructive (e.g. dbus/logind — it severs the session bus and locks out SSH) and no
	// needrestart coordinated-restart handler was available. The operator must reboot to apply.
	RebootRequired []string `json:"reboot_required,omitempty"`
}

type Job struct {
	ID        string   `json:"id"`
	HostID    string   `json:"host_id,omitempty"`       // legacy single-host (kept for compat)
	HostIDs   []string `json:"host_ids,omitempty"`      // multi-host selection
	TagFilter string   `json:"tag_filter,omitempty"`    // run on all hosts with this tag
	HostName  string   `json:"host_name,omitempty"`
	Name      string   `json:"name"`
	CronExpr  string   `json:"cron_expr"`
	Mode      string   `json:"mode"` // scan|apply|scan_apply
	Enabled   bool     `json:"enabled"`

	// Computed (not persisted on the jobs row): populated when listing jobs.
	LastRun *JobRun    `json:"last_run,omitempty"`
	NextRun *time.Time `json:"next_run,omitempty"`
}

// JobRun is one execution of a scheduled job (persisted for history/observability).
type JobRun struct {
	ID          string     `json:"id"`
	JobID       string     `json:"job_id"`
	Mode        string     `json:"mode"`
	Status      string     `json:"status"` // running|success|partial|failed
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	HostsTotal  int        `json:"hosts_total"`
	HostsOK     int        `json:"hosts_ok"`
	HostsFailed int        `json:"hosts_failed"`
	Detail      string     `json:"detail,omitempty"`
}

type NotificationSettings struct {
	AppriseURL       string `json:"apprise_url"`
	UpdatesAvailable bool   `json:"updates_available"`
	AutoApplySuccess bool   `json:"auto_apply_success"`
	AutoApplyFailure bool   `json:"auto_apply_failure"`
	ScanFailure      bool   `json:"scan_failure"`
}

// OIDCConfig is the persisted OpenID Connect configuration. ClientSecretEnc holds the
// AES-GCM ciphertext of the client secret (never the plaintext, never serialized to the
// API). The HTTP layer decrypts it only when building an authenticator.
type OIDCConfig struct {
	Enabled         bool
	Issuer          string
	ClientID        string
	ClientSecretEnc string
	BaseURL         string
	Allowed         string
	ButtonLabel     string
}

type HostNotificationPrefs struct {
	UpdatesAvailable bool `json:"updates_available"`
	AutoApplySuccess bool `json:"auto_apply_success"`
	AutoApplyFailure bool `json:"auto_apply_failure"`
	ScanFailure      bool `json:"scan_failure"`
}

type HostKeyAuditEvent struct {
	ID                  string    `json:"id"`
	HostID              string    `json:"host_id"`
	Event               string    `json:"event"`
	PreviousFingerprint string    `json:"previous_fingerprint,omitempty"`
	NewFingerprint      string    `json:"new_fingerprint,omitempty"`
	Note                string    `json:"note,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

type APIToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Revoked    bool       `json:"revoked"`
}

type ActivityEntry struct {
	ID        int64     `json:"id"`
	HostID    string    `json:"host_id,omitempty"`
	HostName  string    `json:"host_name,omitempty"`
	EventType string    `json:"event_type"`
	Summary   string    `json:"summary,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type RecoveryCode struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	CodeHash  string    `json:"-"`
	Used      bool      `json:"used"`
	CreatedAt time.Time `json:"created_at"`
}
