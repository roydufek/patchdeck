package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestWebManifestInstallable guards the "Install app" signal: the manifest parses, carries
// what Chrome needs, and every icon it names is served (unauthenticated) at the size it claims.
func TestWebManifestInstallable(t *testing.T) {
	a := &app{}
	r := chi.NewRouter()
	r.Get("/assets/{file}", a.nextAsset)
	r.Get("/manifest.webmanifest", a.nextManifest)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/manifest+json" {
		t.Fatalf("manifest: code %d, type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var m struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		ShortName string `json:"short_name"`
		StartURL  string `json:"start_url"`
		Scope     string `json:"scope"`
		Display   string `json:"display"`
		Icons     []struct{ Src, Sizes, Type, Purpose string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("manifest JSON: %v", err)
	}
	if m.ShortName == "" || m.Name == "" || m.StartURL != "/" || m.Scope != "/" || m.ID != "/" || m.Display != "standalone" {
		t.Fatalf("manifest fields: %+v", m)
	}

	seen := map[string]bool{}
	for _, ic := range m.Icons {
		if !strings.HasPrefix(ic.Src, "/") || strings.Contains(ic.Src, "://") {
			t.Errorf("icon %s: want a root-relative URL", ic.Src)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ic.Src, nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("icon %s: code %d, type %q", ic.Src, rec.Code, rec.Header().Get("Content-Type"))
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(rec.Body.Bytes()))
		if err != nil {
			t.Fatalf("icon %s: %v", ic.Src, err)
		}
		if got := fmt.Sprintf("%dx%d", cfg.Width, cfg.Height); got != ic.Sizes {
			t.Errorf("icon %s: is %s, manifest says %s", ic.Src, got, ic.Sizes)
		}
		seen[ic.Purpose+" "+ic.Sizes] = true
	}
	for _, want := range []string{"any 192x192", "any 512x512", "maskable 512x512"} {
		if !seen[want] {
			t.Errorf("manifest lacks a %s icon", want)
		}
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/apple-touch-icon.png", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("apple-touch-icon: code %d", rec.Code)
	}
}

// TestPagesDeclareManifest: every full page (login included) must link the manifest,
// or the browser never sees the install signal on that page.
func TestPagesDeclareManifest(t *testing.T) {
	for _, f := range []string{"activity", "dashboard", "detail", "hostform", "login", "schedules", "settings"} {
		b, err := nextTmplFS.ReadFile("spiketmpl/" + f + ".html")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `{{template "pwahead"}}`) {
			t.Errorf("%s.html: missing {{template \"pwahead\"}} in <head>", f)
		}
	}
}
