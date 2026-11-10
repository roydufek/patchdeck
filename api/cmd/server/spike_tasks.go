package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"patchdeck/api/internal/auth"
	"patchdeck/api/internal/db"
	"patchdeck/api/internal/models"
	"patchdeck/api/internal/sshx"
	"patchdeck/api/internal/tasks"
)

// spike_tasks.go — host actions run as server-side tasks (internal/tasks, decision 0009). Every
// scan / apply / restart / reboot button posts to /tasks; the server runs it whether or not the
// page stays open, and the dashboard follows /events to show progress on each card.

const (
	rebootFirstCheck = 12 * time.Second // give the host time to go down before the first check
	rebootPoll       = 8 * time.Second
	rebootGiveUp     = 15 * time.Minute
	restartSettle    = 2500 * time.Millisecond // let detached restarts settle before the re-scan
)

func (a *app) newTaskManager() *tasks.Manager {
	return tasks.New(a.db, tasks.Config{
		Concurrency:  a.cfg.BulkConcurrency,
		ApplyStagger: time.Duration(a.cfg.ApplyStaggerSeconds) * time.Second,
		IsSelf: func(hostID string) bool {
			snap, _, err := db.GetScanSnapshot(a.db, hostID)
			return err == nil && a.isSelfHost(snap)
		},
		Steps: map[string]tasks.Step{
			tasks.KindScan:    a.stepScan,
			tasks.KindApply:   a.stepApply,
			tasks.KindRestart: a.stepRestart,
			tasks.KindReboot:  a.stepReboot,
		},
		// An apply keeps running detached on the host and a reboot's watch only needs the host back:
		// both are picked up again after a Patchdeck restart. Scan and restart are just reported.
		Resumable: map[string]bool{tasks.KindApply: true, tasks.KindReboot: true},
	})
}

func (a *app) hostBusy(hostID string) bool {
	if a.tasks == nil {
		return false
	}
	_, busy := a.tasks.Busy(hostID)
	return busy
}

// lockHost claims a host for a JSON API action, so it can't overlap a task or a scheduled job.
// It writes 409 and returns false when the host is busy; release with unlockHost.
func (a *app) lockHost(w http.ResponseWriter, hostID, what string) bool {
	if a.tasks == nil || a.tasks.TryLock(hostID, what) {
		return true
	}
	writeJSON(w, http.StatusConflict, map[string]string{"error": "host is busy with another action; try again when it finishes"})
	return false
}

func (a *app) unlockHost(hostID string) {
	if a.tasks != nil {
		a.tasks.Unlock(hostID)
	}
}

func failed(summary string) tasks.Result {
	return tasks.Result{Status: tasks.StFailed, Summary: summary}
}

func hostErrText(err error) string {
	var hkErr *sshx.HostKeyError
	if errors.As(err, &hkErr) {
		return hkErr.Message
	}
	return err.Error()
}

// rescan re-scans a host after an action so its card shows the new state.
func (a *app) rescan(ctx context.Context, host models.Host, r *tasks.Run) {
	r.Line("", "Re-scanning…")
	sres, err := a.sshClient.ScanHostStreaming(ctx, host, a.secrets, func(string) {})
	if err != nil {
		r.Line("err", "Re-scan failed: "+hostErrText(err)+". Scan again to refresh this host.")
		return
	}
	_ = db.UpsertScanResult(a.db, host.ID, sres)
	r.Line("ok", fmt.Sprintf("Now %d update(s) remaining.", len(sres.Packages)))
}

func (a *app) stepScan(ctx context.Context, r *tasks.Run) tasks.Result {
	host, err := db.GetHost(a.db, r.HostID)
	if err != nil {
		return failed("host not found")
	}
	r.Line("", "Connecting to "+host.Name+"…")
	prev, _, _ := db.GetScanSnapshot(a.db, host.ID) // before the upsert, for new-updates detection
	res, err := a.sshClient.ScanHostStreaming(ctx, host, a.secrets, func(l string) { r.Line("", l) })
	if err != nil {
		a.notifyScanFailure(host, fmt.Sprintf("Patchdeck: scan FAILED on %s (%v)", host.Name, err), err.Error())
		_ = db.RecordActivity(a.db, host.ID, host.Name, "scan_fail", fmt.Sprintf("Scan failed: %v", err))
		r.Line("err", "Scan failed: "+hostErrText(err))
		return failed(hostErrText(err))
	}
	_ = db.UpsertScanResult(a.db, host.ID, res)
	_ = db.RecordActivity(a.db, host.ID, host.Name, "scan_ok", fmt.Sprintf("Scan completed: %d packages available", len(res.Packages)))
	a.notifyIfNewUpdates(host, prev.Packages, res)
	msg := fmt.Sprintf("Scan complete — %d update(s) available.", len(res.Packages))
	r.Line("ok", msg)
	return tasks.Result{Status: tasks.StOK, Summary: msg}
}

// stepApply runs the upgrade detached on the host (decision 0008) and records its id, so a
// Patchdeck restart mid-apply reattaches instead of losing it. A multi-host task notifies through
// the digest (one message for the batch); a single-host apply stays quiet as before.
func (a *app) stepApply(ctx context.Context, r *tasks.Run) tasks.Result {
	host, err := db.GetHost(a.db, r.HostID)
	if err != nil {
		return failed("host not found")
	}
	emit := func(l string) { r.Line("", l) }
	var res models.ApplyResult
	if r.Ref != "" {
		r.Line("", "Patchdeck restarted during this apply. Reattaching to the upgrade still running on "+host.Name+"…")
		res, err = a.sshClient.ReattachApply(ctx, host, a.secrets, r.Ref, emit)
	} else {
		r.Line("", "Applying updates on "+host.Name+"…")
		res, err = a.sshClient.ApplyUpdatesTracked(ctx, host, a.secrets, r.SetRef, emit)
	}
	if errors.Is(err, sshx.ErrApplyNotStarted) {
		// Patchdeck stopped after recording the run's id but before it reached the host.
		r.Line("", "The upgrade never started on the host before Patchdeck restarted. Use Resume remaining to run it.")
		return tasks.Result{Status: tasks.StInterrupted, Summary: "not run: Patchdeck restarted before the upgrade started"}
	}
	if err != nil {
		var interrupted *sshx.ApplyInterruptedError
		if errors.As(err, &interrupted) {
			_ = db.RecordActivity(a.db, host.ID, host.Name, "apply_interrupted", fmt.Sprintf("Lost the running upgrade and couldn't reattach within 5 min (%d changed so far) — host may be rebooting or unreachable", interrupted.ChangedSoFar))
			r.Line("err", "Couldn't reattach to the running upgrade for 5 minutes. The host may be rebooting or unreachable. Re-scan to verify.")
		} else {
			_ = db.RecordActivity(a.db, host.ID, host.Name, "apply_fail", fmt.Sprintf("Apply failed: %v", err))
			r.Line("err", "Apply failed: "+hostErrText(err))
		}
		if r.Hosts > 1 {
			a.notifyApplyFailure(host, err)
		}
		return failed(hostErrText(err))
	}
	_ = db.RecordActivity(a.db, host.ID, host.Name, "apply_ok", fmt.Sprintf("Applied updates: %d packages changed", res.ChangedPackages))
	if r.Hosts > 1 {
		a.notifyApplied(host, res)
	}
	msg := fmt.Sprintf("Applied — %d package(s) changed.", res.ChangedPackages)
	r.Line("ok", msg)
	a.rescan(ctx, host, r)
	return tasks.Result{Status: tasks.StOK, Summary: msg}
}

// stepRestart is the smart restart (spike_restart.go's rules): restart only the units a live
// restart can clear, mark the ones actually restarted, then re-scan. A unit still flagged on the
// same boot is learned reboot-only by that scan.
func (a *app) stepRestart(ctx context.Context, r *tasks.Run) tasks.Result {
	host, err := db.GetHost(a.db, r.HostID)
	if err != nil {
		return failed("host not found")
	}
	snap, _, err := db.GetScanSnapshot(a.db, host.ID)
	if err != nil {
		return failed("no scan to restart from")
	}
	restartable, _ := resolveRestartBuckets(snap)
	if len(restartable) == 0 {
		r.Line("ok", "No services are flagged for a restart right now.")
		return tasks.Result{Status: tasks.StSkipped, Summary: "nothing to restart"}
	}
	r.Line("", fmt.Sprintf("Restarting %d service(s) on %s…", len(restartable), host.Name))
	res, err := a.sshClient.RestartDeferredDetached(host, a.secrets, restartable)
	var hkErr *sshx.HostKeyError
	if errors.As(err, &hkErr) {
		_ = db.RecordActivity(a.db, host.ID, host.Name, "restart_fail", fmt.Sprintf("Smart restart failed: %s", hkErr.Message))
		r.Line("err", "Restart failed: "+hkErr.Message)
		return failed(hkErr.Message)
	}
	marked := 0
	for _, svc := range res.Dispatched {
		if e := db.SetRestartMark(a.db, host.ID, svc, snap.BootID); e == nil {
			marked++
		}
	}
	for _, l := range strings.Split(strings.TrimSpace(res.Output), "\n") {
		if strings.TrimSpace(l) != "" {
			r.Line("", l)
		}
	}
	for _, svc := range res.RebootRequired {
		r.Line("", "• "+svc+" still needs a reboot to fully apply")
	}
	kind := "restart_ok"
	if len(res.Failed) > 0 {
		kind = "restart_fail"
	}
	_ = db.RecordActivity(a.db, host.ID, host.Name, kind,
		fmt.Sprintf("Smart restart: %d dispatched (%d watched), %d failed, %d reboot-required", len(res.Dispatched), marked, len(res.Failed), len(res.RebootRequired)))
	msg := fmt.Sprintf("Restarted %d service(s).", len(res.Dispatched))
	if len(res.Failed) > 0 {
		msg = fmt.Sprintf("Restarted %d service(s); %d could not be restarted.", len(res.Dispatched), len(res.Failed))
		r.Line("err", msg)
	} else {
		r.Line("ok", msg)
	}
	time.Sleep(restartSettle)
	a.rescan(ctx, host, r)
	if len(res.Failed) > 0 {
		return failed(msg)
	}
	return tasks.Result{Status: tasks.StOK, Summary: msg}
}

// stepReboot reboots the host, waits for it to come back (its uptime restarting proves it really
// rebooted), then re-scans to clear its flags. The ref records when the reboot was sent, so a
// Patchdeck restart in between (e.g. it rebooted its own host) resumes the watch.
func (a *app) stepReboot(ctx context.Context, r *tasks.Run) tasks.Result {
	host, err := db.GetHost(a.db, r.HostID)
	if err != nil {
		return failed("host not found")
	}
	sent := time.Now()
	if r.Ref != "" {
		if sec, err := strconv.ParseInt(strings.TrimPrefix(r.Ref, "sent="), 10, 64); err == nil {
			sent = time.Unix(sec, 0)
		}
		r.Line("", "Patchdeck restarted during this reboot. Watching for "+host.Name+" to come back…")
	} else {
		if err := a.sshClient.Power(host, a.secrets, "reboot"); err != nil {
			_ = db.RecordActivity(a.db, host.ID, host.Name, "reboot_fail", fmt.Sprintf("Reboot failed: %v", err))
			r.Line("err", "Reboot failed: "+hostErrText(err))
			return failed(hostErrText(err))
		}
		r.SetRef(fmt.Sprintf("sent=%d", sent.Unix()))
		_ = db.RecordActivity(a.db, host.ID, host.Name, "reboot_ok", fmt.Sprintf("Reboot initiated on %s", host.Name))
		r.Line("", "Reboot initiated. Waiting for "+host.Name+" to come back…")
		time.Sleep(rebootFirstCheck)
	}
	checker := a.sshClient.WithTimeouts(a.cfg.ConnectivityTimeout, a.cfg.ConnectivityTimeout)
	for tries := 0; ; tries++ {
		st := a.probeConnectivity(checker, host.ID)
		// Back = reachable AND booted after the reboot was sent (a host that hasn't gone down yet
		// still answers, with its old uptime). A host whose uptime can't be read counts as back
		// once reachable again, as before.
		if st.Connected && (st.UptimeSeconds <= 0 || time.Duration(st.UptimeSeconds)*time.Second < time.Since(sent)+time.Minute) {
			break
		}
		if time.Since(sent) > rebootGiveUp {
			msg := fmt.Sprintf("%s didn't come back within %d minutes. Check it, then scan.", host.Name, int(rebootGiveUp.Minutes()))
			_ = db.RecordActivity(a.db, host.ID, host.Name, "reboot_fail", msg)
			r.Line("err", msg)
			return failed(msg)
		}
		if tries%4 == 3 {
			r.Line("", fmt.Sprintf("Still offline after %s…", time.Since(sent).Round(time.Second)))
		}
		time.Sleep(rebootPoll)
	}
	r.Line("ok", fmt.Sprintf("Back online after %s.", time.Since(sent).Round(time.Second)))
	a.rescan(ctx, host, r)
	return tasks.Result{Status: tasks.StOK, Summary: "rebooted"}
}

// actionFits reports whether kind applies to a host, and if not, why (for the selection confirm).
// Protection is NOT checked here: "… all" leaves protected hosts out, an explicit selection keeps
// them (with a warning, decision 0009). Patchdeck's own host never gets a bulk reboot or restart.
func actionFits(kind string, v nextHostView) (bool, string) {
	if v.Unverified || v.PendingKey {
		return false, "awaiting key approval"
	}
	switch kind {
	case tasks.KindScan:
		return true, ""
	case tasks.KindApply:
		if v.UpdateCount == 0 {
			return false, "no updates"
		}
		return true, ""
	case tasks.KindReboot:
		switch {
		case v.IsSelf:
			return false, "Patchdeck's own host"
		case !v.RebootAny:
			return false, "doesn't need a reboot"
		}
		return true, ""
	case tasks.KindRestart:
		switch {
		case v.IsSelf:
			return false, "Patchdeck's own host"
		case v.RebootAny:
			return false, "needs a reboot instead"
		case !v.RestartOnly:
			return false, "nothing to restart"
		}
		return true, ""
	}
	return false, "unknown action"
}

// protectedFrom: a protected host ("Protect from Reboot all") is kept out of these actions when
// they run fleet-wide, and flagged when the operator selects it on purpose.
func protectedFrom(kind string, v nextHostView) bool {
	return v.ExcludeFromBulk && (kind == tasks.KindReboot || kind == tasks.KindRestart)
}

// fitsList is the card's data-fits: the actions a selection may run on this host (the select bar
// counts from it, so the browser never re-derives the rules).
func fitsList(v nextHostView) string {
	var out []string
	for _, k := range []string{tasks.KindScan, tasks.KindApply, tasks.KindRestart, tasks.KindReboot} {
		if ok, _ := actionFits(k, v); ok {
			out = append(out, k)
		}
	}
	return strings.Join(out, " ")
}

// eligibleForAll returns the hosts an "… all" button acts on: exactly the set the dashboard's
// counts show (the self-host and protected hosts never get a fleet-wide reboot or restart).
func eligibleForAll(kind string, views []nextHostView) []tasks.HostRef {
	var out []tasks.HostRef
	for _, v := range views {
		if ok, _ := actionFits(kind, v); !ok || protectedFrom(kind, v) {
			continue
		}
		out = append(out, tasks.HostRef{ID: v.ID, Name: v.Name})
	}
	return out
}

// selection is what an action on the selected cards would do.
type selection struct {
	Take      []tasks.HostRef
	Protected []string // protected hosts included on purpose (reboot / restart)
	LeftOut   []string // "name (why)"
	Busy      int      // selected hosts left out only because they're busy
	HasSelf   bool     // Patchdeck's own host is included (an apply runs it last)
}

// names and ids of the hosts a selection runs on.
func (s selection) names() string {
	return joinHosts(s.Take, func(h tasks.HostRef) string { return h.Name })
}
func (s selection) ids() string {
	return joinHosts(s.Take, func(h tasks.HostRef) string { return h.ID })
}

func joinHosts(hs []tasks.HostRef, f func(tasks.HostRef) string) string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = f(h)
	}
	return strings.Join(out, ", ")
}

func (a *app) pickSelection(kind string, views []nextHostView, ids []string) selection {
	want := map[string]bool{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	var s selection
	for _, v := range views {
		if !want[v.ID] {
			continue
		}
		ok, why := actionFits(kind, v)
		if ok && v.Busy {
			ok, why = false, "busy right now"
			s.Busy++
		}
		if !ok {
			s.LeftOut = append(s.LeftOut, v.Name+" ("+why+")")
			continue
		}
		s.Take = append(s.Take, tasks.HostRef{ID: v.ID, Name: v.Name})
		if protectedFrom(kind, v) {
			s.Protected = append(s.Protected, v.Name)
		}
		if v.IsSelf {
			s.HasSelf = true
		}
	}
	return s
}

// selConfirmView is the confirm for an action on the selected cards.
type selConfirmView struct {
	Kind, Lead, Note, Warn, Button, None, IDs, Names, LeftOut string
	Count                                                     int
	Protected                                                 []string
}

// nextSelConfirm renders the confirm for an action on the selected cards: the hosts it runs on,
// the protected ones it includes on purpose, and what it leaves out and why.
func (a *app) nextSelConfirm(w http.ResponseWriter, r *http.Request) {
	kind, ok := taskKind(r.FormValue("kind"))
	if !ok {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	views, err := a.nextHostViews()
	if err != nil {
		http.Error(w, "failed to load hosts", http.StatusInternalServerError)
		return
	}
	s := a.pickSelection(kind, views, strings.Split(r.FormValue("hosts"), ","))
	v := selConfirmView{Kind: kind, Count: len(s.Take), IDs: strings.ReplaceAll(s.ids(), " ", ""), Names: s.names(),
		LeftOut: strings.Join(s.LeftOut, ", "), Protected: s.Protected}
	n := len(s.Take)
	switch kind {
	case tasks.KindScan:
		v.Lead, v.Button, v.None = "Scan", fmt.Sprintf("Scan %d now", n), "scanned"
	case tasks.KindApply:
		v.Lead, v.Button, v.None = "Apply updates on", fmt.Sprintf("Apply on %d now", n), "updated"
		v.Note = "Each runs apt upgrade and may restart services."
		if s.HasSelf {
			v.Note += " The host Patchdeck runs on goes last."
		}
	case tasks.KindRestart:
		v.Lead, v.Button, v.None = "Restart flagged services on", fmt.Sprintf("Restart %d now", n), "restarted"
		v.Warn = "is protected from fleet-wide actions: its services restart under whatever runs there."
	case tasks.KindReboot:
		v.Lead, v.Button, v.None = "Reboot", fmt.Sprintf("Reboot %d now", n), "rebooted"
		v.Note = "Each drops offline for a minute or two; Patchdeck watches each one come back."
		v.Warn = "is protected from fleet reboots: whatever runs there (watchers, monitoring) is down until it's back."
	}
	if p := len(s.Protected); p > 0 {
		v.Button += fmt.Sprintf(" (incl. %d protected)", p)
	}
	a.renderNext(w, "selconfirm", v)
}

func taskKind(s string) (string, bool) {
	switch s {
	case tasks.KindScan, tasks.KindApply, tasks.KindRestart, tasks.KindReboot:
		return s, true
	}
	return "", false
}

func (a *app) taskUser(r *http.Request) string {
	if c, ok := auth.ClaimsFromContext(r.Context()); ok {
		return c.Username
	}
	return ""
}

// nextTaskStart starts an action. host=<id> → that host; the response is its progress panel.
// scope=all → every eligible host; scope=selected + hosts=<ids> → the selected cards it applies to.
// For both, the response is a one-line note, and the cards follow /events.
func (a *app) nextTaskStart(w http.ResponseWriter, r *http.Request) {
	kind, ok := taskKind(r.FormValue("kind"))
	if !ok {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if scope := r.FormValue("scope"); scope == "all" || scope == "selected" {
		views, err := a.nextHostViews()
		if err != nil {
			http.Error(w, "failed to load hosts", http.StatusInternalServerError)
			return
		}
		var hosts []tasks.HostRef
		busySel := 0
		if scope == "all" {
			hosts = eligibleForAll(kind, views)
		} else {
			s := a.pickSelection(kind, views, strings.Split(r.FormValue("hosts"), ","))
			hosts, busySel = s.Take, s.Busy
		}
		_, n, err := a.tasks.Start(kind, a.taskUser(r), hosts)
		msg := ""
		switch {
		case err != nil:
			msg = "Couldn't start: " + err.Error()
		case n == 0 && len(hosts) == 0 && busySel > 0:
			msg = "Those hosts are busy right now. Try again once they finish."
		case n == 0 && len(hosts) == 0:
			msg = "Nothing to do: no host needs this right now."
		case n == 0:
			msg = "Every eligible host is already busy."
		default:
			msg = fmt.Sprintf("Started: %s on %d host(s). It keeps running if you leave this page.", kind, n)
			if skipped := len(hosts) - n + busySel; skipped > 0 {
				msg += fmt.Sprintf(" %d busy host(s) left out.", skipped)
			}
		}
		a.renderNext(w, "tasknote", map[string]any{"Msg": msg, "Err": err != nil})
		return
	}
	host, err := db.GetHost(a.db, r.FormValue("host"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	if _, _, err := a.tasks.Start(kind, a.taskUser(r), []tasks.HostRef{{ID: host.ID, Name: host.Name}}); err != nil {
		a.renderNext(w, "tasknote", map[string]any{"Msg": "Couldn't start: " + err.Error(), "Err": true})
		return
	}
	// Already busy → this shows what it's doing instead.
	a.renderTaskPanel(w, host.ID, host.Name)
}

// taskPanelView is what the progress panel renders.
type taskPanelView struct {
	ID, Name, Label, Status, Since string
	Busy, Managed                  bool
	Lines                          []tasks.Line
	Seq                            int
}

func (a *app) renderTaskPanel(w http.ResponseWriter, hostID, name string) {
	v := taskPanelView{ID: hostID, Name: name}
	if st, busy := a.tasks.Busy(hostID); busy {
		v.Busy, v.Managed, v.Label, v.Status = true, st.Managed, st.Label, st.Status
		v.Since = timeAgoShort(st.Since)
		v.Lines = a.tasks.Log(hostID)
		if n := len(v.Lines); n > 0 {
			v.Seq = v.Lines[n-1].Seq
		}
	}
	a.renderNext(w, "taskpanel", v)
}

func (a *app) nextTaskPanel(w http.ResponseWriter, r *http.Request) {
	host, err := db.GetHost(a.db, chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	a.renderTaskPanel(w, host.ID, host.Name)
}

func (a *app) nextTaskResume(w http.ResponseWriter, r *http.Request) {
	_, n, err := a.tasks.Resume(chi.URLParam(r, "id"), a.taskUser(r))
	msg := fmt.Sprintf("Resumed on %d host(s).", n)
	switch {
	case err != nil:
		msg = "Couldn't resume: " + err.Error()
	case n == 0:
		msg = "Those hosts are busy right now. Resume again once they finish."
	}
	a.renderNext(w, "tasknote", map[string]any{"Msg": msg, "Err": err != nil})
}

func (a *app) nextTaskDismiss(w http.ResponseWriter, r *http.Request) {
	a.tasks.Dismiss(chi.URLParam(r, "id"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
}

// taskBanner is one "cut short by a restart" notice on the dashboard.
type taskBanner struct {
	ID, What, Hosts string
	OK, Failed, Not int
}

func (a *app) taskBanners() []taskBanner {
	if a.tasks == nil {
		return nil
	}
	var out []taskBanner
	for _, s := range a.tasks.Interrupted() {
		out = append(out, taskBanner{
			ID: s.ID, What: fmt.Sprintf("%s%s on %d host(s)", strings.ToUpper(s.Kind[:1]), s.Kind[1:], s.OK+s.Failed+s.NotRun),
			Hosts: strings.Join(s.Hosts, ", "), OK: s.OK, Failed: s.Failed, Not: s.NotRun,
		})
	}
	return out
}

// nextEvents is the dashboard's live feed: "sync" (the busy hosts, sent on every connect so a
// page that was away catches up), then "state" and "line" events as tasks run.
func (a *app) nextEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ch, cancel := a.tasks.Subscribe()
	defer cancel()
	send := func(event string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
	}
	fmt.Fprint(w, "retry: 2000\n\n") // reconnect quickly after a drop
	send("sync", a.tasks.BusyHosts())
	ping := time.NewTicker(25 * time.Second) // keeps proxies from closing an idle stream
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case e, open := <-ch:
			if !open {
				return // fell behind: end the stream so the page reconnects and resyncs
			}
			switch e.Type {
			case "state":
				send("state", map[string]string{"h": e.HostID, "s": e.State})
			case "line":
				send("line", map[string]any{"h": e.HostID, "q": e.Line.Seq, "c": e.Line.Class, "t": e.Line.Text})
			}
		}
	}
}
