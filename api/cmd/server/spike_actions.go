package main

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"patchdeck/api/internal/db"
	"patchdeck/api/internal/sshx"
)

// spike_actions.go — /next power posture actions (reboot / shutdown) rendered as HTML
// fragments. The service-restart action lives in spike_restart.go (the smart, self-learning
// restart); these handle the machine-level power controls and their recovery watch.

// nextRebootConfirm renders the confirm step before rebooting a host.
func (a *app) nextRebootConfirm(w http.ResponseWriter, r *http.Request) {
	v, _, err := a.nextHostView(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	a.renderNext(w, "rebootconfirm", map[string]any{"ID": v.ID, "Name": v.Name, "Out": r.URL.Query().Get("out"), "Self": v.IsSelf})
}

// nextShutdownConfirm renders the confirm step before powering a host off.
func (a *app) nextShutdownConfirm(w http.ResponseWriter, r *http.Request) {
	v, _, err := a.nextHostView(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	a.renderNext(w, "shutdownconfirm", map[string]any{"ID": v.ID, "Name": v.Name, "Out": r.URL.Query().Get("out"), "Self": v.IsSelf})
}

// nextShutdown powers the host off. Unlike reboot there's no recovery watch — it won't
// come back until it's powered on again.
func (a *app) nextShutdown(w http.ResponseWriter, r *http.Request) {
	host, err := db.GetHost(a.db, chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	if !a.tasks.TryLock(host.ID, "Shutting down") {
		a.renderNext(w, "actionresult", map[string]any{"ID": host.ID, "Title": "Host is busy", "Err": "Another action is running on this host. Shut it down once that finishes."})
		return
	}
	defer a.tasks.Unlock(host.ID)
	if err := a.sshClient.Power(host, a.secrets, "shutdown"); err != nil {
		msg := err.Error()
		var hkErr *sshx.HostKeyError
		if errors.As(err, &hkErr) {
			msg = hkErr.Message
		}
		_ = db.RecordActivity(a.db, host.ID, host.Name, "shutdown_fail", fmt.Sprintf("Shutdown failed: %v", err))
		a.renderNext(w, "actionresult", map[string]any{"ID": host.ID, "Title": "Shutdown failed", "Err": msg})
		return
	}
	_ = db.RecordActivity(a.db, host.ID, host.Name, "shutdown_ok", fmt.Sprintf("Shutdown initiated on %s", host.Name))
	a.renderNext(w, "actionresult", map[string]any{"ID": host.ID, "Title": "Shutdown initiated", "OK": host.Name + " is powering off. It won't return until it's powered back on."})
}
