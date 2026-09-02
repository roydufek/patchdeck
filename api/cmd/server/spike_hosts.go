package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"patchdeck/api/internal/db"
	"patchdeck/api/internal/models"
	"patchdeck/api/internal/sshx"
)

// spike_hosts.go — /next host management (add / edit / delete) plus host-key
// accept/deny for TOFU/pinned mismatches. Native HTML forms (not JSON) parsed into the
// same hostUpsertRequest the JSON API uses, so all validation/encryption is shared.

// hostFormView carries the values a host add/edit form renders. Secrets are never
// prefilled (write-only): a blank secret on edit keeps the stored one.
type hostFormView struct {
	Mode                                               string // "new" | "edit"
	ID, Name, Address                                  string
	Port                                               int
	SSHUser, AuthType, HostKeyTrustMode, HostKeyPinned string
	AutoUpdatePolicy                                   string
	Tags                                               string // comma-separated, for the input
	ChecksEnabled                                      bool
	Err                                                string
}

// splitTags parses a comma-separated tag string into a trimmed, de-duplicated list.
func splitTags(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t != "" && !seen[strings.ToLower(t)] {
			seen[strings.ToLower(t)] = true
			out = append(out, t)
		}
	}
	return out
}

func (a *app) renderHostForm(w http.ResponseWriter, v hostFormView) {
	a.renderNext(w, "hostform.html", v)
}

// formHostReq builds a hostUpsertRequest from a posted HTML form.
func formHostReq(r *http.Request) hostUpsertRequest {
	port, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("port")))
	return hostUpsertRequest{
		Name:             r.FormValue("name"),
		Address:          r.FormValue("address"),
		Port:             port,
		SSHUser:          r.FormValue("ssh_user"),
		AuthType:         r.FormValue("auth_type"),
		Password:         r.FormValue("password"),
		PrivateKeyPEM:    r.FormValue("private_key_pem"),
		Passphrase:       r.FormValue("passphrase"),
		SudoPassword:     r.FormValue("sudo_password"),
		HostKeyTrustMode: r.FormValue("host_key_trust_mode"),
		HostKeyPinned:    r.FormValue("host_key_pinned_fingerprint"),
		Tags:             splitTags(r.FormValue("tags")),
	}
}

// formViewFromReq rebuilds the form view from a submitted request so a validation error
// re-renders with the operator's typed values intact (secrets excluded).
func formViewFromReq(mode, id string, req hostUpsertRequest, checks bool, policy, errMsg string) hostFormView {
	return hostFormView{
		Mode: mode, ID: id,
		Name: req.Name, Address: req.Address, Port: req.Port, SSHUser: req.SSHUser,
		AuthType: req.AuthType, HostKeyTrustMode: req.HostKeyTrustMode, HostKeyPinned: req.HostKeyPinned,
		Tags: strings.Join(req.Tags, ", "), ChecksEnabled: checks, AutoUpdatePolicy: policy, Err: errMsg,
	}
}

func formChecks(r *http.Request) bool  { return r.FormValue("checks_enabled") != "" }
func formPolicy(r *http.Request) string {
	p := strings.TrimSpace(r.FormValue("auto_update_policy"))
	if p == "" {
		return "manual"
	}
	return p
}

// nextHostNew renders a blank add-host form.
func (a *app) nextHostNew(w http.ResponseWriter, r *http.Request) {
	a.renderHostForm(w, hostFormView{
		Mode: "new", Port: 22, AuthType: "key", HostKeyTrustMode: "tofu",
		ChecksEnabled: true, AutoUpdatePolicy: "manual",
	})
}

// nextHostCreate validates + persists a new host, then HX-redirects to the dashboard.
func (a *app) nextHostCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	req := formHostReq(r)
	checks, policy := formChecks(r), formPolicy(r)
	normalizeHostRequest(&req)
	if msg := validateHostRequest(req); msg != "" {
		a.renderHostForm(w, formViewFromReq("new", "", req, checks, policy, msg))
		return
	}
	if req.AuthType == "password" {
		req.PrivateKeyPEM, req.Passphrase = "", ""
	}
	if req.AuthType == "key" {
		req.Password = ""
	}
	enc, err := encryptHostSecrets(a.secrets, req)
	if err != nil {
		a.renderHostForm(w, formViewFromReq("new", "", req, checks, policy, "Failed to encrypt host credentials."))
		return
	}
	h := models.Host{Name: req.Name, Address: req.Address, Port: req.Port, SSHUser: req.SSHUser, AuthType: req.AuthType, SecretCipher: enc, ChecksEnabled: true, AutoUpdatePolicy: "manual", HostKeyRequired: true, HostKeyTrustMode: req.HostKeyTrustMode, HostKeyPinned: req.HostKeyPinned}
	hostID, err := db.CreateHost(a.db, h)
	if err != nil {
		msg := "Failed to save host."
		if errors.Is(err, db.ErrHostExists) {
			msg = "A host with the same address, port and SSH user already exists."
		}
		a.renderHostForm(w, formViewFromReq("new", "", req, checks, policy, msg))
		return
	}
	if policy != "scheduled_apply" {
		policy = "manual"
	}
	_ = db.UpdateHostOperationalControls(a.db, hostID, checks, policy)
	_ = db.UpdateHostTags(a.db, hostID, req.Tags)
	// New hosts require an explicit first-connection approval: store them "pinned" with no
	// fingerprint so the first connection captures the key as pending (blocked) for review,
	// rather than silently trusting it. Approving sets the pinned fingerprint.
	_ = db.UpdateHostKeyPolicy(a.db, hostID, true, "pinned", "")
	_ = db.RecordActivity(a.db, hostID, req.Name, "host_added", fmt.Sprintf("Host %s added (%s@%s:%d)", req.Name, req.SSHUser, req.Address, req.Port))
	w.Header().Set("HX-Redirect", "/next")
	w.WriteHeader(http.StatusOK)
}

// nextHostEdit renders an edit form prefilled from the stored host (secrets excluded).
func (a *app) nextHostEdit(w http.ResponseWriter, r *http.Request) {
	host, err := db.GetHost(a.db, chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	policy := strings.ToLower(strings.TrimSpace(host.AutoUpdatePolicy))
	if policy == "" {
		policy = "manual"
	}
	mode := strings.TrimSpace(host.HostKeyTrustMode)
	if mode == "" {
		mode = "tofu"
	}
	a.renderHostForm(w, hostFormView{
		Mode: "edit", ID: host.ID, Name: host.Name, Address: host.Address, Port: host.Port,
		SSHUser: host.SSHUser, AuthType: host.AuthType, HostKeyTrustMode: mode, HostKeyPinned: host.HostKeyPinned,
		Tags: strings.Join(host.Tags, ", "), ChecksEnabled: host.ChecksEnabled, AutoUpdatePolicy: policy,
	})
}

// nextHostUpdate mirrors the JSON updateHost: blank secrets keep the stored ones, then
// persists core fields + host-key policy + operational controls, and HX-redirects to detail.
func (a *app) nextHostUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := db.GetHost(a.db, id)
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	req := formHostReq(r)
	checks, policy := formChecks(r), formPolicy(r)
	req.Name = strings.TrimSpace(req.Name)
	req.Address = strings.TrimSpace(req.Address)
	req.SSHUser = strings.TrimSpace(req.SSHUser)
	req.AuthType = strings.TrimSpace(req.AuthType)
	req.Password = strings.TrimSpace(req.Password)
	req.PrivateKeyPEM = strings.TrimSpace(req.PrivateKeyPEM)
	req.Passphrase = strings.TrimSpace(req.Passphrase)
	req.SudoPassword = strings.TrimSpace(req.SudoPassword)
	req.HostKeyTrustMode = strings.ToLower(strings.TrimSpace(req.HostKeyTrustMode))
	req.HostKeyPinned = strings.TrimSpace(req.HostKeyPinned)
	if req.Name == "" {
		req.Name = existing.Name
	}
	if req.Address == "" {
		req.Address = existing.Address
	}
	if req.Port == 0 {
		req.Port = existing.Port
	}
	if req.SSHUser == "" {
		req.SSHUser = existing.SSHUser
	}
	if req.AuthType == "" {
		req.AuthType = existing.AuthType
	}
	stored, err := decodeHostSecrets(a.secrets, existing.SecretCipher)
	if err != nil {
		a.renderHostForm(w, formViewFromReq("edit", id, req, checks, policy, "Failed to read existing host credentials."))
		return
	}
	if req.Password == "" {
		req.Password = stored["password"]
	}
	if req.PrivateKeyPEM == "" {
		req.PrivateKeyPEM = stored["private_key_pem"]
	}
	if req.Passphrase == "" {
		req.Passphrase = stored["passphrase"]
	}
	if req.SudoPassword == "" {
		req.SudoPassword = stored["sudo_password"]
	}
	// Host-key policy isn't edited here — it's owned by the approval workflow. Use dummy
	// valid values so shared validation passes; the stored policy is left untouched.
	req.HostKeyTrustMode = "tofu"
	req.HostKeyPinned = ""
	if msg := validateHostRequest(req); msg != "" {
		a.renderHostForm(w, formViewFromReq("edit", id, req, checks, policy, msg))
		return
	}
	if req.AuthType == "password" {
		req.PrivateKeyPEM, req.Passphrase = "", ""
	}
	if req.AuthType == "key" {
		req.Password = ""
	}
	enc, err := encryptHostSecrets(a.secrets, req)
	if err != nil {
		a.renderHostForm(w, formViewFromReq("edit", id, req, checks, policy, "Failed to encrypt host credentials."))
		return
	}
	updated := models.Host{ID: id, Name: req.Name, Address: req.Address, Port: req.Port, SSHUser: req.SSHUser, AuthType: req.AuthType, SecretCipher: enc}
	if err := db.UpdateHost(a.db, updated); err != nil {
		msg := "Failed to update host."
		if errors.Is(err, db.ErrHostExists) {
			msg = "A host with the same address, port and SSH user already exists."
		}
		a.renderHostForm(w, formViewFromReq("edit", id, req, checks, policy, msg))
		return
	}
	if policy != "scheduled_apply" {
		policy = "manual"
	}
	_ = db.UpdateHostOperationalControls(a.db, id, checks, policy)
	_ = db.UpdateHostTags(a.db, id, req.Tags)
	_ = db.RecordActivity(a.db, id, req.Name, "host_updated", fmt.Sprintf("Host %s updated", req.Name))
	w.Header().Set("HX-Redirect", "/next/hosts/"+id)
	w.WriteHeader(http.StatusOK)
}

// nextHostDeleteConfirm renders the delete confirm step.
func (a *app) nextHostDeleteConfirm(w http.ResponseWriter, r *http.Request) {
	host, err := db.GetHost(a.db, chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	a.renderNext(w, "deleteconfirm", map[string]any{"ID": host.ID, "Name": host.Name})
}

// nextHostDelete removes a host and HX-redirects to the dashboard.
func (a *app) nextHostDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	host, _ := db.GetHost(a.db, id)
	if err := db.DeleteHost(a.db, id); err != nil {
		http.Error(w, "failed to delete host", http.StatusInternalServerError)
		return
	}
	_ = db.RecordActivity(a.db, id, host.Name, "host_deleted", fmt.Sprintf("Host %s deleted", host.Name))
	w.Header().Set("HX-Redirect", "/next")
	w.WriteHeader(http.StatusOK)
}

// nextHostNotifPrefs saves the per-host notification overrides and re-renders the section.
func (a *app) nextHostNotifPrefs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := db.GetHost(a.db, id); err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	_ = r.ParseForm()
	prefs := models.HostNotificationPrefs{
		UpdatesAvailable: r.FormValue("updates_available") != "",
		AutoApplySuccess: r.FormValue("auto_apply_success") != "",
		AutoApplyFailure: r.FormValue("auto_apply_failure") != "",
		ScanFailure:      r.FormValue("scan_failure") != "",
	}
	flash := "Saved."
	if err := db.UpsertHostNotificationPrefs(a.db, id, prefs); err != nil {
		flash = "Failed to save."
	}
	a.renderNext(w, "hostnotif", map[string]any{"ID": id, "P": prefs, "Flash": flash})
}

// nextHostVerify makes a connection to capture the host's SSH key for review. For an
// approval-required (pinned, no-fingerprint) host this records the presented key as pending
// (the connection is intentionally blocked by the host-key check); the operator then approves
// it, which pins that fingerprint. Reused for re-checking after a key rotation.
func (a *app) nextHostVerify(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	checker := sshx.NewClient(a.cfg.ConnectivityTimeout, a.cfg.ConnectivityTimeout, a.verifyHostKey)
	_ = a.probeConnectivity(checker, id) // side effect: captures pending key (or verifies)
	w.Header().Set("HX-Redirect", "/next/hosts/"+id)
	w.WriteHeader(http.StatusOK)
}

// nextHostKeyAccept trusts the pending (rotated) host-key fingerprint, then refreshes detail.
func (a *app) nextHostKeyAccept(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	host, err := db.GetHost(a.db, id)
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	if pending := strings.TrimSpace(host.HostKeyPending); pending != "" {
		if err := db.AcceptHostKeyPendingFingerprint(a.db, id); err == nil {
			_ = db.RecordHostKeyAudit(a.db, id, "host_key_rotation_accepted", host.HostKeyTrusted, pending, "operator accepted new host key fingerprint (/next)")
		}
	}
	w.Header().Set("HX-Redirect", "/next/hosts/"+id)
	w.WriteHeader(http.StatusOK)
}

// nextHostKeyDeny clears the pending fingerprint (keeps the trusted one), then refreshes detail.
func (a *app) nextHostKeyDeny(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	host, err := db.GetHost(a.db, id)
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	if pending := strings.TrimSpace(host.HostKeyPending); pending != "" {
		if err := db.ClearHostKeyPendingFingerprint(a.db, id); err == nil {
			_ = db.RecordHostKeyAudit(a.db, id, "host_key_mismatch_denied", host.HostKeyTrusted, pending, "operator denied new host key fingerprint (/next)")
		}
	}
	w.Header().Set("HX-Redirect", "/next/hosts/"+id)
	w.WriteHeader(http.StatusOK)
}
