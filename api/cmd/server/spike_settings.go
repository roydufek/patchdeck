package main

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/skip2/go-qrcode"
	"golang.org/x/crypto/bcrypt"

	"patchdeck/api/internal/auth"
	"patchdeck/api/internal/db"
	"patchdeck/api/internal/models"
	"patchdeck/api/internal/oidc"
)

// spike_settings.go — /next settings hub. Each concern (notifications, SSO, API tokens,
// audit retention, two-factor) is an independently-savable card: its form posts to its own
// endpoint, which re-renders just that card with an inline Saved/error flash. Reuses the
// exact DB + validation the JSON API uses.

func (a *app) nextUser(r *http.Request) (models.User, bool) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		return models.User{}, false
	}
	user, err := db.GetUserByUsername(a.db, claims.Username)
	if err != nil {
		return models.User{}, false
	}
	return user, true
}

// --- data loaders (one card each) -------------------------------------------------

func (a *app) notifData(flash, errMsg string) map[string]any {
	s, err := db.GetNotificationSettings(a.db)
	if err == nil && strings.TrimSpace(s.AppriseURL) == "" {
		s.AppriseURL = a.cfg.AppriseURL
	}
	return map[string]any{"S": s, "Flash": flash, "Err": errMsg}
}

func (a *app) oidcData(r *http.Request, flash, errMsg string) map[string]any {
	cfg, _ := db.GetOIDCConfig(a.db)
	return map[string]any{
		"Enabled": cfg.Enabled, "Issuer": cfg.Issuer, "ClientID": cfg.ClientID,
		"BaseURL": cfg.BaseURL, "Allowed": cfg.Allowed, "ButtonLabel": cfg.ButtonLabel,
		"HasSecret":   strings.TrimSpace(cfg.ClientSecretEnc) != "",
		"CallbackURL": oidc.Settings{BaseURL: cfg.BaseURL}.CallbackURL(r),
		"Flash":       flash, "Err": errMsg,
	}
}

func (a *app) tokensData(newToken, errMsg string) map[string]any {
	all, _ := db.ListAPITokens(a.db)
	tokens := all[:0:0]
	for _, t := range all {
		if !t.Revoked { // hide revoked tokens — they're dead; keep the list to what's live
			tokens = append(tokens, t)
		}
	}
	return map[string]any{"Tokens": tokens, "New": newToken, "Err": errMsg}
}

func (a *app) auditData(flash, errMsg string) map[string]any {
	days, _ := db.GetAuditRetentionDays(a.db)
	return map[string]any{"Days": days, "Flash": flash, "Err": errMsg}
}

func (a *app) totpData(r *http.Request, extra map[string]any) map[string]any {
	user, _ := a.nextUser(r)
	d := map[string]any{"Enabled": strings.TrimSpace(user.TOTPSecret) != ""}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

// nextSettings renders the full settings page (all cards).
func (a *app) nextSettings(w http.ResponseWriter, r *http.Request) {
	a.renderNext(w, "settings.html", map[string]any{
		"Notif":  a.notifData("", ""),
		"OIDC":   a.oidcData(r, "", ""),
		"Tokens": a.tokensData("", ""),
		"Audit":  a.auditData("", ""),
		"TOTP":   a.totpData(r, nil),
	})
}

// --- notifications ----------------------------------------------------------------

func (a *app) nextNotifSave(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	req := models.NotificationSettings{
		AppriseURL:       strings.TrimSpace(r.FormValue("apprise_url")),
		UpdatesAvailable: r.FormValue("updates_available") != "",
		AutoApplySuccess: r.FormValue("auto_apply_success") != "",
		AutoApplyFailure: r.FormValue("auto_apply_failure") != "",
		ScanFailure:      r.FormValue("scan_failure") != "",
	}
	if msg := validateAppriseTarget(req.AppriseURL, true); msg != "" {
		a.renderNext(w, "notifcard", a.notifData("", msg))
		return
	}
	if err := db.UpsertNotificationSettings(a.db, req); err != nil {
		a.renderNext(w, "notifcard", a.notifData("", "Failed to save notification settings."))
		return
	}
	a.renderNext(w, "notifcard", a.notifData("Saved.", ""))
}

func (a *app) nextNotifTest(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	target := strings.TrimSpace(r.FormValue("apprise_url"))
	if target == "" {
		target = a.currentAppriseURL()
	}
	if msg := validateAppriseTarget(target, false); msg != "" {
		a.renderNext(w, "notifcard", a.notifData("", msg))
		return
	}
	if err := a.notifier.Send(target, "Patchdeck test notification"); err != nil {
		a.renderNext(w, "notifcard", a.notifData("", "Test failed: "+err.Error()))
		return
	}
	a.renderNext(w, "notifcard", a.notifData("Test notification sent.", ""))
}

// --- OIDC / SSO -------------------------------------------------------------------

func (a *app) nextOIDCSave(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	existing, err := db.GetOIDCConfig(a.db)
	if err != nil {
		a.renderNext(w, "oidccard", a.oidcData(r, "", "Failed to load OIDC settings."))
		return
	}
	enabled := r.FormValue("enabled") != ""
	issuer := strings.TrimSpace(r.FormValue("issuer"))
	clientID := strings.TrimSpace(r.FormValue("client_id"))
	baseURL := strings.TrimSpace(r.FormValue("base_url"))
	if issuer != "" && !isHTTPURL(issuer) {
		a.renderNext(w, "oidccard", a.oidcData(r, "", "Issuer must be an absolute http(s) URL."))
		return
	}
	if baseURL != "" && !isHTTPURL(baseURL) {
		a.renderNext(w, "oidccard", a.oidcData(r, "", "Public base URL must be an absolute http(s) URL."))
		return
	}
	secretEnc := existing.ClientSecretEnc
	if r.FormValue("clear_secret") != "" {
		secretEnc = ""
	} else if s := strings.TrimSpace(r.FormValue("client_secret")); s != "" {
		enc, encErr := a.secrets.Encrypt([]byte(s))
		if encErr != nil {
			a.renderNext(w, "oidccard", a.oidcData(r, "", "Failed to encrypt client secret."))
			return
		}
		secretEnc = enc
	}
	if enabled && (issuer == "" || clientID == "" || secretEnc == "") {
		a.renderNext(w, "oidccard", a.oidcData(r, "", "Issuer, Client ID, and Client Secret are all required to enable SSO."))
		return
	}
	cfg := models.OIDCConfig{
		Enabled: enabled, Issuer: issuer, ClientID: clientID, ClientSecretEnc: secretEnc,
		BaseURL: baseURL, Allowed: strings.TrimSpace(r.FormValue("allowed")), ButtonLabel: strings.TrimSpace(r.FormValue("button_label")),
	}
	if err := db.SetOIDCConfig(a.db, cfg); err != nil {
		a.renderNext(w, "oidccard", a.oidcData(r, "", "Failed to save OIDC settings."))
		return
	}
	a.renderNext(w, "oidccard", a.oidcData(r, "Saved.", ""))
}

// --- API tokens -------------------------------------------------------------------

func (a *app) nextTokenCreate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		a.renderNext(w, "tokenscard", a.tokensData("", "Token name is required."))
		return
	}
	plaintext, _, err := db.CreateAPIToken(a.db, name)
	if err != nil {
		a.renderNext(w, "tokenscard", a.tokensData("", "Failed to create API token."))
		return
	}
	a.renderNext(w, "tokenscard", a.tokensData(plaintext, ""))
}

func (a *app) nextTokenRevoke(w http.ResponseWriter, r *http.Request) {
	if err := db.RevokeAPIToken(a.db, chi.URLParam(r, "id")); err != nil {
		a.renderNext(w, "tokenscard", a.tokensData("", "Failed to revoke token."))
		return
	}
	a.renderNext(w, "tokenscard", a.tokensData("", ""))
}

// --- audit retention --------------------------------------------------------------

func (a *app) nextAuditSave(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	days, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("retention_days")))
	if days != 0 && days < 30 {
		a.renderNext(w, "auditcard", a.auditData("", "Minimum retention is 30 days (use 0 for unlimited)."))
		return
	}
	if err := db.SetAuditRetentionDays(a.db, days); err != nil {
		a.renderNext(w, "auditcard", a.auditData("", "Failed to save audit retention."))
		return
	}
	a.renderNext(w, "auditcard", a.auditData("Saved.", ""))
}

// --- two-factor (TOTP) ------------------------------------------------------------

func (a *app) nextTotpSetup(w http.ResponseWriter, r *http.Request) {
	user, ok := a.nextUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if strings.TrimSpace(user.TOTPSecret) != "" {
		a.renderNext(w, "totpcard", a.totpData(r, map[string]any{"Err": "Two-factor is already enabled."}))
		return
	}
	generated, _, genErr := auth.NewTOTPSecret(totpIssuer, user.Username)
	if genErr != nil {
		a.renderNext(w, "totpcard", a.totpData(r, map[string]any{"Err": "Failed to generate a secret."}))
		return
	}
	secret := auth.NormalizeBase32Secret(generated)
	otpauth, err := auth.GenerateTOTPWithSecret(totpIssuer, user.Username, secret)
	if err != nil {
		a.renderNext(w, "totpcard", a.totpData(r, map[string]any{"Err": "Failed to prepare configuration."}))
		return
	}
	png, err := qrcode.Encode(otpauth, qrcode.Medium, 200)
	if err != nil {
		a.renderNext(w, "totpcard", a.totpData(r, map[string]any{"Err": "Failed to render QR code."}))
		return
	}
	a.renderNext(w, "totpcard", a.totpData(r, map[string]any{
		"Setup": true, "Secret": secret,
		"QR": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
	}))
}

func (a *app) nextTotpConfirm(w http.ResponseWriter, r *http.Request) {
	user, ok := a.nextUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	_ = r.ParseForm()
	secret := strings.TrimSpace(r.FormValue("secret"))
	code := strings.TrimSpace(r.FormValue("code"))
	reSetup := func(msg string) {
		png, _ := qrcode.Encode(mustOtpauth(user.Username, secret), qrcode.Medium, 200)
		a.renderNext(w, "totpcard", a.totpData(r, map[string]any{
			"Setup": true, "Secret": secret, "Err": msg,
			"QR": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		}))
	}
	if secret == "" || code == "" {
		reSetup("Enter the 6-digit code from your authenticator.")
		return
	}
	if !auth.ValidateBase32Secret(secret) {
		a.renderNext(w, "totpcard", a.totpData(r, map[string]any{"Err": "Invalid secret."}))
		return
	}
	normalized := auth.NormalizeBase32Secret(secret)
	if !auth.ValidateTOTP(normalized, code) {
		reSetup("That code didn't match — try the current one.")
		return
	}
	if err := db.SetUserTOTP(a.db, user.ID, normalized); err != nil {
		reSetup("Failed to save the secret.")
		return
	}
	_ = db.DeleteRecoveryCodes(a.db, user.ID)
	codes := auth.GenerateRecoveryCodes(10)
	hashed := make([]string, 0, len(codes))
	for _, c := range codes {
		h, herr := bcrypt.GenerateFromPassword([]byte(strings.ReplaceAll(c, "-", "")), bcrypt.DefaultCost)
		if herr != nil {
			reSetup("Failed to protect recovery codes.")
			return
		}
		hashed = append(hashed, string(h))
	}
	_ = db.SaveRecoveryCodes(a.db, user.ID, hashed)
	a.renderNext(w, "totpcard", a.totpData(r, map[string]any{"Enabled": true, "Recovery": codes}))
}

func mustOtpauth(username, secret string) string {
	uri, _ := auth.GenerateTOTPWithSecret(totpIssuer, username, secret)
	return uri
}

func (a *app) nextTotpDisable(w http.ResponseWriter, r *http.Request) {
	user, ok := a.nextUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if strings.TrimSpace(user.TOTPSecret) == "" {
		a.renderNext(w, "totpcard", a.totpData(r, nil))
		return
	}
	_ = r.ParseForm()
	if !auth.CheckPassword(user.PasswordHash, r.FormValue("password")) {
		a.renderNext(w, "totpcard", a.totpData(r, map[string]any{"Err": "Password incorrect."}))
		return
	}
	_ = db.ClearUserTOTP(a.db, user.ID)
	_ = db.DeleteRecoveryCodes(a.db, user.ID)
	a.renderNext(w, "totpcard", a.totpData(r, map[string]any{"Enabled": false, "Flash": "Two-factor disabled."}))
}
