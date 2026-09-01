// Package oidc wires OpenID Connect (Authorization Code + PKCE) login into
// Patchdeck. It is a thin, testable wrapper over coreos/go-oidc + x/oauth2: the
// provider is discovered from the issuer, the ID token is verified (signature via
// JWKS, issuer, audience, expiry), and a small set of claims is returned. The rest
// of the app decides who those claims map to — here we only authenticate.
package oidc

import (
	"context"
	"errors"
	"net/http"
	"strings"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// CallbackPath is the fixed path the IdP redirects back to. It is deliberately
// constant so the redirect URI can't be influenced by request-controlled input.
const CallbackPath = "/auth/oidc/callback"

// Settings is the OIDC configuration (persisted in app_settings). ClientSecret is the
// DECRYPTED secret — callers decrypt the stored ciphertext before building an Authenticator.
type Settings struct {
	Enabled      bool
	Issuer       string
	ClientID     string
	ClientSecret string
	BaseURL      string // optional explicit public origin, e.g. https://patchdeck.example.com
	Allowed      string // optional comma-separated allowlist of emails and/or group names
	ButtonLabel  string
}

// Label is the sign-in button text, defaulting to a generic SSO label.
func (s Settings) Label() string {
	if l := strings.TrimSpace(s.ButtonLabel); l != "" {
		return l
	}
	return "Sign in with SSO"
}

// CallbackURL returns the absolute redirect URI registered with the IdP. It prefers
// the explicitly configured BaseURL; otherwise it derives the public origin from the
// request, honoring the reverse proxy's X-Forwarded-Proto / X-Forwarded-Host (Patchdeck
// typically runs behind Traefik, which terminates TLS upstream).
func (s Settings) CallbackURL(r *http.Request) string {
	if b := strings.TrimSpace(s.BaseURL); b != "" {
		return strings.TrimRight(b, "/") + CallbackPath
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); p != "" {
		// A comma-joined list means multiple proxies; the first hop is the client-facing one.
		if i := strings.IndexByte(p, ','); i >= 0 {
			p = p[:i]
		}
		scheme = strings.TrimSpace(p)
	}
	host := r.Host
	if h := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); h != "" {
		if i := strings.IndexByte(h, ','); i >= 0 {
			h = h[:i]
		}
		host = strings.TrimSpace(h)
	}
	return scheme + "://" + host + CallbackPath
}

// Allow reports whether the authenticated identity is permitted. An empty allowlist
// means the IdP is the sole gate (recommended — restrict the client in the IdP itself).
// When set, the email (case-insensitive) or any of the user's groups must match an entry.
//
// The email claim is only trusted for matching when the IdP asserts email_verified=true:
// on a shared/multi-tenant IdP a user may be able to set an unverified email to an
// allowlisted address, so an unverified email must never satisfy the gate. Group claims
// are IdP-asserted membership (not user-editable identity), so they match as-is. Providers
// that don't emit email_verified can't use email allowlisting — use groups or restrict the
// client in the IdP instead (see docs/oidc.md).
func (s Settings) Allow(email string, emailVerified bool, groups []string) bool {
	raw := strings.TrimSpace(s.Allowed)
	if raw == "" {
		return true
	}
	allow := map[string]bool{}
	for _, e := range strings.Split(raw, ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			allow[e] = true
		}
	}
	if emailVerified {
		if e := strings.ToLower(strings.TrimSpace(email)); e != "" && allow[e] {
			return true
		}
	}
	for _, g := range groups {
		if g = strings.ToLower(strings.TrimSpace(g)); g != "" && allow[g] {
			return true
		}
	}
	return false
}

// Claims is the subset of ID-token claims Patchdeck consumes.
type Claims struct {
	Subject           string   `json:"sub"`
	Email             string   `json:"email"`
	EmailVerified     bool     `json:"email_verified"`
	Name              string   `json:"name"`
	PreferredUsername string   `json:"preferred_username"`
	Groups            []string `json:"groups"`
}

// Authenticator wraps a discovered OIDC provider plus the OAuth2 config for one client.
type Authenticator struct {
	verifier *gooidc.IDTokenVerifier
	oauth    oauth2.Config
}

// New discovers the provider (OIDC discovery on the issuer) and builds the authenticator.
// The network call to the issuer happens here, so a bad/unreachable issuer surfaces
// immediately as an error rather than a broken redirect.
func New(ctx context.Context, s Settings, callbackURL string) (*Authenticator, error) {
	issuer := strings.TrimRight(strings.TrimSpace(s.Issuer), "/")
	if issuer == "" || strings.TrimSpace(s.ClientID) == "" {
		return nil, errors.New("oidc: issuer and client id are required")
	}
	provider, err := gooidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	return &Authenticator{
		verifier: provider.Verifier(&gooidc.Config{ClientID: s.ClientID}),
		oauth: oauth2.Config{
			ClientID:     s.ClientID,
			ClientSecret: s.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  callbackURL,
			Scopes:       []string{gooidc.ScopeOpenID, "profile", "email", "groups"},
		},
	}, nil
}

// NewPKCEVerifier returns a fresh RFC 7636 code_verifier (high-entropy, URL-safe).
// Kept here so callers don't need to import x/oauth2 directly.
func NewPKCEVerifier() string {
	return oauth2.GenerateVerifier()
}

// AuthCodeURL builds the provider authorization URL with an anti-CSRF state, a replay
// nonce, and a PKCE (S256) challenge derived from verifier.
func (a *Authenticator) AuthCodeURL(state, nonce, verifier string) string {
	return a.oauth.AuthCodeURL(state,
		oauth2.SetAuthURLParam("nonce", nonce),
		oauth2.S256ChallengeOption(verifier),
	)
}

// Exchange completes the code exchange (sending the PKCE verifier), verifies the
// returned ID token (signature, issuer, audience, expiry), and returns its claims plus
// the token's nonce for the caller to match against the login request.
func (a *Authenticator) Exchange(ctx context.Context, code, verifier string) (*Claims, string, error) {
	tok, err := a.oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, "", err
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return nil, "", errors.New("oidc: no id_token in token response")
	}
	idToken, err := a.verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, "", err
	}
	var c Claims
	if err := idToken.Claims(&c); err != nil {
		return nil, "", err
	}
	return &c, idToken.Nonce, nil
}
