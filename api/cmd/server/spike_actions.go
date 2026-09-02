package main

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"patchdeck/api/internal/db"
	"patchdeck/api/internal/sshx"
)

// spike_actions.go — /next power posture actions (reboot + needrestart-coordinated
// service restart) rendered as HTML fragments. These reuse the exact backend calls the
// JSON API uses (RestartAllViaNeedrestart / Power) and honor the same dbus guardrail:
// needrestart owns the coordinated restart and leaves unsafe units (dbus/kernel) flagged
// as reboot-required rather than naively bouncing them.

// nextRestartConfirm renders the "are you sure?" step before a coordinated service restart.
func (a *app) nextRestartConfirm(w http.ResponseWriter, r *http.Request) {
	v, snap, err := a.nextHostView(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	a.renderNext(w, "restartconfirm", map[string]any{
		"ID": v.ID, "Name": v.Name, "Count": len(snap.NeedsRestart), "Out": r.URL.Query().Get("out"),
	})
}

// nextRestartAll runs needrestart's coordinated restart pass and renders the outcome.
func (a *app) nextRestartAll(w http.ResponseWriter, r *http.Request) {
	host, err := db.GetHost(a.db, chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	res, err := a.sshClient.RestartAllViaNeedrestart(host, a.secrets)
	if err != nil {
		msg := err.Error()
		var hkErr *sshx.HostKeyError
		if errors.As(err, &hkErr) {
			msg = hkErr.Message
		}
		_ = db.RecordActivity(a.db, host.ID, host.Name, "restart_fail", fmt.Sprintf("needrestart restart-all failed: %v", err))
		a.renderNext(w, "actionresult", map[string]any{
			"ID": host.ID, "Title": "Restart failed", "Err": msg,
		})
		return
	}
	_ = db.RecordActivity(a.db, host.ID, host.Name, "restart_ok", "Ran needrestart coordinated restart (all safe services)")
	a.renderNext(w, "actionresult", map[string]any{
		"ID": host.ID, "Title": "Services restarted",
		"OK":             "needrestart restarted every service it safely could.",
		"RebootRequired": res.RebootRequired,
		"Rescan":         true,
	})
}

// hostRestartServices returns the services the latest scan flagged as needing a restart.
func (a *app) hostRestartServices(hostID string) []string {
	snaps, _ := db.ListScanSnapshots(a.db)
	for _, s := range snaps {
		if s.HostID == hostID {
			return s.NeedsRestart
		}
	}
	return nil
}

// nextRestartDeferredConfirm renders the confirm step before a detached deferred-unit restart.
func (a *app) nextRestartDeferredConfirm(w http.ResponseWriter, r *http.Request) {
	v, snap, err := a.nextHostView(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	a.renderNext(w, "deferredconfirm", map[string]any{
		"ID": v.ID, "Name": v.Name, "Count": len(snap.NeedsRestart), "Out": r.URL.Query().Get("out"),
	})
}

// nextRestartDeferred restarts the flagged units detached (an opt-in alternative to a reboot).
// dbus uses needrestart's coordinated handler or stays reboot-required; the rest are restarted
// detached so a connection flap can't interrupt them.
func (a *app) nextRestartDeferred(w http.ResponseWriter, r *http.Request) {
	host, err := db.GetHost(a.db, chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	services := a.hostRestartServices(host.ID)
	if len(services) == 0 {
		a.renderNext(w, "actionresult", map[string]any{"ID": host.ID, "Title": "Nothing to restart", "OK": "No services are currently flagged for restart.", "Rescan": true})
		return
	}
	res, err := a.sshClient.RestartDeferredDetached(host, a.secrets, services)
	if err != nil {
		msg := err.Error()
		var hkErr *sshx.HostKeyError
		if errors.As(err, &hkErr) {
			msg = hkErr.Message
		}
		_ = db.RecordActivity(a.db, host.ID, host.Name, "restart_fail", fmt.Sprintf("Detached restart failed: %v", err))
		a.renderNext(w, "actionresult", map[string]any{"ID": host.ID, "Title": "Restart failed", "Err": msg})
		return
	}
	_ = db.RecordActivity(a.db, host.ID, host.Name, "restart_ok", fmt.Sprintf("Restarted %d deferred service(s) detached", len(services)))
	a.renderNext(w, "actionresult", map[string]any{
		"ID": host.ID, "Title": "Deferred services restarted",
		"OK": fmt.Sprintf("Issued a detached restart of %d service(s).", len(services)),
		"RebootRequired": res.RebootRequired, "Rescan": true,
	})
}

// nextRebootConfirm renders the confirm step before rebooting a host.
func (a *app) nextRebootConfirm(w http.ResponseWriter, r *http.Request) {
	v, _, err := a.nextHostView(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	a.renderNext(w, "rebootconfirm", map[string]any{"ID": v.ID, "Name": v.Name, "Out": r.URL.Query().Get("out")})
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
	a.renderNext(w, "shutdownconfirm", map[string]any{"ID": v.ID, "Name": v.Name, "Out": r.URL.Query().Get("out")})
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
