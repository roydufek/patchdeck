package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func portOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, p, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func healthMux(status int) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
	return m
}

// The baked-in HEALTHCHECK must pass in BOTH serving modes (plain HTTP behind a proxy, and the
// self-signed HTTPS the image defaults to) and fail on a bad status or a dead port — a wrong probe
// would mark a healthy container unhealthy and a health-aware proxy (Traefik) would drop its route.
func TestRunHealthcheck(t *testing.T) {
	plain := httptest.NewServer(healthMux(http.StatusOK))
	defer plain.Close()
	t.Setenv("PATCHDECK_TLS", "false")
	t.Setenv("PATCHDECK_PORT", portOf(t, plain.URL))
	if rc := runHealthcheck(); rc != 0 {
		t.Errorf("plain HTTP, 200: rc=%d want 0", rc)
	}

	selfSigned := httptest.NewTLSServer(healthMux(http.StatusOK))
	defer selfSigned.Close()
	t.Setenv("PATCHDECK_TLS", "") // unset -> default true, same as the server
	t.Setenv("PATCHDECK_PORT", portOf(t, selfSigned.URL))
	if rc := runHealthcheck(); rc != 0 {
		t.Errorf("self-signed HTTPS, 200: rc=%d want 0", rc)
	}

	sick := httptest.NewServer(healthMux(http.StatusServiceUnavailable))
	defer sick.Close()
	t.Setenv("PATCHDECK_TLS", "false")
	t.Setenv("PATCHDECK_PORT", portOf(t, sick.URL))
	if rc := runHealthcheck(); rc != 1 {
		t.Errorf("503: rc=%d want 1", rc)
	}

	dead := httptest.NewServer(healthMux(http.StatusOK))
	deadPort := portOf(t, dead.URL)
	dead.Close()
	t.Setenv("PATCHDECK_PORT", deadPort)
	if rc := runHealthcheck(); rc != 1 {
		t.Errorf("nothing listening: rc=%d want 1", rc)
	}
}
