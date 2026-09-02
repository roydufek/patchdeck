package main

import (
	"net/http"
	"strconv"
	"strings"

	"patchdeck/api/internal/db"
)

// spike_activity.go — /next activity log (paged) reading the same db.ListActivity the
// JSON API uses. CSV export links straight to the existing /api/activity/export.

const activityPage = 50

// eventTone maps an activity event_type to a semantic dot colour.
func eventTone(t string) string {
	switch {
	case strings.HasSuffix(t, "_fail"):
		return "err"
	case strings.HasSuffix(t, "_interrupted"), t == "host_deleted", strings.Contains(t, "mismatch"), strings.Contains(t, "denied"):
		return "warn"
	default:
		return "ok"
	}
}

var eventLabels = map[string]string{
	"scan_ok": "Scan", "scan_fail": "Scan failed",
	"apply_ok": "Apply", "apply_fail": "Apply failed", "apply_interrupted": "Apply interrupted",
	"restart_ok": "Services restarted", "restart_fail": "Restart failed",
	"reboot_ok": "Reboot", "reboot_fail": "Reboot failed",
	"shutdown_ok": "Shutdown", "shutdown_fail": "Shutdown failed",
	"host_added": "Host added", "host_updated": "Host updated", "host_deleted": "Host deleted",
	"host_key_rotation_accepted": "Host key accepted", "host_key_mismatch_denied": "Host key denied",
}

// eventLabel humanizes an event_type for display.
func eventLabel(t string) string {
	if l, ok := eventLabels[t]; ok {
		return l
	}
	if t == "" {
		return "Event"
	}
	s := strings.ReplaceAll(t, "_", " ")
	return strings.ToUpper(s[:1]) + s[1:]
}

func (a *app) nextActivity(w http.ResponseWriter, r *http.Request) {
	host := strings.TrimSpace(r.URL.Query().Get("host"))
	entries, _ := db.ListActivity(a.db, activityPage, 0, host)
	hosts, _ := db.ListHosts(a.db)
	a.renderNext(w, "activity.html", map[string]any{
		"Entries": entries, "NextOffset": activityPage, "HasMore": len(entries) == activityPage,
		"Hosts": hosts, "Host": host,
	})
}

func (a *app) nextActivityRows(w http.ResponseWriter, r *http.Request) {
	offset, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("offset")))
	if offset < 0 {
		offset = 0
	}
	host := strings.TrimSpace(r.URL.Query().Get("host"))
	entries, _ := db.ListActivity(a.db, activityPage, offset, host)
	a.renderNext(w, "activityrows", map[string]any{
		"Entries": entries, "NextOffset": offset + activityPage, "HasMore": len(entries) == activityPage, "Host": host,
	})
}
