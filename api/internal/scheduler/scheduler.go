package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"patchdeck/api/internal/crypto"
	"patchdeck/api/internal/db"
	"patchdeck/api/internal/models"
	"patchdeck/api/internal/notify"
	"patchdeck/api/internal/sshx"
)

type Engine struct {
	db                *sql.DB
	ssh               *sshx.Client
	secrets           *crypto.SealBox
	notifier          *notify.Dispatcher
	defaultAppriseURL string

	mu            sync.Mutex
	lastRunKey    map[string]string // per-job minute-dedup, pruned each tick
	running       map[string]bool   // jobs currently executing — prevents overlap
	lastPurgeDate string            // "2006-01-02" — run once per day
	sem           chan struct{}     // bounds concurrent job execution
}

func NewEngine(dbConn *sql.DB, ssh *sshx.Client, secrets *crypto.SealBox, notifier *notify.Dispatcher, defaultAppriseURL string) *Engine {
	return &Engine{
		db: dbConn, ssh: ssh, secrets: secrets, notifier: notifier,
		defaultAppriseURL: strings.TrimSpace(defaultAppriseURL),
		lastRunKey:        map[string]string{},
		running:           map[string]bool{},
		sem:               make(chan struct{}, 4),
	}
}

func (e *Engine) Run(ctx context.Context) {
	// Cron is evaluated in LOCAL time so schedules fire in the operator's timezone (set
	// via the TZ env var; the binary embeds tzdata so named zones resolve on the minimal
	// runtime image). The per-minute dedup keys below normalize to UTC internally, which
	// keeps them correct regardless of the wall-clock zone.
	e.tick(ctx, time.Now())
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.tick(ctx, time.Now())
		}
	}
}

func (e *Engine) tick(ctx context.Context, now time.Time) {
	// Daily audit log purge
	e.maybePurgeActivity(now)

	jobs, err := db.ListJobs(e.db)
	if err != nil {
		log.Printf("scheduler: list jobs: %v", err)
		return
	}

	e.pruneLastRun(now)
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		if !cronMatches(j.CronExpr, now) {
			continue
		}
		if !e.markDue(j.ID, now) {
			continue
		}
		if !e.tryStart(j.ID) {
			log.Printf("scheduler: job=%s still running from a previous window; skipping this fire", j.ID)
			continue
		}
		// Run each due job concurrently (bounded by sem) so a long apply doesn't
		// block the ticker or delay other due jobs past their minute window.
		go func(job models.Job) {
			e.sem <- struct{}{}
			defer func() { <-e.sem; e.finishStart(job.ID) }()
			e.runJob(ctx, job)
		}(j)
	}
}

func (e *Engine) markDue(jobID string, now time.Time) bool {
	key := now.UTC().Format("2006-01-02T15:04")
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lastRunKey[jobID] == key {
		return false
	}
	e.lastRunKey[jobID] = key
	return true
}

// pruneLastRun drops dedup entries from past minutes so the map stays bounded.
func (e *Engine) pruneLastRun(now time.Time) {
	key := now.UTC().Format("2006-01-02T15:04")
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, v := range e.lastRunKey {
		if v != key {
			delete(e.lastRunKey, id)
		}
	}
}

// tryStart marks a job as running; returns false if it's already executing.
func (e *Engine) tryStart(jobID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running[jobID] {
		return false
	}
	e.running[jobID] = true
	return true
}

func (e *Engine) finishStart(jobID string) {
	e.mu.Lock()
	delete(e.running, jobID)
	e.mu.Unlock()
}

func (e *Engine) maybePurgeActivity(now time.Time) {
	today := now.UTC().Format("2006-01-02")
	e.mu.Lock()
	if e.lastPurgeDate == today {
		e.mu.Unlock()
		return
	}
	e.lastPurgeDate = today
	e.mu.Unlock()

	retentionDays, err := db.GetAuditRetentionDays(e.db)
	if err != nil {
		log.Printf("scheduler: get audit retention: %v", err)
		return
	}
	if retentionDays == 0 {
		return // unlimited retention
	}
	deleted, err := db.PurgeOldActivity(e.db, retentionDays)
	if err != nil {
		log.Printf("scheduler: purge activity: %v", err)
		return
	}
	if deleted > 0 {
		log.Printf("scheduler: purged %d activity records older than %d days", deleted, retentionDays)
	}
}

// runNotes routes one run's notifications. In digest delivery (the default) host events are
// collected and sent as ONE message when the run ends; in per-host delivery each event is
// sent immediately with its legacy one-line body.
type runNotes struct {
	perHost bool
	always  bool
	digest  notify.Digest
}

func (e *Engine) newRunNotes(j models.Job) *runNotes {
	n := &runNotes{digest: notify.Digest{Scheduled: true, Job: j.Name}}
	if s, err := db.GetNotificationSettings(e.db); err == nil {
		n.perHost = s.Delivery == models.DeliveryPerHost
		n.always = s.DigestAlways
	}
	return n
}

// note records one host event. legacyBody is what per-host delivery sends ("" = per-host
// delivery stays silent for this event); add puts the event into the run's digest.
func (e *Engine) note(n *runNotes, hostID, eventKey, legacyBody string, add func(*notify.Digest)) {
	if n.perHost {
		if legacyBody != "" {
			e.sendNotification(hostID, eventKey, legacyBody)
		}
		return
	}
	if e.eventEnabled(hostID, eventKey) {
		add(&n.digest)
	}
}

// sendDigest sends the run's digest when it says something new (or the operator asked for
// every run). Per-host delivery already sent everything as it happened.
func (e *Engine) sendDigest(n *runNotes) {
	if n.perHost || e.notifier == nil || n.digest.Empty() {
		return
	}
	if !n.always && !n.digest.Notable() {
		return
	}
	if err := e.notifier.SendDigest(e.target(), n.digest); err != nil {
		log.Printf("scheduler: digest notification failed: %v", err)
	}
}

func (e *Engine) target() string {
	if s, err := db.GetNotificationSettings(e.db); err == nil && strings.TrimSpace(s.AppriseURL) != "" {
		return strings.TrimSpace(s.AppriseURL)
	}
	return e.defaultAppriseURL
}

// eventEnabled applies the global then the per-host toggle for an event.
func (e *Engine) eventEnabled(hostID, eventKey string) bool {
	settings, err := db.GetNotificationSettings(e.db)
	if err != nil {
		log.Printf("scheduler: load notification settings failed: %v", err)
		return false
	}
	if !globalEventEnabled(settings, eventKey) {
		return false
	}
	prefs, err := db.GetHostNotificationPrefs(e.db, hostID)
	if err != nil {
		log.Printf("scheduler: load host notification prefs failed host=%s: %v", hostID, err)
		return false
	}
	return hostEventEnabled(prefs, eventKey)
}

func (e *Engine) sendNotification(hostID, eventKey, body string) {
	if e.notifier == nil || !e.eventEnabled(hostID, eventKey) {
		return
	}
	if err := e.notifier.Send(e.target(), body); err != nil {
		log.Printf("scheduler: notification failed: %v", err)
	}
}

// hostKeyChanged is the digest's reason line for a blocked (host-key mismatch) scan or apply.
const hostKeyChanged = "SSH host key changed — review it in Patchdeck"

func globalEventEnabled(settings models.NotificationSettings, eventKey string) bool {
	switch eventKey {
	case "updates_available":
		return settings.UpdatesAvailable
	case "auto_apply_success":
		return settings.AutoApplySuccess
	case "auto_apply_failure":
		return settings.AutoApplyFailure
	case "scan_failure":
		return settings.ScanFailure
	default:
		return true
	}
}

func hostEventEnabled(prefs models.HostNotificationPrefs, eventKey string) bool {
	switch eventKey {
	case "updates_available":
		return prefs.UpdatesAvailable
	case "auto_apply_success":
		return prefs.AutoApplySuccess
	case "auto_apply_failure":
		return prefs.AutoApplyFailure
	case "scan_failure":
		return prefs.ScanFailure
	default:
		return true
	}
}

func (e *Engine) runJob(ctx context.Context, j models.Job) {
	mode := strings.ToLower(strings.TrimSpace(j.Mode))
	if mode == "" {
		mode = "scan"
	}
	runID, cerr := db.CreateJobRun(e.db, j.ID, mode)
	if cerr != nil {
		log.Printf("scheduler: job=%s create run record failed: %v", j.ID, cerr)
		runID = ""
	}

	// Contain panics: a panic in any scan/apply/parse path would otherwise
	// propagate out of the job goroutine and crash the whole process (taking the
	// HTTP server down with it), and would leave this run row stuck in "running".
	// Recover, log, and close the run as failed so the engine keeps serving.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("scheduler: job=%s PANIC recovered: %v", j.ID, r)
			e.finishRun(runID, "failed", 0, 0, 0, fmt.Sprintf("internal error (panic): %v", r))
		}
	}()

	hosts, rerr := e.resolveJobHosts(j)
	if rerr != nil {
		log.Printf("scheduler: job=%s resolve hosts failed: %v", j.ID, rerr)
		e.finishRun(runID, "failed", 0, 0, 0, fmt.Sprintf("resolve hosts failed: %v", rerr))
		return
	}
	if len(hosts) == 0 {
		log.Printf("scheduler: job=%s no hosts resolved (tag=%q host_ids=%v host_id=%s)", j.ID, j.TagFilter, j.HostIDs, j.HostID)
		e.finishRun(runID, "failed", 0, 0, 0, "no hosts resolved (check tag / host selection)")
		return
	}

	notes := e.newRunNotes(j)
	total, ok, failed := 0, 0, 0
	var details []string
	for _, host := range hosts {
		if !host.ChecksEnabled {
			log.Printf("scheduler: job=%s skipped host=%s checks disabled", j.ID, host.Name)
			details = append(details, host.Name+": skipped (checks disabled)")
			continue
		}
		var hostOK bool
		switch mode {
		case "scan":
			hostOK = e.runScan(j, host, notes)
		case "apply":
			hostOK = e.runApply(j, host, notes)
		case "scan_apply":
			hostOK = e.runScanApply(j, host, notes)
		default:
			log.Printf("scheduler: job=%s unknown mode=%q", j.ID, j.Mode)
			details = append(details, host.Name+": unknown mode")
			continue
		}
		total++
		if hostOK {
			ok++
		} else {
			failed++
			details = append(details, host.Name+": failed (see activity log)")
		}
	}

	status := "success"
	if ok > 0 && failed > 0 {
		status = "partial"
	} else if failed > 0 {
		status = "failed"
	}
	e.finishRun(runID, status, total, ok, failed, strings.Join(details, "; "))
	e.sendDigest(notes)
	_ = db.PruneJobRuns(e.db, j.ID, 50)
	_ = ctx
}

func (e *Engine) finishRun(runID, status string, total, ok, failed int, detail string) {
	if runID == "" {
		return
	}
	if err := db.FinishJobRun(e.db, runID, status, total, ok, failed, detail); err != nil {
		log.Printf("scheduler: finish run %s failed: %v", runID, err)
	}
}

func (e *Engine) resolveJobHosts(j models.Job) ([]models.Host, error) {
	// Resolve the TARGET host list first. The single-host path already returns a full,
	// secret-bearing record via GetHost; the tag/multi-host helpers (ListHostsByTag /
	// ListHostsByIDs) are built on ListHosts, which deliberately OMITS secret_cipher (so
	// the API never leaks creds) — so those rows have an empty cipher.
	var targets []models.Host
	switch {
	case strings.TrimSpace(j.TagFilter) != "":
		hs, err := db.ListHostsByTag(e.db, j.TagFilter)
		if err != nil {
			return nil, err
		}
		targets = hs
	case len(j.HostIDs) > 0:
		hs, err := db.ListHostsByIDs(e.db, j.HostIDs)
		if err != nil {
			return nil, err
		}
		targets = hs
	case strings.TrimSpace(j.HostID) != "":
		host, err := db.GetHost(e.db, j.HostID)
		if err != nil {
			return nil, err
		}
		return []models.Host{host}, nil
	default:
		return nil, nil
	}

	// Re-load each target via GetHost so it carries its encrypted SSH secret. Without this
	// the scan/apply fails for every host with "cipher too short" (decrypting an empty
	// cipher) — the tag/multi-host scheduled jobs were hitting exactly that.
	full := make([]models.Host, 0, len(targets))
	for _, t := range targets {
		h, err := db.GetHost(e.db, t.ID)
		if err != nil {
			log.Printf("scheduler: skip host %s (%s) — load failed: %v", t.ID, t.Name, err)
			continue
		}
		full = append(full, h)
	}
	return full, nil
}

// noteScanFailure records a failed scan. Host-key mismatches pass legacyBody "" — digest
// delivery lists them, per-host delivery keeps its old behaviour of staying silent on them.
func (e *Engine) noteScanFailure(n *runNotes, host models.Host, legacyBody, reason string) {
	e.note(n, host.ID, "scan_failure", legacyBody, func(d *notify.Digest) {
		d.Failed = append(d.Failed, notify.FailedHost{Name: host.Name, Phase: "scan", Err: reason})
	})
}

// notePending records a scan's result: a host with updates is listed (its New flag decides
// whether the digest is worth sending), a host without counts as up to date.
func (e *Engine) notePending(n *runNotes, host models.Host, prev []models.PackageInfo, res models.ScanResult) {
	if len(res.Packages) == 0 {
		if !n.perHost {
			n.digest.UpToDate++
		}
		return
	}
	isNew := models.HasNewUpdates(prev, res.Packages)
	legacy := ""
	if isNew { // per-host delivery only pings on genuinely new updates
		legacy = fmt.Sprintf("Patchdeck: updates available on %s (%d packages)", host.Name, len(res.Packages))
	}
	e.note(n, host.ID, "updates_available", legacy, func(d *notify.Digest) {
		d.Pending = append(d.Pending, notify.PendingHost{
			Name: host.Name, Packages: len(res.Packages), Security: models.SecurityCount(res.Packages),
			New: isNew, Reboot: res.NeedsReboot, Restarts: len(res.NeedsRestart),
		})
	})
}

func (e *Engine) runScan(j models.Job, host models.Host, n *runNotes) bool {
	res, err := e.ssh.ScanHost(host, e.secrets)
	if err != nil {
		var hkErr *sshx.HostKeyError
		if errors.As(err, &hkErr) {
			log.Printf("scheduler: job=%s scan blocked host=%s host key mismatch expected=%s presented=%s", j.ID, host.Name, hkErr.ExpectedFingerprint, hkErr.PresentedFingerprint)
			e.noteScanFailure(n, host, "", hostKeyChanged)
			_ = db.RecordActivity(e.db, host.ID, host.Name, "scan_fail", "Scheduled scan blocked: SSH host key mismatch")
			return false
		}
		log.Printf("scheduler: job=%s scan host=%s failed: %v", j.ID, host.Name, err)
		e.noteScanFailure(n, host, fmt.Sprintf("Patchdeck scheduled scan FAILED: %s (%v)", host.Name, err), err.Error())
		_ = db.RecordActivity(e.db, host.ID, host.Name, "scan_fail", fmt.Sprintf("Scheduled scan failed: %v", err))
		return false
	}
	prev, _, _ := db.GetScanSnapshot(e.db, host.ID) // before the upsert, for new-updates detection
	if err := db.UpsertScanResult(e.db, host.ID, res); err != nil {
		log.Printf("scheduler: job=%s save scan host=%s failed: %v", j.ID, host.Name, err)
		return false
	}
	e.notePending(n, host, prev.Packages, res)
	_ = db.RecordActivity(e.db, host.ID, host.Name, "scan_ok", fmt.Sprintf("Scheduled scan completed: %d packages available", len(res.Packages)))
	log.Printf("scheduler: job=%s scan complete host=%s packages=%d reboot=%v", j.ID, host.Name, len(res.Packages), res.NeedsReboot)
	return true
}

func (e *Engine) runApply(j models.Job, host models.Host, n *runNotes) bool {
	res, err := e.ssh.ApplyUpdates(host, e.secrets)
	if err != nil {
		var hkErr *sshx.HostKeyError
		if errors.As(err, &hkErr) {
			log.Printf("scheduler: job=%s apply blocked host=%s host key mismatch expected=%s presented=%s", j.ID, host.Name, hkErr.ExpectedFingerprint, hkErr.PresentedFingerprint)
			e.note(n, host.ID, "auto_apply_failure", "", func(d *notify.Digest) {
				d.Failed = append(d.Failed, notify.FailedHost{Name: host.Name, Phase: "apply", Err: hostKeyChanged})
			})
			_ = db.RecordActivity(e.db, host.ID, host.Name, "apply_fail", "Scheduled apply blocked: SSH host key mismatch")
			return false
		}
		var interrupted *sshx.ApplyInterruptedError
		if errors.As(err, &interrupted) {
			// Connection dropped mid-apply (service restart or reboot) — the apply almost
			// certainly completed. Don't send a FAILED alert; the next scheduled scan will
			// reconcile the host's real state. (The digest lists it as applied, flagged.)
			e.note(n, host.ID, "auto_apply_success", "", func(d *notify.Digest) {
				d.AddApplied(notify.AppliedHost{Name: host.Name, Changed: interrupted.ChangedSoFar, Interrupted: true})
			})
			_ = db.RecordActivity(e.db, host.ID, host.Name, "apply_interrupted",
				fmt.Sprintf("Scheduled apply: connection lost (%d package(s) installed) — host likely rebooting; will reconcile on next scan", interrupted.ChangedSoFar))
			log.Printf("scheduler: job=%s apply host=%s interrupted (connection lost, %d changed): %v", j.ID, host.Name, interrupted.ChangedSoFar, err)
			return true
		}
		e.note(n, host.ID, "auto_apply_failure", fmt.Sprintf("Patchdeck scheduled apply FAILED: %s (%v)", host.Name, err), func(d *notify.Digest) {
			d.Failed = append(d.Failed, notify.FailedHost{Name: host.Name, Phase: "apply", Err: err.Error()})
		})
		_ = db.RecordActivity(e.db, host.ID, host.Name, "apply_fail", fmt.Sprintf("Scheduled apply failed: %v", err))
		log.Printf("scheduler: job=%s apply host=%s failed: %v", j.ID, host.Name, err)
		return false
	}
	e.note(n, host.ID, "auto_apply_success", fmt.Sprintf("Patchdeck scheduled apply success: %s (%d package changes)", host.Name, res.ChangedPackages), func(d *notify.Digest) {
		d.AddApplied(notify.AppliedHost{Name: host.Name, Changed: res.ChangedPackages, Reboot: res.NeedsReboot})
	})
	_ = db.RecordActivity(e.db, host.ID, host.Name, "apply_ok", fmt.Sprintf("Scheduled apply completed: %d packages changed", res.ChangedPackages))
	log.Printf("scheduler: job=%s apply complete host=%s changed=%d reboot=%v", j.ID, host.Name, res.ChangedPackages, res.NeedsReboot)
	return true
}

func (e *Engine) runScanApply(j models.Job, host models.Host, n *runNotes) bool {
	scanRes, err := e.ssh.ScanHost(host, e.secrets)
	if err != nil {
		var hkErr *sshx.HostKeyError
		if errors.As(err, &hkErr) {
			log.Printf("scheduler: job=%s scan_apply scan blocked host=%s host key mismatch", j.ID, host.Name)
			e.noteScanFailure(n, host, "", hostKeyChanged)
			_ = db.RecordActivity(e.db, host.ID, host.Name, "scan_fail", "Scheduled scan_apply blocked: SSH host key mismatch")
			return false
		}
		log.Printf("scheduler: job=%s scan_apply scan host=%s failed: %v", j.ID, host.Name, err)
		e.noteScanFailure(n, host, fmt.Sprintf("Patchdeck scheduled scan FAILED: %s (%v)", host.Name, err), err.Error())
		_ = db.RecordActivity(e.db, host.ID, host.Name, "scan_fail", fmt.Sprintf("Scheduled scan failed: %v", err))
		return false
	}
	if err := db.UpsertScanResult(e.db, host.ID, scanRes); err != nil {
		log.Printf("scheduler: job=%s scan_apply save scan host=%s failed: %v", j.ID, host.Name, err)
		return false
	}
	_ = db.RecordActivity(e.db, host.ID, host.Name, "scan_ok", fmt.Sprintf("Scheduled scan completed: %d packages available", len(scanRes.Packages)))
	log.Printf("scheduler: job=%s scan_apply scan complete host=%s packages=%d", j.ID, host.Name, len(scanRes.Packages))

	if len(scanRes.Packages) == 0 {
		log.Printf("scheduler: job=%s scan_apply no packages to apply host=%s", j.ID, host.Name)
		if !n.perHost {
			n.digest.UpToDate++
		}
		return true // nothing to apply = success
	}

	// Per-host delivery keeps its "applying..." heads-up; the digest reports the apply's outcome instead.
	if n.perHost {
		e.sendNotification(host.ID, "updates_available", fmt.Sprintf("Patchdeck: updates available on %s (%d packages), applying...", host.Name, len(scanRes.Packages)))
	}
	return e.runApply(j, host, n)
}

// NextRun returns the next time the cron expression fires at or after `from`
// (minute resolution), scanning up to ~366 days ahead. Returns nil if the expression
// is invalid or never matches within that window.
func NextRun(expr string, from time.Time) *time.Time {
	if strings.TrimSpace(expr) == "" {
		return nil
	}
	// Evaluate in from's own timezone (callers pass local time) so the computed "next run"
	// matches when the schedule actually fires.
	t := from.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*24*60; i++ {
		if cronMatches(expr, t) {
			next := t
			return &next
		}
		t = t.Add(time.Minute)
	}
	return nil
}

func cronMatches(expr string, now time.Time) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return false
	}
	if isCronMacro(expr) {
		expr = expandMacro(expr)
	}
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return false
	}
	// minute, hour, month are a straightforward AND.
	if !fieldMatches(parts[0], now.Minute(), "minute", 0, 59) {
		return false
	}
	if !fieldMatches(parts[1], now.Hour(), "hour", 0, 23) {
		return false
	}
	if !fieldMatches(parts[3], int(now.Month()), "month", 1, 12) {
		return false
	}
	// Day-of-month (field 2) and day-of-week (field 4) follow standard Vixie-cron
	// semantics: when BOTH are restricted (neither is "*"), the tick matches if
	// EITHER matches (OR). Otherwise the restricted field applies (AND with the "*").
	dom := strings.TrimSpace(parts[2])
	dow := strings.TrimSpace(parts[4])
	domMatch := fieldMatches(dom, now.Day(), "day", 1, 31)
	dowMatch := weekdayFieldMatches(dow, now.Weekday())
	if dom != "*" && dow != "*" {
		return domMatch || dowMatch
	}
	return domMatch && dowMatch
}

// weekdayFieldMatches matches a cron weekday field against wd, accepting BOTH 0
// and 7 for Sunday. Cron allows 7 to mean Sunday, but time.Weekday() only ever
// yields 0-6, so a field of "7" (or a range like "5-7") would never fire without
// this normalization.
func weekdayFieldMatches(field string, wd time.Weekday) bool {
	if fieldMatches(field, int(wd), "weekday", 0, 7) {
		return true
	}
	if wd == time.Sunday && fieldMatches(field, 7, "weekday", 0, 7) {
		return true
	}
	return false
}

func isCronMacro(expr string) bool {
	switch strings.ToLower(strings.TrimSpace(expr)) {
	case "@yearly", "@annually", "@monthly", "@weekly", "@daily", "@midnight", "@hourly":
		return true
	default:
		return false
	}
}

func expandMacro(expr string) string {
	switch strings.ToLower(strings.TrimSpace(expr)) {
	case "@yearly", "@annually":
		return "0 0 1 1 *"
	case "@monthly":
		return "0 0 1 * *"
	case "@weekly":
		return "0 0 * * 0"
	case "@daily", "@midnight":
		return "0 0 * * *"
	case "@hourly":
		return "0 * * * *"
	default:
		return expr
	}
}

func fieldMatches(field string, value int, label string, min int, max int) bool {
	for _, seg := range strings.Split(field, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			return false
		}
		if segmentMatches(seg, value, label, min, max) {
			return true
		}
	}
	return false
}

func segmentMatches(segment string, value int, label string, min int, max int) bool {
	base, stepText, hasStep := strings.Cut(segment, "/")
	step := 1
	if hasStep {
		n, err := strconv.Atoi(stepText)
		if err != nil || n < 1 {
			return false
		}
		step = n
	}

	matchesBase := false
	start := min

	if base == "*" {
		matchesBase = true
		start = min
	} else if strings.Contains(base, "-") {
		loText, hiText, ok := strings.Cut(base, "-")
		if !ok {
			return false
		}
		lo, errLo := cronValueToInt(loText, label)
		hi, errHi := cronValueToInt(hiText, label)
		if errLo != nil || errHi != nil || lo < min || hi > max || lo > hi {
			return false
		}
		matchesBase = value >= lo && value <= hi
		start = lo
	} else {
		n, err := cronValueToInt(base, label)
		if err != nil || n < min || n > max {
			return false
		}
		matchesBase = value == n
		start = n
	}

	if !matchesBase {
		return false
	}
	if !hasStep {
		return true
	}
	if value < start {
		return false
	}
	return (value-start)%step == 0
}

func cronValueToInt(value string, label string) (int, error) {
	v := strings.ToLower(strings.TrimSpace(value))
	if n, err := strconv.Atoi(v); err == nil {
		return n, nil
	}

	switch label {
	case "month":
		monthNames := map[string]int{
			"jan": 1, "january": 1,
			"feb": 2, "february": 2,
			"mar": 3, "march": 3,
			"apr": 4, "april": 4,
			"may": 5,
			"jun": 6, "june": 6,
			"jul": 7, "july": 7,
			"aug": 8, "august": 8,
			"sep": 9, "september": 9,
			"oct": 10, "october": 10,
			"nov": 11, "november": 11,
			"dec": 12, "december": 12,
		}
		if n, ok := monthNames[v]; ok {
			return n, nil
		}
	case "weekday":
		weekdayNames := map[string]int{
			"sun": 0, "sunday": 0,
			"mon": 1, "monday": 1,
			"tue": 2, "tuesday": 2,
			"wed": 3, "wednesday": 3,
			"thu": 4, "thursday": 4,
			"fri": 5, "friday": 5,
			"sat": 6, "saturday": 6,
		}
		if n, ok := weekdayNames[v]; ok {
			return n, nil
		}
	}
	return 0, fmt.Errorf("invalid value")
}
