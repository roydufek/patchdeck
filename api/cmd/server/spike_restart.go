package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"

	"patchdeck/api/internal/db"
	"patchdeck/api/internal/restartpolicy"
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
