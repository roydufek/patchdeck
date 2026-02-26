package db

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"patchdeck/api/internal/models"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var ErrHostExists = errors.New("host already exists")

func Migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY, username TEXT UNIQUE NOT NULL, role TEXT NOT NULL DEFAULT 'admin', password_hash TEXT NOT NULL, totp_secret TEXT NOT NULL, created_at DATETIME NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS hosts (id TEXT PRIMARY KEY, name TEXT NOT NULL, address TEXT NOT NULL, port INTEGER NOT NULL, ssh_user TEXT NOT NULL, auth_type TEXT NOT NULL, secret_cipher TEXT NOT NULL, checks_enabled INTEGER NOT NULL DEFAULT 1, auto_update_policy TEXT NOT NULL DEFAULT 'manual', host_key_required INTEGER NOT NULL DEFAULT 1, host_key_trust_mode TEXT NOT NULL DEFAULT 'tofu', host_key_pinned_fingerprint TEXT NOT NULL DEFAULT '', host_key_trusted_fingerprint TEXT NOT NULL DEFAULT '', host_key_pending_fingerprint TEXT NOT NULL DEFAULT '', host_key_last_verified_at DATETIME, created_at DATETIME NOT NULL);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_hosts_unique_target ON hosts(address, port, ssh_user);`,
		`CREATE TABLE IF NOT EXISTS scans (host_id TEXT PRIMARY KEY, packages_json TEXT NOT NULL, needs_reboot INTEGER NOT NULL, needs_restart_json TEXT NOT NULL, needrestart_found INTEGER NOT NULL, raw_output TEXT NOT NULL, updated_at DATETIME NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS jobs (id TEXT PRIMARY KEY, host_id TEXT NOT NULL, name TEXT, cron_expr TEXT NOT NULL, mode TEXT NOT NULL DEFAULT 'scan', enabled INTEGER NOT NULL DEFAULT 1);`,
		`CREATE TABLE IF NOT EXISTS app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS host_notification_prefs (host_id TEXT PRIMARY KEY, updates_available INTEGER NOT NULL DEFAULT 1, auto_apply_success INTEGER NOT NULL DEFAULT 1, auto_apply_failure INTEGER NOT NULL DEFAULT 1, scan_failure INTEGER NOT NULL DEFAULT 1);`,
		`CREATE TABLE IF NOT EXISTS host_key_audit (id TEXT PRIMARY KEY, host_id TEXT NOT NULL, event TEXT NOT NULL, previous_fingerprint TEXT NOT NULL DEFAULT '', new_fingerprint TEXT NOT NULL DEFAULT '', note TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL);`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	if err := ensureHostColumn(db, "checks_enabled", "INTEGER NOT NULL DEFAULT 1"); err != nil {
		return err
	}
	if err := ensureHostColumn(db, "auto_update_policy", "TEXT NOT NULL DEFAULT 'manual'"); err != nil {
		return err
	}
	if err := ensureHostColumn(db, "host_key_required", "INTEGER NOT NULL DEFAULT 1"); err != nil {
		return err
	}
	if err := ensureHostColumn(db, "host_key_trust_mode", "TEXT NOT NULL DEFAULT 'tofu'"); err != nil {
		return err
	}
	if err := ensureHostColumn(db, "host_key_pinned_fingerprint", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureHostColumn(db, "host_key_trusted_fingerprint", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureHostColumn(db, "host_key_pending_fingerprint", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureHostColumn(db, "host_key_last_verified_at", "DATETIME"); err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE hosts SET host_key_required=1 WHERE host_key_required<>1`); err != nil {
		return err
	}
	if err := ensureUserColumn(db, "role", "TEXT NOT NULL DEFAULT 'admin'"); err != nil {
		return err
	}
	if err := ensureTableColumn(db, "host_notification_prefs", "scan_failure", "INTEGER NOT NULL DEFAULT 1"); err != nil {
		return err
	}
	// scan_history table for audit log
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS scan_history (id TEXT PRIMARY KEY, host_id TEXT NOT NULL, packages_json TEXT NOT NULL, needs_reboot INTEGER NOT NULL, needs_restart_json TEXT NOT NULL, needrestart_found INTEGER NOT NULL, raw_output TEXT NOT NULL, created_at DATETIME NOT NULL)`); err != nil {
		return err
	}
	// tags column on hosts
	if err := ensureHostColumn(db, "tags_json", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return err
	}
	// api_tokens table
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS api_tokens (id TEXT PRIMARY KEY, name TEXT NOT NULL, token_hash TEXT NOT NULL, created_at DATETIME NOT NULL, last_used_at DATETIME, revoked INTEGER NOT NULL DEFAULT 0)`); err != nil {
		return err
	}
	// activity_log table
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS activity_log (id INTEGER PRIMARY KEY AUTOINCREMENT, host_id TEXT, host_name TEXT, event_type TEXT NOT NULL, summary TEXT, created_at DATETIME NOT NULL)`); err != nil {
		return err
	}
	// sysinfo columns on scans
	for _, col := range []string{"os_name", "os_version", "uptime", "kernel"} {
		if err := ensureTableColumn(db, "scans", col, "TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	// multi-host and tag_filter columns on jobs
	if err := ensureTableColumn(db, "jobs", "host_ids_json", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return err
	}
	if err := ensureTableColumn(db, "jobs", "tag_filter", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	// reboot_reason columns
	if err := ensureTableColumn(db, "scans", "reboot_reason", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureTableColumn(db, "scan_history", "reboot_reason", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return nil
}

func HasUsers(db *sql.DB) bool {
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM users`).Scan(&n)
	return n > 0
}

func CreateInitialUser(db *sql.DB, username, role, hash, totp string) error {
	role = strings.TrimSpace(strings.ToLower(role))
	if role == "" {
		role = "admin"
	}
	_, err := db.Exec(`INSERT INTO users(id,username,role,password_hash,totp_secret,created_at) VALUES(?,?,?,?,?,?)`, uuid.NewString(), username, role, hash, totp, time.Now().UTC())
	return err
}

func GetUserByUsername(db *sql.DB, username string) (models.User, error) {
	var u models.User
	err := db.QueryRow(`SELECT id,username,role,password_hash,totp_secret,created_at FROM users WHERE username=?`, username).Scan(&u.ID, &u.Username, &u.Role, &u.PasswordHash, &u.TOTPSecret, &u.CreatedAt)
	if u.Role == "" {
		u.Role = "admin"
	}
	return u, err
}

func CreateHost(db *sql.DB, h models.Host) (string, error) {
	checksEnabled := h.ChecksEnabled
	if !checksEnabled {
		checksEnabled = true
	}
	autoPolicy := strings.TrimSpace(h.AutoUpdatePolicy)
	if autoPolicy == "" {
		autoPolicy = "manual"
	}
	hostKeyMode := strings.ToLower(strings.TrimSpace(h.HostKeyTrustMode))
	if hostKeyMode == "" {
		hostKeyMode = "tofu"
	}
	hostID := uuid.NewString()
	_, err := db.Exec(`INSERT INTO hosts(id,name,address,port,ssh_user,auth_type,secret_cipher,checks_enabled,auto_update_policy,host_key_required,host_key_trust_mode,host_key_pinned_fingerprint,host_key_trusted_fingerprint,host_key_pending_fingerprint,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, hostID, h.Name, h.Address, h.Port, h.SSHUser, h.AuthType, h.SecretCipher, boolToInt(checksEnabled), autoPolicy, boolToInt(h.HostKeyRequired), hostKeyMode, strings.TrimSpace(h.HostKeyPinned), strings.TrimSpace(h.HostKeyTrusted), strings.TrimSpace(h.HostKeyPending), time.Now().UTC())
	if err != nil && isUniqueConstraintErr(err) {
		return "", ErrHostExists
	}
	if err != nil {
		return "", err
	}
	return hostID, nil
}

func ListHosts(db *sql.DB) ([]models.Host, error) {
	rows, err := db.Query(`SELECT id,name,address,port,ssh_user,auth_type,checks_enabled,auto_update_policy,host_key_required,host_key_trust_mode,host_key_pinned_fingerprint,host_key_trusted_fingerprint,host_key_pending_fingerprint,host_key_last_verified_at,tags_json,created_at FROM hosts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Host{}
	for rows.Next() {
		var h models.Host
		var checksEnabled, hostKeyRequired int
		var hostKeyLastVerified sql.NullTime
		var tagsJSON string
		if err := rows.Scan(&h.ID, &h.Name, &h.Address, &h.Port, &h.SSHUser, &h.AuthType, &checksEnabled, &h.AutoUpdatePolicy, &hostKeyRequired, &h.HostKeyTrustMode, &h.HostKeyPinned, &h.HostKeyTrusted, &h.HostKeyPending, &hostKeyLastVerified, &tagsJSON, &h.CreatedAt); err != nil {
			return nil, err
		}
		h.ChecksEnabled = checksEnabled == 1
		h.HostKeyRequired = hostKeyRequired == 1
		if h.AutoUpdatePolicy == "" {
			h.AutoUpdatePolicy = "manual"
		}
		if strings.TrimSpace(h.HostKeyTrustMode) == "" {
			h.HostKeyTrustMode = "tofu"
		}
		if hostKeyLastVerified.Valid {
			t := hostKeyLastVerified.Time
			h.HostKeyLastVerifiedAt = &t
		}
		h.Tags = []string{}
		if strings.TrimSpace(tagsJSON) != "" {
			_ = json.Unmarshal([]byte(tagsJSON), &h.Tags)
		}
		if h.Tags == nil {
			h.Tags = []string{}
		}
		prefs, err := GetHostNotificationPrefs(db, h.ID)
		if err == nil {
			h.NotificationPrefs = prefs
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func GetHost(db *sql.DB, id string) (models.Host, error) {
	var h models.Host
	var checksEnabled, hostKeyRequired int
	var hostKeyLastVerified sql.NullTime
	var tagsJSON string
	err := db.QueryRow(`SELECT id,name,address,port,ssh_user,auth_type,secret_cipher,checks_enabled,auto_update_policy,host_key_required,host_key_trust_mode,host_key_pinned_fingerprint,host_key_trusted_fingerprint,host_key_pending_fingerprint,host_key_last_verified_at,tags_json,created_at FROM hosts WHERE id=?`, id).Scan(&h.ID, &h.Name, &h.Address, &h.Port, &h.SSHUser, &h.AuthType, &h.SecretCipher, &checksEnabled, &h.AutoUpdatePolicy, &hostKeyRequired, &h.HostKeyTrustMode, &h.HostKeyPinned, &h.HostKeyTrusted, &h.HostKeyPending, &hostKeyLastVerified, &tagsJSON, &h.CreatedAt)
	if err != nil {
		return h, err
	}
	h.ChecksEnabled = checksEnabled == 1
	h.HostKeyRequired = hostKeyRequired == 1
	if h.AutoUpdatePolicy == "" {
		h.AutoUpdatePolicy = "manual"
	}
	if strings.TrimSpace(h.HostKeyTrustMode) == "" {
		h.HostKeyTrustMode = "tofu"
	}
	if hostKeyLastVerified.Valid {
		t := hostKeyLastVerified.Time
		h.HostKeyLastVerifiedAt = &t
	}
	h.Tags = []string{}
	if strings.TrimSpace(tagsJSON) != "" {
		_ = json.Unmarshal([]byte(tagsJSON), &h.Tags)
	}
	if h.Tags == nil {
		h.Tags = []string{}
	}
	prefs, err := GetHostNotificationPrefs(db, h.ID)
	if err == nil {
		h.NotificationPrefs = prefs
	}
	return h, nil
}

func UpdateHost(db *sql.DB, h models.Host) error {
	res, err := db.Exec(`UPDATE hosts SET name=?, address=?, port=?, ssh_user=?, auth_type=?, secret_cipher=? WHERE id=?`, h.Name, h.Address, h.Port, h.SSHUser, h.AuthType, h.SecretCipher, h.ID)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return ErrHostExists
		}
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func DeleteHost(db *sql.DB, id string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`DELETE FROM hosts WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	if _, err := tx.Exec(`DELETE FROM scans WHERE host_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM scan_history WHERE host_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM jobs WHERE host_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func UpsertScanResult(db *sql.DB, hostID string, sr models.ScanResult) error {
	pkg, _ := json.Marshal(sr.Packages)
	svc, _ := json.Marshal(sr.NeedsRestart)
	now := time.Now().UTC()
	_, err := db.Exec(`INSERT INTO scans(host_id,packages_json,needs_reboot,reboot_reason,needs_restart_json,needrestart_found,raw_output,os_name,os_version,uptime,kernel,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(host_id) DO UPDATE SET packages_json=excluded.packages_json,needs_reboot=excluded.needs_reboot,reboot_reason=excluded.reboot_reason,needs_restart_json=excluded.needs_restart_json,needrestart_found=excluded.needrestart_found,raw_output=excluded.raw_output,os_name=excluded.os_name,os_version=excluded.os_version,uptime=excluded.uptime,kernel=excluded.kernel,updated_at=excluded.updated_at`,
		hostID, string(pkg), boolToInt(sr.NeedsReboot), sr.RebootReason, string(svc), boolToInt(sr.NeedrestartFound), sr.RawOutput, sr.OsName, sr.OsVersion, sr.Uptime, sr.Kernel, now)
	if err != nil {
		return err
	}
	// Insert into scan_history
	_, err = db.Exec(`INSERT INTO scan_history(id,host_id,packages_json,needs_reboot,reboot_reason,needs_restart_json,needrestart_found,raw_output,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		uuid.NewString(), hostID, string(pkg), boolToInt(sr.NeedsReboot), sr.RebootReason, string(svc), boolToInt(sr.NeedrestartFound), sr.RawOutput, now)
	if err != nil {
		return err
	}
	// Prune to keep only the latest 10 per host
	_, err = db.Exec(`DELETE FROM scan_history WHERE host_id=? AND id NOT IN (SELECT id FROM scan_history WHERE host_id=? ORDER BY created_at DESC LIMIT 10)`, hostID, hostID)
	return err
}

func CreateJob(db *sql.DB, j models.Job) error {
	if j.Mode == "" {
		j.Mode = "scan"
	}
	hostIDsJSON := "[]"
	if len(j.HostIDs) > 0 {
		b, _ := json.Marshal(j.HostIDs)
		hostIDsJSON = string(b)
	}
	tagFilter := strings.TrimSpace(j.TagFilter)
	// For backward compat, if HostID is empty but HostIDs has entries, use first
	hostID := strings.TrimSpace(j.HostID)
	if hostID == "" && len(j.HostIDs) > 0 {
		hostID = j.HostIDs[0]
	}
	_, err := db.Exec(`INSERT INTO jobs(id,host_id,name,cron_expr,mode,enabled,host_ids_json,tag_filter) VALUES(?,?,?,?,?,?,?,?)`, uuid.NewString(), hostID, j.Name, j.CronExpr, j.Mode, boolToInt(j.Enabled), hostIDsJSON, tagFilter)
	return err
}

func UpdateJobEnabled(db *sql.DB, id string, enabled bool) error {
	res, err := db.Exec(`UPDATE jobs SET enabled=? WHERE id=?`, boolToInt(enabled), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func DeleteJob(db *sql.DB, id string) error {
	res, err := db.Exec(`DELETE FROM jobs WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func ListJobs(db *sql.DB) ([]models.Job, error) {
	rows, err := db.Query(`
		SELECT j.id, j.host_id, COALESCE(h.name,''), j.name, j.cron_expr, j.mode, j.enabled, j.host_ids_json, j.tag_filter
		FROM jobs j
		LEFT JOIN hosts h ON h.id = j.host_id
		ORDER BY j.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Job{}
	for rows.Next() {
		var j models.Job
		var enabled int
		var hostIDsJSON, tagFilter string
		if err := rows.Scan(&j.ID, &j.HostID, &j.HostName, &j.Name, &j.CronExpr, &j.Mode, &enabled, &hostIDsJSON, &tagFilter); err != nil {
			return nil, err
		}
		j.Enabled = enabled == 1
		j.TagFilter = strings.TrimSpace(tagFilter)
		j.HostIDs = []string{}
		if strings.TrimSpace(hostIDsJSON) != "" && hostIDsJSON != "[]" {
			_ = json.Unmarshal([]byte(hostIDsJSON), &j.HostIDs)
		}
		if j.HostIDs == nil {
			j.HostIDs = []string{}
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func GetNotificationSettings(db *sql.DB) (models.NotificationSettings, error) {
	s := models.NotificationSettings{UpdatesAvailable: true, AutoApplySuccess: true, AutoApplyFailure: true, ScanFailure: true}
	if err := db.QueryRow(`SELECT value FROM app_settings WHERE key='apprise_url'`).Scan(&s.AppriseURL); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return s, err
	}
	var err error
	s.UpdatesAvailable, err = getSettingBool(db, "notif_updates_available", true)
	if err != nil {
		return s, err
	}
	s.AutoApplySuccess, err = getSettingBool(db, "notif_auto_apply_success", true)
	if err != nil {
		return s, err
	}
	s.AutoApplyFailure, err = getSettingBool(db, "notif_auto_apply_failure", true)
	if err != nil {
		return s, err
	}
	s.ScanFailure, err = getSettingBool(db, "notif_scan_failure", true)
	if err != nil {
		return s, err
	}
	return s, nil
}

func UpsertNotificationSettings(db *sql.DB, s models.NotificationSettings) error {
	updates := []struct {
		key string
		val string
	}{
		{key: "apprise_url", val: strings.TrimSpace(s.AppriseURL)},
		{key: "notif_updates_available", val: boolString(s.UpdatesAvailable)},
		{key: "notif_auto_apply_success", val: boolString(s.AutoApplySuccess)},
		{key: "notif_auto_apply_failure", val: boolString(s.AutoApplyFailure)},
		{key: "notif_scan_failure", val: boolString(s.ScanFailure)},
	}
	for _, u := range updates {
		if _, err := db.Exec(`INSERT INTO app_settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, u.key, u.val); err != nil {
			return err
		}
	}
	return nil
}

func GetHostNotificationPrefs(db *sql.DB, hostID string) (models.HostNotificationPrefs, error) {
	var p models.HostNotificationPrefs
	var updatesAvailable, autoApplySuccess, autoApplyFailure, scanFailure int
	err := db.QueryRow(`SELECT updates_available, auto_apply_success, auto_apply_failure, scan_failure FROM host_notification_prefs WHERE host_id=?`, hostID).Scan(&updatesAvailable, &autoApplySuccess, &autoApplyFailure, &scanFailure)
	if errors.Is(err, sql.ErrNoRows) {
		return models.HostNotificationPrefs{UpdatesAvailable: true, AutoApplySuccess: true, AutoApplyFailure: true, ScanFailure: true}, nil
	}
	if err != nil {
		return p, err
	}
	p.UpdatesAvailable = updatesAvailable == 1
	p.AutoApplySuccess = autoApplySuccess == 1
	p.AutoApplyFailure = autoApplyFailure == 1
	p.ScanFailure = scanFailure == 1
	return p, nil
}

func UpsertHostNotificationPrefs(db *sql.DB, hostID string, p models.HostNotificationPrefs) error {
	_, err := db.Exec(`INSERT INTO host_notification_prefs(host_id,updates_available,auto_apply_success,auto_apply_failure,scan_failure) VALUES(?,?,?,?,?) ON CONFLICT(host_id) DO UPDATE SET updates_available=excluded.updates_available,auto_apply_success=excluded.auto_apply_success,auto_apply_failure=excluded.auto_apply_failure,scan_failure=excluded.scan_failure`, hostID, boolToInt(p.UpdatesAvailable), boolToInt(p.AutoApplySuccess), boolToInt(p.AutoApplyFailure), boolToInt(p.ScanFailure))
	return err
}

func UpdateHostOperationalControls(db *sql.DB, hostID string, checksEnabled bool, autoUpdatePolicy string) error {
	policy := strings.TrimSpace(strings.ToLower(autoUpdatePolicy))
	if policy == "" {
		policy = "manual"
	}
	res, err := db.Exec(`UPDATE hosts SET checks_enabled=?, auto_update_policy=? WHERE id=?`, boolToInt(checksEnabled), policy, hostID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func UpdateHostKeyPolicy(db *sql.DB, hostID string, required bool, trustMode string, pinnedFingerprint string) error {
	mode := strings.ToLower(strings.TrimSpace(trustMode))
	if mode == "" {
		mode = "tofu"
	}
	pinned := strings.TrimSpace(pinnedFingerprint)
	res, err := db.Exec(`UPDATE hosts SET host_key_required=?, host_key_trust_mode=?, host_key_pinned_fingerprint=? WHERE id=?`, boolToInt(required), mode, pinned, hostID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func RecordHostKeyAudit(db *sql.DB, hostID, event, previousFingerprint, newFingerprint, note string) error {
	_, err := db.Exec(`INSERT INTO host_key_audit(id,host_id,event,previous_fingerprint,new_fingerprint,note,created_at) VALUES(?,?,?,?,?,?,?)`, uuid.NewString(), hostID, strings.TrimSpace(event), strings.TrimSpace(previousFingerprint), strings.TrimSpace(newFingerprint), strings.TrimSpace(note), time.Now().UTC())
	return err
}

func ListHostKeyAuditEvents(db *sql.DB, hostID string, limit int) ([]models.HostKeyAuditEvent, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := db.Query(`SELECT id, host_id, event, previous_fingerprint, new_fingerprint, note, created_at FROM host_key_audit WHERE host_id=? ORDER BY created_at DESC LIMIT ?`, hostID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.HostKeyAuditEvent{}
	for rows.Next() {
		var e models.HostKeyAuditEvent
		if err := rows.Scan(&e.ID, &e.HostID, &e.Event, &e.PreviousFingerprint, &e.NewFingerprint, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func AcceptHostKeyPendingFingerprint(db *sql.DB, hostID string) error {
	res, err := db.Exec(`
		UPDATE hosts
		SET host_key_trusted_fingerprint = host_key_pending_fingerprint,
			host_key_pinned_fingerprint = CASE WHEN host_key_trust_mode='pinned' THEN host_key_pending_fingerprint ELSE host_key_pinned_fingerprint END,
			host_key_pending_fingerprint = '',
			host_key_last_verified_at = ?
		WHERE id=? AND TRIM(host_key_pending_fingerprint) <> ''`, time.Now().UTC(), hostID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func ClearHostKeyPendingFingerprint(db *sql.DB, hostID string) error {
	res, err := db.Exec(`UPDATE hosts SET host_key_pending_fingerprint='' WHERE id=?`, hostID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func TrustHostKeyFirstUse(db *sql.DB, hostID, fingerprint string) error {
	_, err := db.Exec(`UPDATE hosts SET host_key_trusted_fingerprint=?, host_key_pending_fingerprint='', host_key_last_verified_at=? WHERE id=?`, strings.TrimSpace(fingerprint), time.Now().UTC(), hostID)
	return err
}

func SetHostKeyPendingFingerprint(db *sql.DB, hostID, fingerprint string) error {
	_, err := db.Exec(`UPDATE hosts SET host_key_pending_fingerprint=? WHERE id=?`, strings.TrimSpace(fingerprint), hostID)
	return err
}

func MarkHostKeyVerified(db *sql.DB, hostID string) error {
	_, err := db.Exec(`UPDATE hosts SET host_key_last_verified_at=? WHERE id=?`, time.Now().UTC(), hostID)
	return err
}

// unmarshalPackages handles both old format (["pkg1","pkg2"]) and new format ([{"name":"pkg1",...}]).
func unmarshalPackages(raw string) []models.PackageInfo {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}
	// Try new object array format first
	var pkgs []models.PackageInfo
	if err := json.Unmarshal([]byte(raw), &pkgs); err == nil && len(pkgs) > 0 {
		return pkgs
	}
	// Fall back to old string array format
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err == nil {
		out := make([]models.PackageInfo, len(names))
		for i, n := range names {
			out[i] = models.PackageInfo{Name: n}
		}
		return out
	}
	return nil
}

func ListScanSnapshots(db *sql.DB) ([]models.ScanSnapshot, error) {
	rows, err := db.Query(`
		SELECT s.host_id, h.name, s.packages_json, s.needs_reboot, s.reboot_reason, s.needs_restart_json, s.needrestart_found, s.os_name, s.os_version, s.uptime, s.kernel, s.updated_at
		FROM scans s
		JOIN hosts h ON h.id = s.host_id
		ORDER BY s.updated_at DESC, h.name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.ScanSnapshot{}
	for rows.Next() {
		var snap models.ScanSnapshot
		var pkgJSON string
		var needsRestartJSON string
		var needsReboot int
		var needrestartFound int
		if err := rows.Scan(&snap.HostID, &snap.HostName, &pkgJSON, &needsReboot, &snap.RebootReason, &needsRestartJSON, &needrestartFound, &snap.OsName, &snap.OsVersion, &snap.Uptime, &snap.Kernel, &snap.UpdatedAt); err != nil {
			return nil, err
		}
		snap.NeedsReboot = needsReboot == 1
		snap.NeedrestartFound = needrestartFound == 1
		snap.Packages = unmarshalPackages(pkgJSON)
		if strings.TrimSpace(needsRestartJSON) != "" {
			if err := json.Unmarshal([]byte(needsRestartJSON), &snap.NeedsRestart); err != nil {
				return nil, err
			}
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

func ListScanHistory(db *sql.DB, hostID string) ([]models.ScanHistoryEntry, error) {
	rows, err := db.Query(`SELECT id, host_id, packages_json, needs_reboot, reboot_reason, needs_restart_json, needrestart_found, raw_output, created_at FROM scan_history WHERE host_id=? ORDER BY created_at DESC LIMIT 10`, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ScanHistoryEntry{}
	for rows.Next() {
		var e models.ScanHistoryEntry
		var pkgJSON, needsRestartJSON string
		var needsReboot, needrestartFound int
		if err := rows.Scan(&e.ID, &e.HostID, &pkgJSON, &needsReboot, &e.RebootReason, &needsRestartJSON, &needrestartFound, &e.RawOutput, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.NeedsReboot = needsReboot == 1
		e.NeedrestartFound = needrestartFound == 1
		e.Packages = unmarshalPackages(pkgJSON)
		if strings.TrimSpace(needsRestartJSON) != "" {
			if err := json.Unmarshal([]byte(needsRestartJSON), &e.NeedsRestart); err != nil {
				return nil, err
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func UpdateHostTags(db *sql.DB, hostID string, tags []string) error {
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, _ := json.Marshal(tags)
	res, err := db.Exec(`UPDATE hosts SET tags_json=? WHERE id=?`, string(tagsJSON), hostID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func ListAllTags(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT tags_json FROM hosts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]struct{}{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var tags []string
		if strings.TrimSpace(raw) != "" {
			if err := json.Unmarshal([]byte(raw), &tags); err != nil {
				continue
			}
		}
		for _, t := range tags {
			t = strings.TrimSpace(t)
			if t != "" {
				seen[t] = struct{}{}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	return out, nil
}

func CountHosts(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT count(*) FROM hosts`).Scan(&n)
	return n, err
}

func getSettingBool(db *sql.DB, key string, fallback bool) (bool, error) {
	var raw string
	err := db.QueryRow(`SELECT value FROM app_settings WHERE key=?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return fallback, nil
	}
	if err != nil {
		return fallback, err
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return fallback, nil
	}
}

func boolString(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func ensureHostColumn(db *sql.DB, name, def string) error {
	return ensureTableColumn(db, "hosts", name, def)
}

func ensureUserColumn(db *sql.DB, name, def string) error {
	return ensureTableColumn(db, "users", name, def)
}

func ensureTableColumn(db *sql.DB, table, name, def string) error {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var colName, colType string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &colName, &colType, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if strings.EqualFold(colName, name) {
			return nil
		}
	}
	if rows.Err() != nil {
		return rows.Err()
	}
	_, err = db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, name, def))
	return err
}

func isUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}

// --- API Token functions ---

func CreateAPIToken(db *sql.DB, name string) (plaintext string, id string, err error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	plaintext = "pd_" + hex.EncodeToString(b)
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", "", fmt.Errorf("hash token: %w", err)
	}
	id = uuid.NewString()
	_, err = db.Exec(`INSERT INTO api_tokens(id,name,token_hash,created_at,revoked) VALUES(?,?,?,?,0)`, id, strings.TrimSpace(name), string(hash), time.Now().UTC())
	if err != nil {
		return "", "", err
	}
	return plaintext, id, nil
}

func ListAPITokens(db *sql.DB) ([]models.APIToken, error) {
	rows, err := db.Query(`SELECT id, name, created_at, last_used_at, revoked FROM api_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.APIToken{}
	for rows.Next() {
		var t models.APIToken
		var lastUsed sql.NullTime
		var revoked int
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt, &lastUsed, &revoked); err != nil {
			return nil, err
		}
		if lastUsed.Valid {
			lu := lastUsed.Time
			t.LastUsedAt = &lu
		}
		t.Revoked = revoked == 1
		out = append(out, t)
	}
	return out, rows.Err()
}

func RevokeAPIToken(db *sql.DB, id string) error {
	res, err := db.Exec(`UPDATE api_tokens SET revoked=1 WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func DeleteAPIToken(db *sql.DB, id string) error {
	res, err := db.Exec(`DELETE FROM api_tokens WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ValidateAPIToken checks a bearer token against stored hashes (non-revoked).
// Returns true and the token ID if valid.
func ValidateAPIToken(db *sql.DB, token string) (bool, string, error) {
	rows, err := db.Query(`SELECT id, token_hash FROM api_tokens WHERE revoked=0`)
	if err != nil {
		return false, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id, hash string
		if err := rows.Scan(&id, &hash); err != nil {
			return false, "", err
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(token)) == nil {
			// Update last_used_at
			_, _ = db.Exec(`UPDATE api_tokens SET last_used_at=? WHERE id=?`, time.Now().UTC(), id)
			return true, id, nil
		}
	}
	return false, "", rows.Err()
}

// --- Activity Log functions ---

func RecordActivity(db *sql.DB, hostID, hostName, eventType, summary string) error {
	_, err := db.Exec(`INSERT INTO activity_log(host_id,host_name,event_type,summary,created_at) VALUES(?,?,?,?,?)`,
		strings.TrimSpace(hostID), strings.TrimSpace(hostName), strings.TrimSpace(eventType), strings.TrimSpace(summary), time.Now().UTC())
	return err
}

// GetAuditRetentionDays returns the configured retention in days (0 = unlimited).
// Default is 30 if not set.
func GetAuditRetentionDays(db *sql.DB) (int, error) {
	var raw string
	err := db.QueryRow(`SELECT value FROM app_settings WHERE key='audit_retention_days'`).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 30, nil // default
		}
		return 30, err
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 30, nil
	}
	return v, nil
}

// SetAuditRetentionDays stores the retention setting. Enforces minimum of 30 (or 0 for unlimited).
func SetAuditRetentionDays(db *sql.DB, days int) error {
	if days != 0 && days < 30 {
		return fmt.Errorf("minimum retention is 30 days (or 0 for unlimited)")
	}
	_, err := db.Exec(`INSERT INTO app_settings(key,value) VALUES('audit_retention_days',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.Itoa(days))
	return err
}

// PurgeOldActivity deletes activity records older than the retention window.
// Respects a hard 30-day floor regardless of input. Returns count deleted.
func PurgeOldActivity(db *sql.DB, retentionDays int) (int64, error) {
	if retentionDays == 0 {
		return 0, nil // unlimited, never purge
	}
	if retentionDays < 30 {
		retentionDays = 30 // hard floor
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	res, err := db.Exec(`DELETE FROM activity_log WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ExportActivity returns all activity entries matching the filters (no pagination limit).
func ExportActivity(db *sql.DB, hostID, eventType string, from, to time.Time) ([]models.ActivityEntry, error) {
	query := `SELECT id, host_id, host_name, event_type, summary, created_at FROM activity_log WHERE 1=1`
	args := []any{}

	hostID = strings.TrimSpace(hostID)
	if hostID != "" {
		query += ` AND host_id=?`
		args = append(args, hostID)
	}
	eventType = strings.TrimSpace(eventType)
	if eventType != "" {
		query += ` AND event_type=?`
		args = append(args, eventType)
	}
	if !from.IsZero() {
		query += ` AND created_at >= ?`
		args = append(args, from)
	}
	if !to.IsZero() {
		query += ` AND created_at <= ?`
		args = append(args, to)
	}
	query += ` ORDER BY created_at ASC, id ASC`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.ActivityEntry
	for rows.Next() {
		var e models.ActivityEntry
		var hid, hname, summary sql.NullString
		if err := rows.Scan(&e.ID, &hid, &hname, &e.EventType, &summary, &e.CreatedAt); err != nil {
			return nil, err
		}
		if hid.Valid {
			e.HostID = hid.String
		}
		if hname.Valid {
			e.HostName = hname.String
		}
		if summary.Valid {
			e.Summary = summary.String
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func ListActivity(db *sql.DB, limit, offset int, hostID string) ([]models.ActivityEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	var rows *sql.Rows
	var err error
	hostID = strings.TrimSpace(hostID)
	if hostID != "" {
		rows, err = db.Query(`SELECT id, host_id, host_name, event_type, summary, created_at FROM activity_log WHERE host_id=? ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, hostID, limit, offset)
	} else {
		rows, err = db.Query(`SELECT id, host_id, host_name, event_type, summary, created_at FROM activity_log ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ActivityEntry{}
	for rows.Next() {
		var e models.ActivityEntry
		var hid, hname, summary sql.NullString
		if err := rows.Scan(&e.ID, &hid, &hname, &e.EventType, &summary, &e.CreatedAt); err != nil {
			return nil, err
		}
		if hid.Valid {
			e.HostID = hid.String
		}
		if hname.Valid {
			e.HostName = hname.String
		}
		if summary.Valid {
			e.Summary = summary.String
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func _(_ fmt.Stringer) {}

// ListHostsByTag returns all hosts that have the given tag.
func ListHostsByTag(db *sql.DB, tag string) ([]models.Host, error) {
	all, err := ListHosts(db)
	if err != nil {
		return nil, err
	}
	tag = strings.TrimSpace(strings.ToLower(tag))
	if tag == "" {
		return nil, nil
	}
	var out []models.Host
	for _, h := range all {
		for _, t := range h.Tags {
			if strings.TrimSpace(strings.ToLower(t)) == tag {
				out = append(out, h)
				break
			}
		}
	}
	return out, nil
}

// ListHostsByIDs returns hosts matching the given IDs.
func ListHostsByIDs(db *sql.DB, ids []string) ([]models.Host, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	idSet := map[string]struct{}{}
	for _, id := range ids {
		idSet[strings.TrimSpace(id)] = struct{}{}
	}
	all, err := ListHosts(db)
	if err != nil {
		return nil, err
	}
	var out []models.Host
	for _, h := range all {
		if _, ok := idSet[h.ID]; ok {
			out = append(out, h)
		}
	}
	return out, nil
}
