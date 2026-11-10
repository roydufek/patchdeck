package main

import (
	"bytes"
	"strings"
	"testing"

	"patchdeck/api/internal/models"
	"patchdeck/api/internal/tasks"
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
	mustNotContain(t, "card/restart", out, "Reboot needed")

	// Healthy: up-to-date line, neither facet.
	out = exec(t, "card", healthyHost)
	mustContain(t, "card/healthy", out, "Up to date")
	mustContain(t, "card/healthy", out, `data-reboot="0"`)
	mustContain(t, "card/healthy", out, `data-restart="0"`)

	// A restart-only host that is self / operator-protected still shows the restart chip + facet, but
	// gets NO bulk restart trigger (never swept by "Restart all" — matches "Reboot all").
	selfRestart := nextHostView{ID: "h-self", Name: "self", HasScan: true, RestartOnly: true, RestartServices: []string{"docker.service"}, IsSelf: true}
	out = exec(t, "card", selfRestart)
	mustContain(t, "card/self-restart", out, "1 restart")
	mustContain(t, "card/self-restart", out, `data-restart="1"`)
	mustNotContain(t, "card/self-restart", out, `data-pdbulk="restart"`)

	protRestart := nextHostView{ID: "h-prot", Name: "prot", HasScan: true, RestartOnly: true, RestartServices: []string{"cron.service"}, ExcludeFromBulk: true}
	out = exec(t, "card", protRestart)
	mustNotContain(t, "card/protected-restart", out, `data-pdbulk="restart"`)

	// Card buttons start server-side tasks (decision 0009), not browser-driven streams.
	otherUpd := nextHostView{ID: "h-o", Name: "o", HasScan: true}
	mustContain(t, "card/scan", exec(t, "card", otherUpd), `hx-post="/tasks" hx-vals='{"kind":"scan","host":"h-o"}'`)
}

// TestBusyCard: a busy host's card loads its progress panel and offers no action buttons, so a
// second action can't be started on it from this page.
func TestBusyCard(t *testing.T) {
	busy := nextHostView{ID: "h1", Name: "one", HasScan: true, UpdateCount: 2, Busy: true}
	out := exec(t, "card", busy)
	mustContain(t, "card/busy", out, `data-busy="1"`)
	mustContain(t, "card/busy", out, `hx-get="/hosts/h1/taskpanel" hx-trigger="load"`)
	mustNotContain(t, "card/busy", out, "Apply 2")
	mustNotContain(t, "card/busy", out, `"kind":"scan"`)

	idle := exec(t, "card", nextHostView{ID: "h1", Name: "one", HasScan: true, UpdateCount: 2})
	mustContain(t, "card/idle", idle, "Apply 2")
	mustNotContain(t, "card/idle", idle, "taskpanel")
}

// TestTaskPanel renders a running host's panel (with its buffered lines + last seq, so the page
// never repeats a line) and an idle host's (empty, so the card refreshes).
func TestTaskPanel(t *testing.T) {
	run := exec(t, "taskpanel", taskPanelView{ID: "h1", Name: "one", Label: "Applying updates", Status: "running", Busy: true, Managed: true,
		Since: "2m ago", Seq: 2, Lines: []tasks.Line{{Seq: 1, Text: "Reading package lists"}, {Seq: 2, Class: "ok", Text: "<done>"}}})
	mustContain(t, "taskpanel/running", run, `id="task-h1"`)
	mustContain(t, "taskpanel/running", run, `data-pdtask="h1"`)
	mustContain(t, "taskpanel/running", run, `id="tasklog-h1" class="scanlog" data-seq="2"`)
	mustContain(t, "taskpanel/running", run, "Applying updates on one")
	mustContain(t, "taskpanel/running", run, "&lt;done&gt;") // output is escaped
	q := exec(t, "taskpanel", taskPanelView{ID: "h1", Name: "one", Label: "Scanning", Status: "queued", Busy: true, Managed: true})
	mustContain(t, "taskpanel/queued", q, "Queued: Scanning on one")
	sched := exec(t, "taskpanel", taskPanelView{ID: "h1", Name: "one", Label: "Scheduled apply", Status: "running", Busy: true})
	mustContain(t, "taskpanel/scheduled", sched, "Started by a schedule or the API")
	// An idle host's panel is just a marker: the page swaps it for a refreshed card.
	idle := exec(t, "taskpanel", taskPanelView{ID: "h1"})
	mustContain(t, "taskpanel/idle", idle, `data-pdidle="h1"`)
	mustNotContain(t, "taskpanel/idle", idle, "data-pdtask")
	banner := exec(t, "taskbanner", taskBanner{ID: "t1", What: "Apply on 3 host(s)", Hosts: "nova, orbit", OK: 1, Not: 2})
	mustContain(t, "taskbanner", banner, `hx-post="/tasks/t1/resume"`)
	mustContain(t, "taskbanner", banner, "2 not run")
}

// TestPickSelection: an explicitly selected PROTECTED host is included in a reboot (and named, for
// the warning); Patchdeck's own host, busy hosts, unapproved hosts and hosts the action doesn't fit
// are left out WITH the reason (decision 0009).
func TestPickSelection(t *testing.T) {
	views := []nextHostView{
		{ID: "rb", Name: "rb", RebootAny: true},
		{ID: "prot", Name: "prot", RebootAny: true, ExcludeFromBulk: true},
		{ID: "self", Name: "self", RebootAny: true, IsSelf: true, UpdateCount: 2},
		{ID: "busy", Name: "busy", RebootAny: true, Busy: true},
		{ID: "new", Name: "new", RebootAny: true, PendingKey: true},
		{ID: "ok", Name: "ok"},
		{ID: "notsel", Name: "notsel", RebootAny: true},
	}
	ids := []string{"rb", "prot", "self", "busy", "new", "ok"}
	s := (&app{}).pickSelection(tasks.KindReboot, views, ids)
	if got := s.names(); got != "rb, prot" {
		t.Errorf("reboot takes %s, want rb,prot", got)
	}
	if strings.Join(s.Protected, ",") != "prot" {
		t.Errorf("protected = %v, want [prot]", s.Protected)
	}
	left := strings.Join(s.LeftOut, "; ")
	for _, want := range []string{"self (Patchdeck's own host)", "busy (busy right now)", "new (awaiting key approval)", "ok (doesn't need a reboot)"} {
		if !strings.Contains(left, want) {
			t.Errorf("left out %q missing %q", left, want)
		}
	}
	// Apply on the self host is fine (it runs last); restart never touches a reboot-needing host.
	if a := (&app{}).pickSelection(tasks.KindApply, views, ids); a.names() != "self" || !a.HasSelf {
		t.Errorf("apply takes %v (self=%v), want [self]", a.names(), a.HasSelf)
	}
	if r := (&app{}).pickSelection(tasks.KindRestart, views, ids); len(r.Take) != 0 {
		t.Errorf("restart should take nothing here, took %v", r.names())
	}
	if s.Busy != 1 {
		t.Errorf("busy count %d, want 1", s.Busy)
	}
	// The card carries what fits it, so the select bar never re-derives the rules.
	for v, want := range map[string]string{
		fitsList(nextHostView{UpdateCount: 2}):                         "scan apply",
		fitsList(nextHostView{RebootAny: true, IsSelf: true}):          "scan",
		fitsList(nextHostView{RebootAny: true, ExcludeFromBulk: true}): "scan reboot",
		fitsList(nextHostView{RestartOnly: true}):                      "scan restart",
		fitsList(nextHostView{PendingKey: true, UpdateCount: 1}):       "",
	} {
		if v != want {
			t.Errorf("fitsList = %q, want %q", v, want)
		}
	}
	mustContain(t, "card/fits", exec(t, "card", nextHostView{ID: "x", Name: "x", HasScan: true, UpdateCount: 1, Fits: "scan apply"}), `data-fits="scan apply"`)

	out := exec(t, "selconfirm", selConfirmView{Kind: "reboot", Lead: "Reboot", Count: 2, Names: "rb, prot", IDs: "rb,prot",
		Button: "Reboot 2 now (incl. 1 protected)", Protected: []string{"prot"}, Warn: "is protected from fleet reboots: whatever runs there (watchers, monitoring) is down until it's back.",
		LeftOut: "self (Patchdeck's own host)"})
	mustContain(t, "selconfirm", out, "Reboot 2 now (incl. 1 protected)")
	mustContain(t, "selconfirm", out, "<b>prot</b> is protected from fleet reboots")
	mustContain(t, "selconfirm", out, `"scope":"selected","hosts":"rb,prot"`)
	mustContain(t, "selconfirm", out, "Left out: self (Patchdeck&#39;s own host)")
	none := exec(t, "selconfirm", selConfirmView{Kind: "apply", None: "updated", LeftOut: "ok (no updates)"})
	mustContain(t, "selconfirm/none", none, "None of the selected hosts can be updated right now.")
	mustNotContain(t, "selconfirm/none", none, "hx-post")
}

// TestEligibleForAll: the "… all" buttons act on exactly the hosts the dashboard counts. The
// Patchdeck host and protected hosts never get a fleet-wide reboot or restart, and unapproved
// hosts get nothing.
func TestEligibleForAll(t *testing.T) {
	views := []nextHostView{
		{ID: "upd", UpdateCount: 2},
		{ID: "rb", RebootAny: true},
		{ID: "rb-self", RebootAny: true, IsSelf: true},
		{ID: "rb-prot", RebootAny: true, ExcludeFromBulk: true},
		{ID: "rs", RestartOnly: true},
		{ID: "new", Unverified: true, UpdateCount: 5},
	}
	ids := func(kind string) string {
		var out []string
		for _, h := range eligibleForAll(kind, views) {
			out = append(out, h.ID)
		}
		return strings.Join(out, ",")
	}
	for kind, want := range map[string]string{
		tasks.KindApply: "upd", tasks.KindReboot: "rb", tasks.KindRestart: "rs",
		tasks.KindScan: "upd,rb,rb-self,rb-prot,rs",
	} {
		if got := ids(kind); got != want {
			t.Errorf("%s: got %s want %s", kind, got, want)
		}
	}
}
