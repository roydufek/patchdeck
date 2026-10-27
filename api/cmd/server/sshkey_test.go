package main

import (
	"database/sql"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"patchdeck/api/internal/appkey"
	"patchdeck/api/internal/crypto"
	"patchdeck/api/internal/db"
	"patchdeck/api/internal/models"
	"patchdeck/api/internal/sshx"
)

func keyTestApp(t *testing.T) *app {
	t.Helper()
	conn, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	seal, _ := crypto.NewSealBox(strings.Repeat("k", 32))
	keys, generated, err := appkey.Open(conn, seal)
	if err != nil || !generated {
		t.Fatalf("appkey: %v", err)
	}
	a := &app{db: conn, secrets: seal, appKeys: keys}
	a.sshClient = sshx.NewClient(0, 0, nil)
	a.sshClient.SetAppKeySource(keys.Signers)
	return a
}

func TestMergeHostSecrets(t *testing.T) {
	stored := map[string]string{"password": "pw", "private_key_pem": "OLDKEY", "passphrase": "oldpass", "sudo_password": "rootpw"}
	cases := []struct {
		name        string
		req         hostUpsertRequest
		wantPw      string
		wantKey     string
		wantPass    string
		wantSudo    string
		wantRemoved string
	}{
		{"blank keeps everything (key auth drops the login password)", hostUpsertRequest{AuthType: "key"}, "", "OLDKEY", "oldpass", "rootpw", "login password"},
		{"clear sudo on save", hostUpsertRequest{AuthType: "key", ClearSudoPassword: true}, "", "OLDKEY", "oldpass", "", "login password, sudo/root password"},
		{"switch to Patchdeck key drops key + password", hostUpsertRequest{AuthType: models.AuthAppKey}, "", "", "", "rootpw", "login password, private key"},
		{"new key does not inherit the old passphrase", hostUpsertRequest{AuthType: "key", PrivateKeyPEM: "NEWKEY"}, "", "NEWKEY", "", "rootpw", "login password"},
		{"clear password while using password auth", hostUpsertRequest{AuthType: "password", ClearPassword: true}, "", "", "", "rootpw", "login password, private key"},
	}
	for _, tc := range cases {
		req := tc.req
		removed := strings.Join(mergeHostSecrets(&req, stored), ", ")
		if req.Password != tc.wantPw || req.PrivateKeyPEM != tc.wantKey || req.Passphrase != tc.wantPass || req.SudoPassword != tc.wantSudo || removed != tc.wantRemoved {
			t.Errorf("%s: got pw=%q key=%q pass=%q sudo=%q removed=%q", tc.name, req.Password, req.PrivateKeyPEM, req.Passphrase, req.SudoPassword, removed)
		}
	}
	// Clearing the only login password of a password host must fail validation, not save an empty one.
	req := hostUpsertRequest{Name: "h", Address: "a", SSHUser: "u", Port: 22, AuthType: "password", ClearPassword: true, HostKeyTrustMode: "tofu"}
	mergeHostSecrets(&req, stored)
	if msg := validateHostRequest(req); !strings.Contains(msg, "password required") {
		t.Errorf("want a validation error, got %q", msg)
	}
}

func TestValidateAppKeyAuth(t *testing.T) {
	req := hostUpsertRequest{Name: "web-01", Address: "warpgate.example.com", Port: 2222, SSHUser: "patchdeck:web-01", AuthType: models.AuthAppKey, HostKeyTrustMode: "tofu"}
	if msg := validateHostRequest(req); msg != "" {
		t.Fatalf("app_key needs no secret, and user:target is valid: %q", msg)
	}
	req.AuthType = "telepathy"
	if msg := validateHostRequest(req); !strings.Contains(msg, "app_key") {
		t.Fatalf("got %q", msg)
	}
}

func TestSSHKeyCardAndRotationAPI(t *testing.T) {
	a := keyTestApp(t)
	cur := a.appKeys.Current()

	render := func() string {
		var b strings.Builder
		if err := nextTmpl.ExecuteTemplate(&b, "sshkeycard", a.sshKeyData("", "", []keyCheck{{Name: "web-01", OK: true}, {Name: "db-01", Error: "SSH authentication failed — check credentials"}}, true)); err != nil {
			t.Fatal(err)
		}
		return html.UnescapeString(b.String())
	}
	if h := render(); !strings.Contains(h, cur.AuthorizedKey) || !strings.Contains(h, cur.Fingerprint) || !strings.Contains(h, "Rotate key") {
		t.Fatalf("card before rotation lacks the key or the rotate button")
	}

	call := func(method, path, body string, h http.HandlerFunc) (int, map[string]any) {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	code, out := call("GET", "/api/settings/ssh-key", "", a.getSSHKey)
	if code != 200 || out["current"].(map[string]any)["public_key"] != cur.AuthorizedKey || out["rotation"] != nil {
		t.Fatalf("get: %d %v", code, out)
	}
	if code, _ = call("POST", "/api/settings/ssh-key/finish", "", a.apiKeyFinish); code != 409 {
		t.Fatalf("finish without a rotation: %d", code)
	}
	code, out = call("POST", "/api/settings/ssh-key/rotate", "", a.apiKeyRotate)
	next := out["rotation"].(map[string]any)["fingerprint"]
	if code != 200 || next == cur.Fingerprint {
		t.Fatalf("rotate: %d %v", code, out)
	}
	if h := render(); !strings.Contains(h, "Finish anyway") || !strings.Contains(h, "db-01") || !strings.Contains(h, "Check hosts") {
		t.Fatalf("mid-rotation card lacks the check results / force button")
	}
	if code, out = call("POST", "/api/settings/ssh-key/check", "", a.apiKeyCheck); code != 200 || out["ready"] != true {
		t.Fatalf("check with no app_key hosts: %d %v", code, out)
	}
	code, out = call("POST", "/api/settings/ssh-key/finish", "", a.apiKeyFinish)
	if code != 200 || out["current"].(map[string]any)["fingerprint"] != next {
		t.Fatalf("finish: %d %v", code, out)
	}
	_, out = call("POST", "/api/settings/ssh-key/rotate", "", a.apiKeyRotate)
	if code, _ = call("POST", "/api/settings/ssh-key/cancel", "", a.apiKeyCancel); code != 200 {
		t.Fatalf("cancel: %d", code)
	}
	if a.appKeys.Current().Fingerprint != next {
		t.Fatal("cancel must keep the current key")
	}
	// Every step is in the activity log.
	acts, _ := db.ListActivity(a.db, 50, 0, "")
	var events []string
	for _, e := range acts {
		events = append(events, e.EventType)
	}
	all := strings.Join(events, ",")
	for _, want := range []string{"ssh_key_rotation_started", "ssh_key_rotated", "ssh_key_rotation_cancelled"} {
		if !strings.Contains(all, want) {
			t.Errorf("activity log lacks %s: %s", want, all)
		}
	}
}

func TestHostFormRendersKeyAndClearBoxes(t *testing.T) {
	a := keyTestApp(t)
	rec := httptest.NewRecorder()
	a.renderHostForm(rec, hostFormView{Mode: "edit", ID: "x", Name: "web-01", Port: 2222, SSHUser: "patchdeck:web-01", AuthType: models.AuthAppKey,
		HostKeyTrustMode: "tofu", HasPassword: true, HasSudo: true})
	h := html.UnescapeString(rec.Body.String())
	for _, want := range []string{
		a.appKeys.Current().AuthorizedKey, `value="app_key" selected`, `name="clear_password"`, `name="clear_sudo_password"`,
		`id="sshpaste"`, `id="wg-hint"`, "function pdParseSSH",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("host form lacks %s", want)
		}
	}
	if strings.Contains(h, `name="clear_private_key"`) {
		t.Error("no stored key → no clear-key box")
	}
}
