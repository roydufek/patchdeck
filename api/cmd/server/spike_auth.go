package main

import (
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"patchdeck/api/internal/auth"
	"patchdeck/api/internal/db"
)

// spike_auth.go — the pre-auth surface for /next so it can stand alone once the React SPA
// is retired: a server-rendered login (with 2FA-at-login + SSO button), first-run bootstrap,
// and logout. Reuses the exact same auth primitives as the JSON /api/login + /api/bootstrap
// (login limiter, password + TOTP + recovery-code checks, session cookie), just rendered as
// HTML instead of JSON.

// loginView is what the login/bootstrap page renders.
type loginView struct {
	Bootstrap    bool // first run — show the create-admin form instead of login
	OIDCEnabled  bool
	OIDCLabel    string
	TOTPRequired bool
	Username     string // preserved across the 2FA step
	Password     string // preserved (hidden) across the 2FA step
	Error        string
	SSOError     string
}

func (a *app) loginData() loginView {
	v := loginView{Bootstrap: !db.HasUsers(a.db)}
	if cfg, err := db.GetOIDCConfig(a.db); err == nil && a.oidcConfigured(cfg) {
		v.OIDCEnabled = true
		v.OIDCLabel = strings.TrimSpace(cfg.ButtonLabel)
		if v.OIDCLabel == "" {
			v.OIDCLabel = "Sign in with SSO"
		}
	}
	return v
}

// ssoErrorMessage maps the short reason codes oidcFail redirects with to friendly text.
func ssoErrorMessage(code string) string {
	switch code {
	case "":
		return ""
	case "sso_forbidden":
		return "That account isn't allowed to sign in here."
	case "sso_disabled":
		return "Single sign-on isn't enabled."
	case "sso_expired", "sso_state", "sso_nonce":
		return "The sign-in attempt expired — please try again."
	case "sso_no_admin":
		return "No local admin exists yet — finish first-run setup first."
	default:
		return "Single sign-on failed — please try again or use your password."
	}
}

func (a *app) nextLogin(w http.ResponseWriter, r *http.Request) {
	// Already signed in? Go to the dashboard.
	if claims := a.sessionClaims(r); claims != nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	v := a.loginData()
	v.SSOError = ssoErrorMessage(strings.TrimSpace(r.URL.Query().Get("sso_error")))
	a.renderNext(w, "login.html", v)
}

func (a *app) nextLoginPost(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	v := a.loginData()
	if v.Bootstrap { // no users yet — the login form shouldn't submit; show bootstrap
		a.renderNext(w, "login.html", v)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	code := strings.TrimSpace(r.FormValue("code"))
	v.Username = username
	v.Password = password

	ip := clientIP(r)
	if ok, retry := a.loginLimiter.Allowed(ip); !ok {
		v.Error = "Too many attempts — try again in about " + retrySeconds(retry) + "."
		a.renderNext(w, "login.html", v)
		return
	}
	user, err := db.GetUserByUsername(a.db, username)
	if err != nil || !auth.CheckPassword(user.PasswordHash, password) {
		a.loginLimiter.RecordFailure(ip)
		v.Error = "Invalid username or password."
		v.Password = ""
		a.renderNext(w, "login.html", v)
		return
	}
	// Password OK — enforce TOTP if enabled.
	if secret := strings.TrimSpace(user.TOTPSecret); secret != "" {
		if code == "" {
			v.TOTPRequired = true
			a.renderNext(w, "login.html", v)
			return
		}
		if !auth.ValidateTOTP(secret, code) && !a.consumeRecoveryCode(user.ID, code) {
			a.loginLimiter.RecordFailure(ip)
			v.TOTPRequired = true
			v.Error = "Invalid two-factor code."
			a.renderNext(w, "login.html", v)
			return
		}
	}
	a.loginLimiter.Reset(ip)
	tok, serr := db.CreateSession(a.db, user.ID, user.Username, user.Role, sessionTTL)
	if serr != nil {
		v.Error = "Could not start a session — please try again."
		a.renderNext(w, "login.html", v)
		return
	}
	setSessionCookie(w, tok)
	w.Header().Set("HX-Redirect", "/")
	w.WriteHeader(http.StatusOK)
}

// consumeRecoveryCode checks a submitted code against the user's unused recovery codes,
// marking it used on a match. Mirrors the JSON login path.
func (a *app) consumeRecoveryCode(userID, code string) bool {
	normalized := strings.ToUpper(strings.ReplaceAll(code, "-", ""))
	codes, err := db.GetUnusedRecoveryCodes(a.db, userID)
	if err != nil {
		return false
	}
	for _, rc := range codes {
		if bcrypt.CompareHashAndPassword([]byte(rc.CodeHash), []byte(normalized)) == nil {
			_ = db.UseRecoveryCode(a.db, rc.ID)
			return true
		}
	}
	return false
}

func (a *app) nextBootstrapPost(w http.ResponseWriter, r *http.Request) {
	v := loginView{Bootstrap: true}
	if db.HasUsers(a.db) { // already bootstrapped
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	_ = r.ParseForm()
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	v.Username = username
	if username == "" || len(password) < 12 {
		v.Error = "Pick a username and a password of at least 12 characters."
		a.renderNext(w, "login.html", v)
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		v.Error = "Could not process that password — try again."
		a.renderNext(w, "login.html", v)
		return
	}
	if err := db.CreateInitialUser(a.db, username, "admin", hash, ""); err != nil {
		v.Error = "Could not create the admin account."
		a.renderNext(w, "login.html", v)
		return
	}
	// Sign the new admin straight in.
	if user, uerr := db.GetUserByUsername(a.db, username); uerr == nil {
		if tok, serr := db.CreateSession(a.db, user.ID, user.Username, user.Role, sessionTTL); serr == nil {
			setSessionCookie(w, tok)
			w.Header().Set("HX-Redirect", "/")
			w.WriteHeader(http.StatusOK)
			return
		}
	}
	w.Header().Set("HX-Redirect", "/login")
	w.WriteHeader(http.StatusOK)
}

func (a *app) nextLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		_ = db.DeleteSession(a.db, c.Value)
	}
	clearSessionCookie(w)
	w.Header().Set("HX-Redirect", "/login")
	w.WriteHeader(http.StatusOK)
}

func retrySeconds(n int) string {
	if n < 1 {
		n = 1
	}
	return strconv.Itoa(n) + "s"
}
