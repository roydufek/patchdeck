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

// nextReboot initiates a reboot and renders a recovery panel that watches the host come
// back on its own (the dot polls independently — each host has its own life in the browser).
func (a *app) nextReboot(w http.ResponseWriter, r *http.Request) {
	host, err := db.GetHost(a.db, chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	if err := a.sshClient.Power(host, a.secrets, "reboot"); err != nil {
		msg := err.Error()
		var hkErr *sshx.HostKeyError
		if errors.As(err, &hkErr) {
			msg = hkErr.Message
		}
		_ = db.RecordActivity(a.db, host.ID, host.Name, "reboot_fail", fmt.Sprintf("Reboot failed: %v", err))
		a.renderNext(w, "actionresult", map[string]any{"ID": host.ID, "Title": "Reboot failed", "Err": msg})
		return
	}
	_ = db.RecordActivity(a.db, host.ID, host.Name, "reboot_ok", fmt.Sprintf("Reboot initiated on %s", host.Name))
	a.renderNext(w, "rebootpanel", map[string]any{"ID": host.ID, "Name": host.Name})
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

// nextRebootWatch is a self-refreshing fragment: while the host is unreachable it re-polls
// itself every 8s; once it answers SSH again it swaps to a static "back online" row (no
// trigger), so polling stops by construction. Each host's watcher runs on its own.
func (a *app) nextRebootWatch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	checker := sshx.NewClient(a.cfg.ConnectivityTimeout, a.cfg.ConnectivityTimeout, a.verifyHostKey)
	st := a.probeConnectivity(checker, id)
	a.renderNext(w, "rebootwatch", map[string]any{"ID": id, "Connected": st.Connected})
}
