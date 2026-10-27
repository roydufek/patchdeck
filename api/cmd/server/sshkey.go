package main

import (
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	"patchdeck/api/internal/appkey"
	"patchdeck/api/internal/db"
	"patchdeck/api/internal/models"
)

// sshkey.go — Patchdeck's own SSH keypair (internal/appkey): the Settings card, its JSON API,
// and the rotation flow. Rotation is two-phase so no host is locked out: Start offers both
// keys on every connection; Check logs into each "Patchdeck key" host with ONLY the new key;
// Finish promotes it (refusing, unless forced, while any host still rejects it).

// keyCheck is one host's answer to "is the new key installed here yet?".
type keyCheck struct {
	Name  string `json:"host"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// appKeyHosts lists the hosts that log in with Patchdeck's key.
func (a *app) appKeyHosts() []models.Host {
	all, _ := db.ListHosts(a.db)
	var out []models.Host
	for _, h := range all {
		if h.AuthType == models.AuthAppKey {
			out = append(out, h)
		}
	}
	return out
}

// checkNextKey tries the rotation's new key, alone, against every "Patchdeck key" host.
func (a *app) checkNextKey() ([]keyCheck, error) {
	next, ok := a.appKeys.Next()
	if !ok {
		return nil, appkey.ErrNotRotating
	}
	hosts := a.appKeyHosts()
	checker := a.sshClient.WithTimeouts(a.cfg.ConnectivityTimeout, a.cfg.ConnectivityTimeout)
	out := make([]keyCheck, len(hosts))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i, h := range hosts {
		wg.Add(1)
		go func(i int, h models.Host) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = keyCheck{Name: h.Name, OK: true}
			if err := checker.CheckAppKey(h, next.Signer); err != nil {
				out[i] = keyCheck{Name: h.Name, Error: err.Error()}
			}
		}(i, h)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func allOK(checks []keyCheck) bool {
	for _, c := range checks {
		if !c.OK {
			return false
		}
	}
	return true
}

func (a *app) keyActivity(event, summary string) {
	_ = db.RecordActivity(a.db, "", "Patchdeck", event, summary)
}

// startRotation / finishRotation / cancelRotation are shared by the UI and the API.
func (a *app) startRotation() (appkey.Key, error) {
	k, err := a.appKeys.StartRotation()
	if err == nil {
		a.keyActivity("ssh_key_rotation_started", "SSH key rotation started — new key "+k.Fingerprint+" ("+k.Comment()+")")
	}
	return k, err
}

func (a *app) finishRotation(force bool) ([]keyCheck, error) {
	var checks []keyCheck
	if !force {
		var err error
		if checks, err = a.checkNextKey(); err != nil {
			return nil, err
		}
		if !allOK(checks) {
			return checks, errNotReady
		}
	}
	retired, err := a.appKeys.FinishRotation()
	if err == nil {
		note := ""
		if force {
			note = " (forced)"
		}
		cur := a.appKeys.Current()
		a.keyActivity("ssh_key_rotated", "SSH key rotated"+note+" — now "+cur.Fingerprint+"; retired "+retired.Fingerprint+" ("+retired.Comment()+")")
	}
	return checks, err
}

var errNotReady = errors.New("some hosts don't accept the new key yet")

func (a *app) cancelRotation() error {
	dropped, err := a.appKeys.CancelRotation()
	if err == nil {
		a.keyActivity("ssh_key_rotation_cancelled", "SSH key rotation cancelled — discarded "+dropped.Fingerprint)
	}
	return err
}

// --- Settings card -------------------------------------------------------------------------

func (a *app) sshKeyData(flash, errMsg string, checks []keyCheck, needForce bool) map[string]any {
	next, rotating := a.appKeys.Next()
	var names []string
	for _, h := range a.appKeyHosts() {
		names = append(names, h.Name)
	}
	return map[string]any{
		"Cur": a.appKeys.Current(), "Next": next, "Rotating": rotating, "Hosts": names,
		"Checks": checks, "NeedForce": needForce, "Flash": flash, "Err": errMsg,
	}
}

func (a *app) renderKeyCard(w http.ResponseWriter, flash, errMsg string, checks []keyCheck, needForce bool) {
	a.renderNext(w, "sshkeycard", a.sshKeyData(flash, errMsg, checks, needForce))
}

func (a *app) nextKeyRotate(w http.ResponseWriter, r *http.Request) {
	if _, err := a.startRotation(); err != nil {
		a.renderKeyCard(w, "", err.Error(), nil, false)
		return
	}
	a.renderKeyCard(w, "New key created. Install its public key, then Check hosts and Finish.", "", nil, false)
}

func (a *app) nextKeyCheck(w http.ResponseWriter, r *http.Request) {
	checks, err := a.checkNextKey()
	if err != nil {
		a.renderKeyCard(w, "", err.Error(), nil, false)
		return
	}
	if len(checks) == 0 {
		a.renderKeyCard(w, "No hosts use Patchdeck's key yet — nothing to check.", "", nil, false)
		return
	}
	a.renderKeyCard(w, "", "", checks, false)
}

func (a *app) nextKeyFinish(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	checks, err := a.finishRotation(r.FormValue("force") != "")
	switch {
	case errors.Is(err, errNotReady):
		a.renderKeyCard(w, "", "Not finished: these hosts still reject the new key. Install it there, or finish anyway (they'll lose access until you do).", checks, true)
	case err != nil:
		a.renderKeyCard(w, "", err.Error(), nil, false)
	default:
		a.renderKeyCard(w, "Rotation finished. The old key is destroyed — remove its line from authorized_keys / your Warpgate user.", "", nil, false)
	}
}

func (a *app) nextKeyCancel(w http.ResponseWriter, r *http.Request) {
	if err := a.cancelRotation(); err != nil {
		a.renderKeyCard(w, "", err.Error(), nil, false)
		return
	}
	a.renderKeyCard(w, "Rotation cancelled. The current key is unchanged.", "", nil, false)
}

// --- JSON API ------------------------------------------------------------------------------

type keyJSON struct {
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   string `json:"created_at"`
}

func keyView(k appkey.Key) keyJSON {
	return keyJSON{PublicKey: k.AuthorizedKey, Fingerprint: k.Fingerprint, CreatedAt: k.Created.UTC().Format("2006-01-02T15:04:05Z")}
}

func (a *app) getSSHKey(w http.ResponseWriter, _ *http.Request) {
	out := map[string]any{"current": keyView(a.appKeys.Current()), "rotation": nil}
	if next, ok := a.appKeys.Next(); ok {
		out["rotation"] = keyView(next)
	}
	var names []string
	for _, h := range a.appKeyHosts() {
		names = append(names, h.Name)
	}
	out["hosts"] = names
	writeJSON(w, http.StatusOK, out)
}

func (a *app) apiKeyRotate(w http.ResponseWriter, _ *http.Request) {
	k, err := a.startRotation()
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rotation": keyView(k)})
}

func (a *app) apiKeyCheck(w http.ResponseWriter, _ *http.Request) {
	checks, err := a.checkNextKey()
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ready": allOK(checks), "hosts": checks})
}

// apiKeyFinish takes an optional {"force": true}; an empty body means no force.
func (a *app) apiKeyFinish(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Force bool `json:"force"`
	}
	if body, _ := io.ReadAll(io.LimitReader(r.Body, 4096)); strings.TrimSpace(string(body)) != "" {
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	checks, err := a.finishRotation(req.Force)
	switch {
	case errors.Is(err, errNotReady):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "hosts": checks})
	case err != nil:
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"current": keyView(a.appKeys.Current())})
	}
}

func (a *app) apiKeyCancel(w http.ResponseWriter, _ *http.Request) {
	if err := a.cancelRotation(); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"current": keyView(a.appKeys.Current())})
}
