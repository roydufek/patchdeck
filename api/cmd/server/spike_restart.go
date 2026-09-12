package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"

	"patchdeck/api/internal/db"
	"patchdeck/api/internal/restartpolicy"
	"patchdeck/api/internal/sshx"
)

// spike_restart.go — the restart-intel app layer: one smart "Restart services" action that
// restarts only the units a live restart can actually clear (the rest are learned to be
// reboot-only), plus the self-host protection controls. The learning lives in db (restart
// marks keyed to boot_id, reconciled every scan); this layer drives it and renders the UI.

// readSelfBootID reads this container's kernel boot_id. It's the host kernel's id (boot_id is
// not namespaced), so it identifies the machine Patchdeck runs on. Best-effort: empty when the
// file is absent (non-Linux dev), which simply disables self-host auto-detection.
func readSelfBootID() string {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// nextRestartConfirmSmart renders the confirm step for the single smart restart. It names the
// units whose restart is disruptive (may drop the session or bounce containers) so the operator
// sees the blast radius honestly, without having to choose a restart strategy themselves.
func (a *app) nextRestartConfirmSmart(w http.ResponseWriter, r *http.Request) {
	v, snap, err := a.nextHostView(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	restartable, _ := resolveRestartBuckets(snap)
	var disruptive []string
	for _, s := range restartable {
		if restartpolicy.IsDisruptive(s) {
			disruptive = append(disruptive, s)
		}
	}
	a.renderNext(w, "smartrestartconfirm", map[string]any{
		"ID": v.ID, "Name": v.Name, "Count": len(restartable),
		"Disruptive": disruptive, "Out": r.URL.Query().Get("out"),
	})
}

// nextRestartSmart restarts the restartable bucket for a host: each unit via its needrestart
// coordinated handler if it has one, else a detached systemctl restart (RestartDeferredDetached
// picks per unit). Every unit we actually dispatch is stamped with a restart mark on the host's
// current boot_id; if the next scan still shows it flagged on the same boot, the reconcile
// promotes it to the reboot-only bucket (that's the learning). A reboot — via Patchdeck or out
// of band — changes boot_id and clears the marks.
func (a *app) nextRestartSmart(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	// bulk = driven by "Restart all": auto-rescan the host afterward to confirm/clear its flags,
	// instead of leaving the operator a manual "Re-scan" button (the detail-page single restart).
	bulk := r.URL.Query().Get("ctx") == "bulk"
	host, err := db.GetHost(a.db, id)
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	snap, _, err := db.GetScanSnapshot(a.db, id)
	if err != nil {
		http.Error(w, "failed to load scan", http.StatusInternalServerError)
		return
	}
	restartable, _ := resolveRestartBuckets(snap)
	if len(restartable) == 0 {
		a.renderNext(w, "actionresult", map[string]any{"ID": host.ID, "Title": "Nothing to restart", "OK": "No services are flagged for a restart right now."})
		return
	}
	res, err := a.sshClient.RestartDeferredDetached(host, a.secrets, restartable)
	// A host-key mismatch is the only thing that stops us cold — nothing was restarted, and it's
	// an approval decision, not a per-service hiccup.
	var hkErr *sshx.HostKeyError
	if errors.As(err, &hkErr) {
		_ = db.RecordActivity(a.db, host.ID, host.Name, "restart_fail", fmt.Sprintf("Smart restart failed: %s", hkErr.Message))
		a.renderNext(w, "actionresult", map[string]any{"ID": host.ID, "Title": "Restart failed", "Err": hkErr.Message})
		return
	}
	// Mark ONLY the units we actually dispatched a restart for (not the failed or reboot-required
	// ones — we never restarted those). The mark is keyed to the boot_id we saw at scan time; the
	// next scan's reconcile keeps it only if the unit is still flagged on that boot.
	marked := 0
	for _, svc := range res.Dispatched {
		if e := db.SetRestartMark(a.db, host.ID, svc, snap.BootID); e == nil {
			marked++
		}
	}
	// Partial failure is NOT a hard failure: report the per-service breakdown, keep the successes,
	// and auto-rescan regardless so the reboot-only learning still runs. Bulk re-scans into the
	// card; the detail page re-scans and refreshes in place.
	var breakdown []string
	if s := strings.TrimSpace(res.Output); s != "" {
		breakdown = strings.Split(s, "\n")
	}
	title, actKind := "Services restarted", "restart_ok"
	okMsg := fmt.Sprintf("Restarted %d service(s). Re-scanning to confirm — any that come back flagged move to “reboot required”.", len(res.Dispatched))
	if len(res.Failed) > 0 {
		title, actKind = "Restart finished with issues", "restart_fail"
		okMsg = fmt.Sprintf("Restarted %d of %d service(s); %d could not be restarted (see below). Re-scanning to confirm.",
			len(res.Dispatched), len(res.Dispatched)+len(res.Failed), len(res.Failed))
	}
	_ = db.RecordActivity(a.db, host.ID, host.Name, actKind,
		fmt.Sprintf("Smart restart: %d dispatched (%d watched), %d failed, %d reboot-required", len(res.Dispatched), marked, len(res.Failed), len(res.RebootRequired)))
	a.renderNext(w, "actionresult", map[string]any{
		"ID": host.ID, "Title": title,
		"OK":             okMsg,
		"Breakdown":      breakdown,
		"HasIssues":      len(res.Failed) > 0,
		"RebootRequired": res.RebootRequired,
		"AutoRescan":     true,
		"Bulk":           bulk,
	})
}

// nextToggleExcludeBulk flips a host's exclude-from-bulk protection (kept out of fleet-wide
// reboots). The self-host is always treated as excluded regardless of this flag; this lets the
// operator also protect any other must-not-sweep host. Re-renders the protect control in place.
func (a *app) nextToggleExcludeBulk(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	host, err := db.GetHost(a.db, id)
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	want := !host.ExcludeFromBulk
	if err := db.SetHostExcludeFromBulk(a.db, id, want); err != nil {
		http.Error(w, "failed to update host", http.StatusInternalServerError)
		return
	}
	verb := "protected from fleet reboots"
	if !want {
		verb = "returned to fleet reboots"
	}
	_ = db.RecordActivity(a.db, id, host.Name, "host_updated", fmt.Sprintf("%s %s", host.Name, verb))
	snap, _, _ := db.GetScanSnapshot(a.db, id)
	a.renderNext(w, "bulkprotect", map[string]any{
		"ID": id, "ExcludeFromBulk": want, "IsSelf": a.isSelfHost(snap),
	})
}
