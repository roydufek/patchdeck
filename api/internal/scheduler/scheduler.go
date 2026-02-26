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

	mu              sync.Mutex
	lastRunKey      map[string]string
	lastPurgeDate   string // "2006-01-02" — run once per day
}

func NewEngine(dbConn *sql.DB, ssh *sshx.Client, secrets *crypto.SealBox, notifier *notify.Dispatcher, defaultAppriseURL string) *Engine {
	return &Engine{db: dbConn, ssh: ssh, secrets: secrets, notifier: notifier, defaultAppriseURL: strings.TrimSpace(defaultAppriseURL), lastRunKey: map[string]string{}}
}

func (e *Engine) Run(ctx context.Context) {
	e.tick(ctx, time.Now().UTC())
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.tick(ctx, time.Now().UTC())
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
		e.runJob(ctx, j)
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

func (e *Engine) sendNotification(hostID, eventKey, body string) {
	if e.notifier == nil {
		return
	}
	settings, err := db.GetNotificationSettings(e.db)
	if err != nil {
		log.Printf("scheduler: load notification settings failed: %v", err)
		return
	}
	if !globalEventEnabled(settings, eventKey) {
		return
	}
	prefs, err := db.GetHostNotificationPrefs(e.db, hostID)
	if err != nil {
		log.Printf("scheduler: load host notification prefs failed host=%s: %v", hostID, err)
		return
	}
	if !hostEventEnabled(prefs, eventKey) {
		return
	}
	target := strings.TrimSpace(settings.AppriseURL)
	if target == "" {
		target = e.defaultAppriseURL
	}
	if err := e.notifier.Send(target, body); err != nil {
		log.Printf("scheduler: notification failed: %v", err)
	}
}

func globalEventEnabled(settings models.NotificationSettings, eventKey string) bool {
	switch eventKey {
	case "updates_available":
		return settings.UpdatesAvailable
	case "auto_apply_success":
		return settings.AutoApplySuccess
	case "auto_apply_failure":
		return settings.AutoApplyFailure
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
	default:
		return true
	}
}

func (e *Engine) runJob(ctx context.Context, j models.Job) {
	// Resolve target hosts
	hosts, err := e.resolveJobHosts(j)
	if err != nil {
		log.Printf("scheduler: job=%s resolve hosts failed: %v", j.ID, err)
		return
	}
	if len(hosts) == 0 {
		log.Printf("scheduler: job=%s no hosts resolved (tag=%q host_ids=%v host_id=%s)", j.ID, j.TagFilter, j.HostIDs, j.HostID)
		return
	}

	mode := strings.ToLower(strings.TrimSpace(j.Mode))
	if mode == "" {
		mode = "scan"
	}

	for _, host := range hosts {
		if !host.ChecksEnabled {
			log.Printf("scheduler: job=%s skipped host=%s checks disabled", j.ID, host.Name)
			continue
		}

		switch mode {
		case "scan":
			e.runScan(j, host)
		case "apply":
			e.runApply(j, host)
		case "scan_apply":
			e.runScanApply(j, host)
		default:
			log.Printf("scheduler: job=%s unknown mode=%q", j.ID, j.Mode)
		}
	}
	_ = ctx
}

func (e *Engine) resolveJobHosts(j models.Job) ([]models.Host, error) {
	// Tag filter takes priority
	if strings.TrimSpace(j.TagFilter) != "" {
		return db.ListHostsByTag(e.db, j.TagFilter)
	}
	// Multi-host IDs
	if len(j.HostIDs) > 0 {
		return db.ListHostsByIDs(e.db, j.HostIDs)
	}
	// Legacy single host
	if strings.TrimSpace(j.HostID) != "" {
		host, err := db.GetHost(e.db, j.HostID)
		if err != nil {
			return nil, err
		}
		return []models.Host{host}, nil
	}
	return nil, nil
}

func (e *Engine) runScan(j models.Job, host models.Host) {
	res, err := e.ssh.ScanHost(host, e.secrets)
	if err != nil {
		var hkErr *sshx.HostKeyError
		if errors.As(err, &hkErr) {
			log.Printf("scheduler: job=%s scan blocked host=%s host key mismatch expected=%s presented=%s", j.ID, host.Name, hkErr.ExpectedFingerprint, hkErr.PresentedFingerprint)
			return
		}
		log.Printf("scheduler: job=%s scan host=%s failed: %v", j.ID, host.Name, err)
		return
	}
	if err := db.UpsertScanResult(e.db, host.ID, res); err != nil {
		log.Printf("scheduler: job=%s save scan host=%s failed: %v", j.ID, host.Name, err)
		return
	}
	if len(res.Packages) > 0 {
		e.sendNotification(host.ID, "updates_available", fmt.Sprintf("Patchdeck: updates available on %s (%d packages)", host.Name, len(res.Packages)))
	}
	_ = db.RecordActivity(e.db, host.ID, host.Name, "scan_ok", fmt.Sprintf("Scheduled scan completed: %d packages available", len(res.Packages)))
	log.Printf("scheduler: job=%s scan complete host=%s packages=%d reboot=%v", j.ID, host.Name, len(res.Packages), res.NeedsReboot)
}

func (e *Engine) runApply(j models.Job, host models.Host) {
	res, err := e.ssh.ApplyUpdates(host, e.secrets)
	if err != nil {
		var hkErr *sshx.HostKeyError
		if errors.As(err, &hkErr) {
			log.Printf("scheduler: job=%s apply blocked host=%s host key mismatch expected=%s presented=%s", j.ID, host.Name, hkErr.ExpectedFingerprint, hkErr.PresentedFingerprint)
			return
		}
		e.sendNotification(host.ID, "auto_apply_failure", fmt.Sprintf("Patchdeck scheduled apply FAILED: %s (%v)", host.Name, err))
		_ = db.RecordActivity(e.db, host.ID, host.Name, "apply_fail", fmt.Sprintf("Scheduled apply failed: %v", err))
		log.Printf("scheduler: job=%s apply host=%s failed: %v", j.ID, host.Name, err)
		return
	}
	e.sendNotification(host.ID, "auto_apply_success", fmt.Sprintf("Patchdeck scheduled apply success: %s (%d package changes)", host.Name, res.ChangedPackages))
	_ = db.RecordActivity(e.db, host.ID, host.Name, "apply_ok", fmt.Sprintf("Scheduled apply completed: %d packages changed", res.ChangedPackages))
	log.Printf("scheduler: job=%s apply complete host=%s changed=%d reboot=%v", j.ID, host.Name, res.ChangedPackages, res.NeedsReboot)
}

func (e *Engine) runScanApply(j models.Job, host models.Host) {
	// First scan
	scanRes, err := e.ssh.ScanHost(host, e.secrets)
	if err != nil {
		var hkErr *sshx.HostKeyError
		if errors.As(err, &hkErr) {
			log.Printf("scheduler: job=%s scan_apply scan blocked host=%s host key mismatch", j.ID, host.Name)
			return
		}
		log.Printf("scheduler: job=%s scan_apply scan host=%s failed: %v", j.ID, host.Name, err)
		return
	}
	if err := db.UpsertScanResult(e.db, host.ID, scanRes); err != nil {
		log.Printf("scheduler: job=%s scan_apply save scan host=%s failed: %v", j.ID, host.Name, err)
		return
	}
	_ = db.RecordActivity(e.db, host.ID, host.Name, "scan_ok", fmt.Sprintf("Scheduled scan completed: %d packages available", len(scanRes.Packages)))
	log.Printf("scheduler: job=%s scan_apply scan complete host=%s packages=%d", j.ID, host.Name, len(scanRes.Packages))

	if len(scanRes.Packages) == 0 {
		log.Printf("scheduler: job=%s scan_apply no packages to apply host=%s", j.ID, host.Name)
		return
	}

	// Then apply
	e.sendNotification(host.ID, "updates_available", fmt.Sprintf("Patchdeck: updates available on %s (%d packages), applying...", host.Name, len(scanRes.Packages)))
	e.runApply(j, host)
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
	vals := []int{now.Minute(), now.Hour(), now.Day(), int(now.Month()), int(now.Weekday())}
	labels := []string{"minute", "hour", "day", "month", "weekday"}
	limits := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	for i := range parts {
		if !fieldMatches(parts[i], vals[i], labels[i], limits[i][0], limits[i][1]) {
			return false
		}
	}
	return true
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
