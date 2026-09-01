package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"patchdeck/api/internal/db"
	"patchdeck/api/internal/models"
	"patchdeck/api/internal/oidc"
)

// OIDC login uses three short-lived, path-scoped cookies to carry the anti-CSRF state,
// the replay nonce, and the PKCE verifier across the redirect to the IdP and back. They
// are scoped to /auth/oidc so they're never sent with ordinary app requests, and cleared
// as soon as the callback resolves (success or failure).
const (
	oidcStateCookie    = "pd_oidc_state"
	oidcNonceCookie    = "pd_oidc_nonce"
	oidcVerifierCookie = "pd_oidc_verifier"
	oidcCookiePath     = "/auth/oidc"
	oidcTempTTL        = 10 * time.Minute
	oidcDiscoveryWait  = 15 * time.Second
)

func randToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func setOIDCTempCookie(w http.ResponseWriter, name, val string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: val, Path: oidcCookiePath,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		MaxAge: int(oidcTempTTL.Seconds()),
	})
}

func clearOIDCTempCookies(w http.ResponseWriter) {
	for _, n := range []string{oidcStateCookie, oidcNonceCookie, oidcVerifierCookie} {
		http.SetCookie(w, &http.Cookie{
			Name: n, Value: "", Path: oidcCookiePath,
			HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1,
		})
	}
}

// oidcSettings loads the stored config and decrypts the client secret into a usable form.
func (a *app) oidcSettings() (oidc.Settings, models.OIDCConfig, error) {
	cfg, err := db.GetOIDCConfig(a.db)
	if err != nil {
		return oidc.Settings{}, cfg, err
	}
	s := oidc.Settings{
		Enabled:     cfg.Enabled,
		Issuer:      cfg.Issuer,
		ClientID:    cfg.ClientID,
		BaseURL:     cfg.BaseURL,
		Allowed:     cfg.Allowed,
		ButtonLabel: cfg.ButtonLabel,
	}
	if strings.TrimSpace(cfg.ClientSecretEnc) != "" {
		plain, derr := a.secrets.Decrypt(cfg.ClientSecretEnc)
		if derr != nil {
			return s, cfg, fmt.Errorf("decrypt oidc client secret: %w", derr)
		}
		s.ClientSecret = string(plain)
	}
	return s, cfg, nil
}

func (a *app) oidcConfigured(cfg models.OIDCConfig) bool {
	return cfg.Enabled && strings.TrimSpace(cfg.Issuer) != "" && strings.TrimSpace(cfg.ClientID) != "" && strings.TrimSpace(cfg.ClientSecretEnc) != ""
}

// oidcStatus (public) tells the login page whether to render the SSO button. It reveals
// only whether SSO is on and its label — never issuer, client id, or secret.
func (a *app) oidcStatus(w http.ResponseWriter, _ *http.Request) {
	cfg, err := db.GetOIDCConfig(a.db)
	if err != nil {
		writeJSON(w, 200, map[string]any{"enabled": false})
		return
	}
	writeJSON(w, 200, map[string]any{
		"enabled": a.oidcConfigured(cfg),
		"label":   oidc.Settings{ButtonLabel: cfg.ButtonLabel}.Label(),
	})
}

// oidcFail clears the transient cookies and bounces back to the SPA with a short,
// non-sensitive reason code the login page can surface.
func (a *app) oidcFail(w http.ResponseWriter, r *http.Request, reason string) {
	clearOIDCTempCookies(w)
	http.Redirect(w, r, "/?sso_error="+reason, http.StatusFound)
}

// oidcLogin (public) begins the Authorization Code + PKCE flow: discover the provider,
// stash state/nonce/verifier in cookies, and redirect to the IdP.
func (a *app) oidcLogin(w http.ResponseWriter, r *http.Request) {
	s, cfg, err := a.oidcSettings()
	if err != nil || !a.oidcConfigured(cfg) {
		a.oidcFail(w, r, "sso_disabled")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), oidcDiscoveryWait)
	defer cancel()
	authr, err := oidc.New(ctx, s, s.CallbackURL(r))
	if err != nil {
		log.Printf("oidc: provider discovery failed: %v", err)
		a.oidcFail(w, r, "sso_unavailable")
		return
	}
	state, e1 := randToken(24)
	nonce, e2 := randToken(24)
	if e1 != nil || e2 != nil {
		a.oidcFail(w, r, "sso_error")
		return
	}
	verifier := oidc.NewPKCEVerifier()
	setOIDCTempCookie(w, oidcStateCookie, state)
	setOIDCTempCookie(w, oidcNonceCookie, nonce)
	setOIDCTempCookie(w, oidcVerifierCookie, verifier)
	http.Redirect(w, r, authr.AuthCodeURL(state, nonce, verifier), http.StatusFound)
}

// oidcCallback (public) completes the flow: validate state, exchange the code (PKCE),
// verify the ID token + nonce, enforce the allowlist, then mint the admin session.
func (a *app) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if e := strings.TrimSpace(r.URL.Query().Get("error")); e != "" {
		log.Printf("oidc: provider returned error: %s", e)
		a.oidcFail(w, r, "sso_denied")
		return
	}
	stateParam := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	stateCookie, e1 := r.Cookie(oidcStateCookie)
	nonceCookie, e2 := r.Cookie(oidcNonceCookie)
	verifierCookie, e3 := r.Cookie(oidcVerifierCookie)
	if e1 != nil || e2 != nil || e3 != nil || code == "" {
		a.oidcFail(w, r, "sso_expired")
		return
	}
	if subtle.ConstantTimeCompare([]byte(stateParam), []byte(stateCookie.Value)) != 1 {
		a.oidcFail(w, r, "sso_state")
		return
	}
	s, cfg, err := a.oidcSettings()
	if err != nil || !a.oidcConfigured(cfg) {
		a.oidcFail(w, r, "sso_disabled")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), oidcDiscoveryWait)
	defer cancel()
	authr, err := oidc.New(ctx, s, s.CallbackURL(r))
	if err != nil {
		a.oidcFail(w, r, "sso_unavailable")
		return
	}
	claims, nonce, err := authr.Exchange(ctx, code, verifierCookie.Value)
	if err != nil {
		log.Printf("oidc: token exchange/verify failed: %v", err)
		a.oidcFail(w, r, "sso_verify")
		return
	}
	if subtle.ConstantTimeCompare([]byte(nonce), []byte(nonceCookie.Value)) != 1 {
		a.oidcFail(w, r, "sso_nonce")
		return
	}
	if !s.Allow(claims.Email, claims.EmailVerified, claims.Groups) {
		log.Printf("oidc: identity not permitted by allowlist (sub=%s)", claims.Subject)
		a.oidcFail(w, r, "sso_forbidden")
		return
	}
	admin, err := db.GetPrimaryAdmin(a.db)
	if err != nil {
		log.Printf("oidc: no admin account to grant: %v", err)
		a.oidcFail(w, r, "sso_no_admin")
		return
	}
	sessTok, err := db.CreateSession(a.db, admin.ID, admin.Username, admin.Role, sessionTTL)
	if err != nil {
		a.oidcFail(w, r, "sso_error")
		return
	}
	clearOIDCTempCookies(w)
	setSessionCookie(w, sessTok)
	_ = db.RecordActivity(a.db, "", "", "login_sso", fmt.Sprintf("SSO sign-in via OIDC (%s)", oidcIdentityLabel(claims)))
	http.Redirect(w, r, "/", http.StatusFound)
}

func oidcIdentityLabel(c *oidc.Claims) string {
	if c == nil {
		return "unknown"
	}
	for _, v := range []string{c.Email, c.PreferredUsername, c.Subject} {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return "unknown"
}

// getOIDCSettings (admin) returns the config for the settings UI, minus the secret.
func (a *app) getOIDCSettings(w http.ResponseWriter, r *http.Request) {
	cfg, err := db.GetOIDCConfig(a.db)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to load OIDC settings"})
		return
	}
	writeJSON(w, 200, map[string]any{
		"enabled":           cfg.Enabled,
		"issuer":            cfg.Issuer,
		"client_id":         cfg.ClientID,
		"has_client_secret": strings.TrimSpace(cfg.ClientSecretEnc) != "",
		"base_url":          cfg.BaseURL,
		"allowed":           cfg.Allowed,
		"button_label":      cfg.ButtonLabel,
		"callback_url":      oidc.Settings{BaseURL: cfg.BaseURL}.CallbackURL(r),
	})
}

// putOIDCSettings (admin) persists the config. The client secret is write-only: a blank
// secret keeps the stored one, a non-blank one replaces it (encrypted), and clear_secret
// wipes it. SSO can't be enabled without issuer, client id, and a stored secret.
func (a *app) putOIDCSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled      bool   `json:"enabled"`
		Issuer       string `json:"issuer"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		ClearSecret  bool   `json:"clear_secret"`
		BaseURL      string `json:"base_url"`
		Allowed      string `json:"allowed"`
		ButtonLabel  string `json:"button_label"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	existing, err := db.GetOIDCConfig(a.db)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to load OIDC settings"})
		return
	}
	issuer := strings.TrimSpace(req.Issuer)
	clientID := strings.TrimSpace(req.ClientID)
	baseURL := strings.TrimSpace(req.BaseURL)
	if issuer != "" && !isHTTPURL(issuer) {
		writeJSON(w, 400, map[string]string{"error": "Issuer must be an absolute http(s) URL"})
		return
	}
	if baseURL != "" && !isHTTPURL(baseURL) {
		writeJSON(w, 400, map[string]string{"error": "Public base URL must be an absolute http(s) URL"})
		return
	}
	secretEnc := existing.ClientSecretEnc
	if req.ClearSecret {
		secretEnc = ""
	} else if strings.TrimSpace(req.ClientSecret) != "" {
		enc, err := a.secrets.Encrypt([]byte(strings.TrimSpace(req.ClientSecret)))
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "Failed to encrypt client secret"})
			return
		}
		secretEnc = enc
	}
	if req.Enabled && (issuer == "" || clientID == "" || secretEnc == "") {
		writeJSON(w, 400, map[string]string{"error": "Issuer, Client ID, and Client Secret are all required to enable SSO"})
		return
	}
	cfg := models.OIDCConfig{
		Enabled:         req.Enabled,
		Issuer:          issuer,
		ClientID:        clientID,
		ClientSecretEnc: secretEnc,
		BaseURL:         baseURL,
		Allowed:         strings.TrimSpace(req.Allowed),
		ButtonLabel:     strings.TrimSpace(req.ButtonLabel),
	}
	if err := db.SetOIDCConfig(a.db, cfg); err != nil {
		writeJSON(w, 500, map[string]string{"error": "Failed to save OIDC settings"})
		return
	}
	writeJSON(w, 200, map[string]string{"message": "OIDC settings updated"})
}

func isHTTPURL(s string) bool {
	u, err := neturl.Parse(strings.TrimSpace(s))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
