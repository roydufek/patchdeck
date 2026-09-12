package main

import (
	"bytes"
	"strings"
	"testing"

	"patchdeck/api/internal/models"
)

// TestSnapshotClassification checks the db-snapshot -> view derivation (fillFromSnapshot) and the
// fleet counts (buildSummary) for the restart-vs-reboot split, using the same postures the
// restart-intel demo seeds: a host with a reboot-only service but NO kernel reboot flag must be
// classified reboot (not restart); a kernel-reboot host is reboot; a restart-only host is restart.
func TestSnapshotClassification(t *testing.T) {
	// orion-style: no kernel reboot, but dbus is a learned reboot-only unit -> RebootAny, not restart.
	rebootOnlySvc := models.ScanSnapshot{NeedsRestart: []string{"systemd-logind.service", "dbus.service"}, RestartServices: []string{"systemd-logind.service"}, RebootServices: []string{"dbus.service"}}
	// cosmos-style: kernel reboot flag set.
	kernelReboot := models.ScanSnapshot{NeedsReboot: true, NeedsRestart: []string{"cron.service"}, RestartServices: []string{"cron.service"}, RebootServices: []string{"containerd.service"}}
	// restart-only: restartable units, no reboot at all.
	restartOnly := models.ScanSnapshot{NeedsRestart: []string{"cron.service"}, RestartServices: []string{"cron.service"}}
	healthy := models.ScanSnapshot{}

	check := func(name string, s models.ScanSnapshot, wantReboot, wantRestartOnly bool) nextHostView {
		var v nextHostView
		fillFromSnapshot(&v, s)
		if v.RebootAny != wantReboot {
			t.Errorf("%s: RebootAny=%v want %v", name, v.RebootAny, wantReboot)
		}
		if v.RestartOnly != wantRestartOnly {
			t.Errorf("%s: RestartOnly=%v want %v", name, v.RestartOnly, wantRestartOnly)
		}
		return v
	}
	vRebootOnly := check("reboot-only-service", rebootOnlySvc, true, false)
	vKernel := check("kernel-reboot", kernelReboot, true, false)
	vRestart := check("restart-only", restartOnly, false, true)
	vHealthy := check("healthy", healthy, false, false)

	s, _, _ := buildSummary([]nextHostView{vRebootOnly, vKernel, vRestart, vHealthy})
	if s.RebootHosts != 2 {
		t.Errorf("RebootHosts=%d want 2", s.RebootHosts)
	}
	if s.RestartHosts != 1 {
		t.Errorf("RestartHosts=%d want 1", s.RestartHosts)
	}
}

// exec renders a template by name and fails on any execution error (a struct field typo in a
// template surfaces here, not at compile time — and renderNext only logs such errors in prod).
func exec(t *testing.T, name string, data any) string {
	t.Helper()
	var b bytes.Buffer
	if err := nextTmpl.ExecuteTemplate(&b, name, data); err != nil {
		t.Fatalf("execute %q: %v", name, err)
	}
	return b.String()
}

func mustContain(t *testing.T, name, out, want string) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Errorf("%s: expected output to contain %q\n---\n%s", name, want, out)
	}
}

func mustNotContain(t *testing.T, name, out, bad string) {
	t.Helper()
	if strings.Contains(out, bad) {
		t.Errorf("%s: output should NOT contain %q\n---\n%s", name, bad, out)
	}
}

// TestCardRestartRebootPresentation covers the restart-vs-reboot split added for the bulk
// restart / reboot-chip work: a reboot-needed host shows the reboot chip + reboot facet flag and
// no restart trigger; a restart-only host shows the restart chip + restart facet flag + a hidden
// restart bulk trigger; a reboot-needed host is never also offered for a restart.
func TestCardRestartRebootPresentation(t *testing.T) {
	rebootHost := nextHostView{
		ID: "h-reboot", Name: "reboot-box", Address: "10.0.0.1", HasScan: true,
		RebootAny: true, RebootServices: []string{"containerd.service"}, RestartServices: []string{"containerd.service"},
		RestartCount: 1,
	}
	restartHost := nextHostView{
		ID: "h-restart", Name: "restart-box", Address: "10.0.0.2", HasScan: true,
		RestartOnly: true, RestartServices: []string{"cron.service", "rsyslog.service"}, RestartCount: 2,
	}
	healthyHost := nextHostView{ID: "h-ok", Name: "ok-box", Address: "10.0.0.3", HasScan: true}

	// Reboot-needed: crit reboot chip, reboot facet, NO restart facet/trigger.
	out := exec(t, "card", rebootHost)
	mustContain(t, "card/reboot", out, "Reboot needed")
	mustContain(t, "card/reboot", out, `data-reboot="1"`)
	mustContain(t, "card/reboot", out, `data-restart="0"`)
	mustNotContain(t, "card/reboot", out, `data-pdbulk="restart"`)

	// Restart-only: amber restart chip, restart facet, a hidden restart bulk trigger, NOT reboot.
	out = exec(t, "card", restartHost)
	mustContain(t, "card/restart", out, "2 restarts")
	mustContain(t, "card/restart", out, `data-restart="1"`)
	mustContain(t, "card/restart", out, `data-reboot="0"`)
	mustContain(t, "card/restart", out, `data-pdbulk="restart"`)
	mustContain(t, "card/restart", out, "/hosts/h-restart/restart?ctx=bulk")
	mustNotContain(t, "card/restart", out, "Reboot needed")

	// Healthy: up-to-date line, neither facet.
	out = exec(t, "card", healthyHost)
	mustContain(t, "card/healthy", out, "Up to date")
	mustContain(t, "card/healthy", out, `data-reboot="0"`)
	mustContain(t, "card/healthy", out, `data-restart="0"`)
}

// TestActionresultAutoRescan verifies the bulk restart path auto-fires a scan (no manual button),
// while the non-bulk path keeps the manual "Re-scan to refresh" control.
func TestActionresultAutoRescan(t *testing.T) {
	// Bulk restart: auto-rescan into the card (default ctx => card swap on done), targeting the
	// result's own element, and NO manual button.
	bulk := exec(t, "actionresult", map[string]any{"ID": "h1", "Title": "Services restarted", "OK": "done", "AutoRescan": true, "Bulk": true})
	mustContain(t, "actionresult/bulk", bulk, `hx-get="/hosts/h1/scanpanel"`)
	mustContain(t, "actionresult/bulk", bulk, `hx-target="#actn-h1"`)
	mustNotContain(t, "actionresult/bulk", bulk, "scanpanel?ctx=detail")
	mustNotContain(t, "actionresult/bulk", bulk, "Re-scan to refresh")

	// Single per-host restart (detail): auto-rescan with ctx=detail (reload on done), same self-target.
	detail := exec(t, "actionresult", map[string]any{"ID": "h1", "Title": "Services restarted", "OK": "done", "AutoRescan": true, "Bulk": false})
	mustContain(t, "actionresult/detail", detail, "/hosts/h1/scanpanel?ctx=detail")
	mustContain(t, "actionresult/detail", detail, `hx-target="#actn-h1"`)
	mustNotContain(t, "actionresult/detail", detail, "Re-scan to refresh")

	// Partial-failure: the per-service breakdown renders, the header shows a problem dot (not the
	// check), and it STILL auto-rescans (never a blanket dead-end).
	partial := exec(t, "actionresult", map[string]any{
		"ID": "h1", "Title": "Restart finished with issues", "HasIssues": true,
		"OK":         "Restarted 12 of 13 service(s); 1 could not be restarted (see below). Re-scanning to confirm.",
		"Breakdown":  []string{"✓ cron.service — restart dispatched (detached; reconnecting to confirm)", "✗ apcupsd.service — Password: su: Authentication failure"},
		"AutoRescan": true, "Bulk": true,
	})
	mustContain(t, "actionresult/partial", partial, "✓ cron.service")
	mustContain(t, "actionresult/partial", partial, "✗ apcupsd.service")
	mustContain(t, "actionresult/partial", partial, "1 could not be restarted")
	mustContain(t, "actionresult/partial", partial, `hx-get="/hosts/h1/scanpanel"`) // still auto-rescans
	mustContain(t, "actionresult/partial", partial, "dot-down")                      // problem indicator, not the check
}

// TestRebootWatchAutoScan verifies the reconnect branch auto-scans instead of offering Refresh,
// targeting the card (bulk) or the detail power area (detail), and that the still-offline poll
// preserves ctx.
func TestRebootWatchAutoScan(t *testing.T) {
	bulk := exec(t, "rebootwatch", map[string]any{"ID": "h9", "Connected": true, "Ctx": "bulk"})
	mustContain(t, "rebootwatch/bulk", bulk, `hx-target="#rbw-h9"`)
	mustContain(t, "rebootwatch/bulk", bulk, `hx-get="/hosts/h9/scanpanel"`)
	mustNotContain(t, "rebootwatch/bulk", bulk, "Refresh")
	mustNotContain(t, "rebootwatch/bulk", bulk, "scanpanel?ctx=detail") // bulk => card-swap scan, not detail reload

	detail := exec(t, "rebootwatch", map[string]any{"ID": "h9", "Connected": true, "Ctx": ""})
	mustContain(t, "rebootwatch/detail", detail, `hx-target="#rbw-h9"`)
	mustContain(t, "rebootwatch/detail", detail, "scanpanel?ctx=detail")

	offline := exec(t, "rebootwatch", map[string]any{"ID": "h9", "Connected": false, "Ctx": "bulk"})
	mustContain(t, "rebootwatch/offline", offline, "/hosts/h9/reboot-watch?ctx=bulk")

	panel := exec(t, "rebootpanel", map[string]any{"ID": "h9", "Name": "n", "Ctx": "bulk"})
	mustContain(t, "rebootpanel", panel, "/hosts/h9/reboot-watch?ctx=bulk")
}
