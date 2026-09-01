package oidc

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSettingsAllow(t *testing.T) {
	cases := []struct {
		name     string
		allowed  string
		email    string
		verified bool
		groups   []string
		want     bool
	}{
		{"empty allowlist admits all (IdP is the gate)", "", "anyone@example.com", true, nil, true},
		{"empty allowlist admits even unverified (IdP is the gate)", "", "anyone@example.com", false, nil, true},
		{"verified email match, case-insensitive", "Roy@Example.com", "roy@example.com", true, nil, true},
		{"UNVERIFIED email match is rejected", "roy@example.com", "roy@example.com", false, nil, false},
		{"email miss", "roy@example.com", "eve@example.com", true, nil, false},
		{"group match (email need not be verified)", "patchdeck-admins", "eve@example.com", false, []string{"patchdeck-admins"}, true},
		{"group match among several", "ops, patchdeck", "eve@example.com", false, []string{"users", "patchdeck"}, true},
		{"group miss", "patchdeck", "eve@example.com", true, []string{"users"}, false},
		{"blank email with allowlist set is not admitted", "roy@example.com", "", true, nil, false},
		{"whitespace in allowlist entries tolerated", "  roy@example.com , patchdeck ", "ROY@example.com", true, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := Settings{Allowed: c.allowed}
			if got := s.Allow(c.email, c.verified, c.groups); got != c.want {
				t.Fatalf("Allow(%q,verified=%v,%v) with allowlist %q = %v, want %v", c.email, c.verified, c.groups, c.allowed, got, c.want)
			}
		})
	}
}

func TestCallbackURL_ExplicitBaseURL(t *testing.T) {
	s := Settings{BaseURL: "https://patchdeck.example.com/"}
	r := httptest.NewRequest(http.MethodGet, "http://internal:6070/auth/oidc/login", nil)
	got := s.CallbackURL(r)
	want := "https://patchdeck.example.com" + CallbackPath
	if got != want {
		t.Fatalf("CallbackURL = %q, want %q", got, want)
	}
}

func TestCallbackURL_ForwardedHeaders(t *testing.T) {
	s := Settings{} // no explicit base URL -> derive from the request/proxy
	r := httptest.NewRequest(http.MethodGet, "http://internal:6070/auth/oidc/login", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "patchdeck.orbit.example.com")
	got := s.CallbackURL(r)
	want := "https://patchdeck.orbit.example.com" + CallbackPath
	if got != want {
		t.Fatalf("CallbackURL = %q, want %q", got, want)
	}
}

func TestCallbackURL_MultiHopForwarded(t *testing.T) {
	s := Settings{}
	r := httptest.NewRequest(http.MethodGet, "http://internal:6070/auth/oidc/login", nil)
	r.Header.Set("X-Forwarded-Proto", "https, http")
	r.Header.Set("X-Forwarded-Host", "patchdeck.example.com, internal:6070")
	got := s.CallbackURL(r)
	want := "https://patchdeck.example.com" + CallbackPath
	if got != want {
		t.Fatalf("CallbackURL = %q, want %q", got, want)
	}
}

func TestLabelDefault(t *testing.T) {
	if got := (Settings{}).Label(); got != "Sign in with SSO" {
		t.Fatalf("default label = %q", got)
	}
	if got := (Settings{ButtonLabel: "  Pocket ID  "}).Label(); got != "Pocket ID" {
		t.Fatalf("custom label = %q", got)
	}
}
